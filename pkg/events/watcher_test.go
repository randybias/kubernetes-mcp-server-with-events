package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type WatcherTestSuite struct {
	suite.Suite
}

func TestWatcherSuite(t *testing.T) {
	suite.Run(t, new(WatcherTestSuite))
}

// TestExponentialBackoff validates the backoff calculation
func (s *WatcherTestSuite) TestExponentialBackoff() {
	s.Run("returns correct sequence", func() {
		expected := []time.Duration{
			1 * time.Second,  // retry 0
			2 * time.Second,  // retry 1
			4 * time.Second,  // retry 2
			8 * time.Second,  // retry 3
			16 * time.Second, // retry 4
			30 * time.Second, // retry 5 (capped)
			30 * time.Second, // retry 6 (capped)
			30 * time.Second, // retry 7 (capped)
		}

		for i, exp := range expected {
			actual := exponentialBackoff(i)
			s.Equal(exp, actual, "backoff for retry %d should be %v", i, exp)
		}
	})

	s.Run("caps at 30 seconds", func() {
		for i := 6; i < 20; i++ {
			backoff := exponentialBackoff(i)
			s.Equal(30*time.Second, backoff, "backoff should be capped at 30s for retry %d", i)
		}
	})
}

// TestWatchReconnection validates watch reconnection behavior
func (s *WatcherTestSuite) TestWatchReconnection() {
	s.Run("reconnects after watch channel closes", func() {
		clientset := fake.NewClientset()

		watcher := watch.NewFake()
		reconnectCount := 0

		// Intercept watch calls to provide our fake watcher
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			reconnectCount++
			if reconnectCount == 1 {
				// First watch - close after one event
				go func() {
					time.Sleep(10 * time.Millisecond)
					watcher.Stop()
				}()
			}
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Name)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send an event before closing
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				ResourceVersion: "1",
			},
		}
		watcher.Add(event)

		time.Sleep(200 * time.Millisecond)

		s.GreaterOrEqual(reconnectCount, 1, "should have attempted reconnection")
	})
}

// TestWatchDegradedState validates degraded state after max retries
func (s *WatcherTestSuite) TestWatchDegradedState() {
	s.Run("sets degraded after 5 failures", func() {
		clientset := fake.NewClientset()

		failCount := 0
		maxRetries := 5

		// Make watch always fail
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			failCount++
			watcher := watch.NewFake()
			go func() {
				// Close immediately to simulate failure
				time.Sleep(1 * time.Millisecond)
				watcher.Stop()
			}()
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		errorCount := 0
		degradedCalled := false

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: maxRetries,
			DedupCache: dedupCache,
			OnError: func(err error) {
				errorCount++
			},
			OnDegraded: func() {
				degradedCalled = true
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		eventWatcher.Start(ctx)

		// Wait for degraded state
		time.Sleep(1500 * time.Millisecond)

		s.Equal(maxRetries, failCount, "should have failed exactly %d times", maxRetries)
		s.True(degradedCalled, "onDegraded callback should have been called")
		s.GreaterOrEqual(errorCount, maxRetries, "onError should have been called at least %d times", maxRetries)
	})
}

// TestWatchRetryCountReset validates retry count resets on success
func (s *WatcherTestSuite) TestWatchRetryCountReset() {
	s.Run("resets retry count on successful event", func() {
		clientset := fake.NewClientset()

		watcher := watch.NewFake()
		watchCallCount := 0

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchCallCount++
			if watchCallCount == 1 {
				// First call - close after event to trigger retry
				go func() {
					time.Sleep(50 * time.Millisecond)
					watcher.Stop()
				}()
			}
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		errorCount := 0

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
			OnError: func(err error) {
				errorCount++
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send an event to reset retry count
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				ResourceVersion: "1",
			},
		}
		watcher.Add(event)

		time.Sleep(100 * time.Millisecond)

		// Retry count should be reset after successful event
		s.Equal(0, eventWatcher.retryCount, "retry count should be reset after successful event")
	})
}

// TestWatchResourceVersionTracking validates resource version tracking
func (s *WatcherTestSuite) TestWatchResourceVersionTracking() {
	s.Run("tracks resource version for resume", func() {
		clientset := fake.NewClientset()

		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send events with increasing resource versions
		for i := 1; i <= 3; i++ {
			event := &v1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-event",
					Namespace:       "default",
					ResourceVersion: string(rune('0' + i)),
				},
			}
			watcher.Add(event)
			time.Sleep(20 * time.Millisecond)
		}

		time.Sleep(50 * time.Millisecond)

		s.NotEmpty(eventWatcher.resourceVersion, "resource version should be tracked")
	})
}

// TestWatchFiltering validates client-side event filtering
func (s *WatcherTestSuite) TestWatchFiltering() {
	s.Run("filters by namespace", func() {
		filters := &SubscriptionFilters{
			Namespaces: []string{"default", "kube-system"},
		}

		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			Filters:    filters,
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Namespace+"/"+event.Name)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send events in different namespaces
		events := []*v1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event1",
					Namespace:       "default",
					ResourceVersion: "1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event2",
					Namespace:       "other",
					ResourceVersion: "2",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event3",
					Namespace:       "kube-system",
					ResourceVersion: "3",
				},
			},
		}

		for _, event := range events {
			watcher.Add(event)
			time.Sleep(20 * time.Millisecond)
		}

		time.Sleep(50 * time.Millisecond)

		s.Contains(processedEvents, "default/event1", "should process event in default namespace")
		s.Contains(processedEvents, "kube-system/event3", "should process event in kube-system namespace")
		s.NotContains(processedEvents, "other/event2", "should not process event in other namespace")
	})

	s.Run("filters by type", func() {
		filters := &SubscriptionFilters{
			Type: "Warning",
		}

		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			Filters:    filters,
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Type+"/"+event.Name)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send events with different types
		events := []*v1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "warning1",
					Namespace:       "default",
					ResourceVersion: "1",
				},
				Type: "Warning",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "normal1",
					Namespace:       "default",
					ResourceVersion: "2",
				},
				Type: "Normal",
			},
		}

		for _, event := range events {
			watcher.Add(event)
			time.Sleep(20 * time.Millisecond)
		}

		time.Sleep(50 * time.Millisecond)

		s.Contains(processedEvents, "Warning/warning1", "should process Warning event")
		s.NotContains(processedEvents, "Normal/normal1", "should not process Normal event")
	})

	s.Run("filters by reason prefix", func() {
		filters := &SubscriptionFilters{
			Reason: "Back",
		}

		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			Filters:    filters,
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Reason)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send events with different reasons
		events := []*v1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event1",
					Namespace:       "default",
					ResourceVersion: "1",
				},
				Reason: "BackOff",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event2",
					Namespace:       "default",
					ResourceVersion: "2",
				},
				Reason: "Started",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "event3",
					Namespace:       "default",
					ResourceVersion: "3",
				},
				Reason: "BackoffLimitExceeded",
			},
		}

		for _, event := range events {
			watcher.Add(event)
			time.Sleep(20 * time.Millisecond)
		}

		time.Sleep(50 * time.Millisecond)

		s.Contains(processedEvents, "BackOff", "should process event with reason starting with 'Back'")
		s.Contains(processedEvents, "BackoffLimitExceeded", "should process event with reason starting with 'Back'")
		s.NotContains(processedEvents, "Started", "should not process event with reason not starting with 'Back'")
	})
}

// TestWatchDeduplication validates deduplication integration
func (s *WatcherTestSuite) TestWatchDeduplication() {
	s.Run("skips duplicate events within TTL", func() {
		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Name)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send duplicate event
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				UID:             "uid-123",
				ResourceVersion: "1",
			},
		}

		watcher.Add(event)
		time.Sleep(20 * time.Millisecond)

		// Send same event again (should be deduplicated)
		watcher.Add(event)
		time.Sleep(20 * time.Millisecond)

		s.Equal(1, len(processedEvents), "should only process event once")
	})

	s.Run("processes event with different resource version", func() {
		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.ResourceVersion)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)

		// Send event with resource version 1
		event1 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				UID:             "uid-123",
				ResourceVersion: "1",
			},
		}
		watcher.Add(event1)
		time.Sleep(20 * time.Millisecond)

		// Send same event with different resource version
		event2 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				UID:             "uid-123",
				ResourceVersion: "2",
			},
		}
		watcher.Add(event2)
		time.Sleep(20 * time.Millisecond)

		s.Equal(2, len(processedEvents), "should process both events with different resource versions")
		s.Contains(processedEvents, "1")
		s.Contains(processedEvents, "2")
	})
}

// TestInitialResourceVersion validates that the watcher uses initial resource version to skip historical events
func (s *WatcherTestSuite) TestInitialResourceVersion() {
	s.Run("uses initial resource version on first watch", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""

		// Capture the resource version used in the watch
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion
			return true, watch.NewFake(), nil
		})

		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "",
			InitialResourceVersion: "12345",
			MaxRetries:             5,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(50 * time.Millisecond)

		s.Equal("12345", capturedResourceVersion, "watcher should use initial resource version on first watch")
	})

	s.Run("uses updated resource version on reconnection", func() {
		clientset := fake.NewClientset()

		capturedResourceVersions := []string{}
		watchCallCount := 0
		var mu sync.Mutex

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)

			mu.Lock()
			capturedResourceVersions = append(capturedResourceVersions, watchAction.GetWatchRestrictions().ResourceVersion)
			callNum := watchCallCount
			watchCallCount++
			mu.Unlock()

			watcher := watch.NewFake()

			if callNum == 0 {
				// First watch - close after sending an event to trigger reconnection
				go func() {
					time.Sleep(10 * time.Millisecond)
					event := &v1.Event{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-event",
							Namespace:       "default",
							ResourceVersion: "67890",
						},
					}
					watcher.Add(event)
					time.Sleep(10 * time.Millisecond)
					watcher.Stop()
				}()
			}
			return true, watcher, nil
		})

		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "",
			InitialResourceVersion: "12345",
			MaxRetries:             5,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(300 * time.Millisecond)

		mu.Lock()
		numCalls := len(capturedResourceVersions)
		mu.Unlock()

		s.GreaterOrEqual(numCalls, 2, "should have at least 2 watch calls")
		if numCalls >= 2 {
			s.Equal("12345", capturedResourceVersions[0], "first watch should use initial resource version")
			s.Equal("67890", capturedResourceVersions[1], "second watch should use updated resource version from event")
		}
	})

	s.Run("handles empty initial resource version", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion
			return true, watch.NewFake(), nil
		})

		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "",
			InitialResourceVersion: "",
			MaxRetries:             5,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(50 * time.Millisecond)

		s.Equal("", capturedResourceVersion, "watcher should not set resource version when initial is empty")
	})
}

// TestWatch410ErrorHandling validates that 410 Gone errors clear the resourceVersion
func (s *WatcherTestSuite) TestWatch410ErrorHandling() {
	s.Run("clears resourceVersion on 410 Gone error", func() {
		clientset := fake.NewClientset()

		capturedResourceVersions := []string{}
		watchCallCount := 0
		var mu sync.Mutex

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)

			mu.Lock()
			capturedResourceVersions = append(capturedResourceVersions, watchAction.GetWatchRestrictions().ResourceVersion)
			callNum := watchCallCount
			watchCallCount++
			mu.Unlock()

			watcher := watch.NewFake()

			if callNum == 0 {
				// First watch - send an event to set resourceVersion, then send 410 error
				go func() {
					time.Sleep(10 * time.Millisecond)
					// Send an event to establish a resourceVersion
					event := &v1.Event{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-event",
							Namespace:       "default",
							ResourceVersion: "2256",
						},
					}
					watcher.Add(event)
					time.Sleep(10 * time.Millisecond)

					// Send 410 Gone error
					status := &metav1.Status{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Status",
							APIVersion: "v1",
						},
						Status:  "Failure",
						Message: "The resourceVersion for the provided watch is too old.",
						Reason:  "Expired",
						Code:    410,
					}
					watcher.Error(status)
				}()
			}
			return true, watcher, nil
		})

		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "",
			InitialResourceVersion: "",
			MaxRetries:             5,
		}

		eventWatcher := NewEventWatcher(config)
		// Need longer timeout to account for 2s exponential backoff on first retry
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		eventWatcher.Start(ctx)
		// Wait for initial watch + 410 error + 2s backoff + retry
		time.Sleep(2500 * time.Millisecond)

		mu.Lock()
		numCalls := len(capturedResourceVersions)
		mu.Unlock()

		s.GreaterOrEqual(numCalls, 2, "should have at least 2 watch calls (initial + retry after 410)")
		if numCalls >= 2 {
			s.Equal("", capturedResourceVersions[0], "first watch should start without resource version")
			s.Equal("", capturedResourceVersions[1], "second watch should not use stale resource version after 410 error")
		}
	})

	s.Run("continues normal operation after 410 error recovery", func() {
		clientset := fake.NewClientset()

		watchCallCount := 0
		processedEvents := []string{}
		var mu sync.Mutex

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			mu.Lock()
			callNum := watchCallCount
			watchCallCount++
			mu.Unlock()

			watcher := watch.NewFake()

			if callNum == 0 {
				// First watch - send 410 error immediately
				go func() {
					time.Sleep(10 * time.Millisecond)
					status := &metav1.Status{
						TypeMeta: metav1.TypeMeta{
							Kind:       "Status",
							APIVersion: "v1",
						},
						Status:  "Failure",
						Message: "The resourceVersion for the provided watch is too old.",
						Reason:  "Expired",
						Code:    410,
					}
					watcher.Error(status)
				}()
			} else {
				// Second watch (after 410) - send normal event
				go func() {
					time.Sleep(10 * time.Millisecond)
					event := &v1.Event{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "recovery-event",
							Namespace:       "default",
							ResourceVersion: "9999",
						},
					}
					watcher.Add(event)
				}()
			}
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  clientset,
			Namespace:  "",
			MaxRetries: 5,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				mu.Lock()
				processedEvents = append(processedEvents, event.Name)
				mu.Unlock()
			},
		}

		eventWatcher := NewEventWatcher(config)
		// Need longer timeout to account for 2s exponential backoff on first retry
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		eventWatcher.Start(ctx)
		// Wait for initial watch + 410 error + 2s backoff + retry + event processing
		time.Sleep(2500 * time.Millisecond)

		mu.Lock()
		numCalls := watchCallCount
		numProcessed := len(processedEvents)
		mu.Unlock()

		s.GreaterOrEqual(numCalls, 2, "should have reconnected after 410 error")
		s.GreaterOrEqual(numProcessed, 1, "should process events after recovering from 410 error")
		if numProcessed >= 1 {
			s.Contains(processedEvents, "recovery-event", "should process the recovery event")
		}
	})
}

// Mock objects to compile tests
var _ runtime.Object = &v1.Event{}
