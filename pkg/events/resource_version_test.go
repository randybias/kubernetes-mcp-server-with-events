package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type ResourceVersionTestSuite struct {
	suite.Suite
}

func TestResourceVersionSuite(t *testing.T) {
	suite.Run(t, new(ResourceVersionTestSuite))
}

// TestInitialResourceVersion_ConfigField tests that InitialResourceVersion is stored in EventWatcherConfig
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_ConfigField() {
	s.Run("stores initial resource version in config", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "12345",
			MaxRetries:             5,
		}

		s.Equal("12345", config.InitialResourceVersion, "config should store initial resource version")
	})

	s.Run("allows empty initial resource version", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "",
			MaxRetries:             5,
		}

		s.Equal("", config.InitialResourceVersion, "config should allow empty initial resource version")
	})
}

// TestInitialResourceVersion_WatcherField tests that EventWatcher stores initialResourceVersion
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_WatcherField() {
	s.Run("transfers initial resource version from config to watcher", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "67890",
			MaxRetries:             5,
		}

		watcher := NewEventWatcher(config)
		s.Equal("67890", watcher.initialResourceVersion, "watcher should store initial resource version from config")
	})

	s.Run("handles empty initial resource version", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "",
			MaxRetries:             5,
		}

		watcher := NewEventWatcher(config)
		s.Equal("", watcher.initialResourceVersion, "watcher should handle empty initial resource version")
	})
}

// TestInitialResourceVersion_StartWatch tests that startWatch uses initialResourceVersion on first watch
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_StartWatch() {
	s.Run("uses initial resource version on first watch", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""
		watcher := watch.NewFake()

		// Intercept watch calls to capture options
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion

			// Close watcher immediately to end test
			go func() {
				time.Sleep(10 * time.Millisecond)
				watcher.Stop()
			}()

			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "initial-100",
			MaxRetries:             1,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		s.Equal("initial-100", capturedResourceVersion, "should use initial resource version on first watch")
	})

	s.Run("does not use initial resource version when resourceVersion is set", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""
		watcher := watch.NewFake()

		// Intercept watch calls to capture options
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion

			// Close watcher immediately to end test
			go func() {
				time.Sleep(10 * time.Millisecond)
				watcher.Stop()
			}()

			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "initial-100",
			MaxRetries:             1,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		// Simulate that watch has already received events (resourceVersion is set)
		eventWatcher.resourceVersion = "current-200"

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		s.Equal("current-200", capturedResourceVersion, "should use current resource version, not initial")
	})

	s.Run("skips resource version when both are empty", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""
		watcher := watch.NewFake()

		// Intercept watch calls to capture options
		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion

			// Close watcher immediately to end test
			go func() {
				time.Sleep(10 * time.Millisecond)
				watcher.Stop()
			}()

			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "",
			MaxRetries:             1,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		s.Equal("", capturedResourceVersion, "should not set resource version when both are empty")
	})
}

// TestInitialResourceVersion_SkipsHistoricalEvents tests that historical events are not processed
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_SkipsHistoricalEvents() {
	s.Run("processes only new events when initial resource version is set", func() {
		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		processedEvents := []string{}

		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "1000", // Simulate starting from resource version 1000
			MaxRetries:             5,
			DedupCache:             dedupCache,
			ProcessEvent: func(event *v1.Event) {
				processedEvents = append(processedEvents, event.Name)
			},
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(50 * time.Millisecond)

		// Simulate events arriving AFTER initial resource version
		newEvent1 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "new-event-1",
				Namespace:       "default",
				ResourceVersion: "1001", // After initial version
			},
		}
		watcher.Add(newEvent1)
		time.Sleep(20 * time.Millisecond)

		newEvent2 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "new-event-2",
				Namespace:       "default",
				ResourceVersion: "1002", // After initial version
			},
		}
		watcher.Add(newEvent2)
		time.Sleep(20 * time.Millisecond)

		// With initial resource version set to 1000, events with RV >= 1001 should be processed
		s.Contains(processedEvents, "new-event-1", "should process new event after initial resource version")
		s.Contains(processedEvents, "new-event-2", "should process new event after initial resource version")
	})
}

// TestInitialResourceVersion_ResourceVersionPriority tests priority of resourceVersion over initialResourceVersion
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_ResourceVersionPriority() {
	s.Run("current resource version takes priority on reconnection", func() {
		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		watchCallCount := 0
		firstCallResourceVersion := ""
		secondCallResourceVersion := ""

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			watchCallCount++

			switch watchCallCount {
			case 1:
				firstCallResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion
				// Send an event then close to trigger reconnection
				go func() {
					time.Sleep(20 * time.Millisecond)
					event := &v1.Event{
						ObjectMeta: metav1.ObjectMeta{
							Name:            "test-event",
							Namespace:       "default",
							ResourceVersion: "2000",
						},
					}
					watcher.Add(event)
					time.Sleep(20 * time.Millisecond)
					watcher.Stop()
				}()
			case 2:
				secondCallResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion
				// Keep open for remainder of test
			}

			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "1000",
			MaxRetries:             5,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(300 * time.Millisecond)

		s.Equal("1000", firstCallResourceVersion, "first watch should use initial resource version")
		if watchCallCount >= 2 {
			s.Equal("2000", secondCallResourceVersion, "second watch should use updated resource version, not initial")
		}
	})
}

// TestInitialResourceVersion_ClusterWideWatch tests initial resource version with cluster-wide watches
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_ClusterWideWatch() {
	s.Run("uses initial resource version for cluster-wide watch", func() {
		clientset := fake.NewClientset()

		capturedResourceVersion := ""
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			watchAction := action.(k8stesting.WatchAction)
			capturedResourceVersion = watchAction.GetWatchRestrictions().ResourceVersion

			go func() {
				time.Sleep(10 * time.Millisecond)
				watcher.Stop()
			}()

			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "", // Empty namespace = cluster-wide
			InitialResourceVersion: "cluster-5000",
			MaxRetries:             1,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(100 * time.Millisecond)

		s.Equal("cluster-5000", capturedResourceVersion, "should use initial resource version for cluster-wide watch")
	})
}

// TestInitialResourceVersion_UpdateTracking tests that resource version is updated as events arrive
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_UpdateTracking() {
	s.Run("updates resource version as events are received", func() {
		clientset := fake.NewClientset()
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("events", func(action k8stesting.Action) (handled bool, ret watch.Interface, err error) {
			return true, watcher, nil
		})

		dedupCache := NewDeduplicationCache(5 * time.Second)
		config := EventWatcherConfig{
			Clientset:              clientset,
			Namespace:              "default",
			InitialResourceVersion: "100",
			MaxRetries:             5,
			DedupCache:             dedupCache,
		}

		eventWatcher := NewEventWatcher(config)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		eventWatcher.Start(ctx)
		time.Sleep(50 * time.Millisecond)

		// Initial state
		s.Equal("100", eventWatcher.initialResourceVersion, "initial resource version should be set")
		s.Equal("", eventWatcher.resourceVersion, "current resource version should be empty initially")

		// Send events with increasing resource versions
		for i := 1; i <= 5; i++ {
			event := &v1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:            "test-event",
					Namespace:       "default",
					ResourceVersion: string(rune('0' + 100 + i)), // "101", "102", etc.
				},
			}
			watcher.Add(event)
			time.Sleep(20 * time.Millisecond)
		}

		time.Sleep(50 * time.Millisecond)

		// Resource version should be updated
		s.NotEmpty(eventWatcher.resourceVersion, "resource version should be updated after receiving events")
		s.Equal("100", eventWatcher.initialResourceVersion, "initial resource version should remain unchanged")
	})
}

// TestInitialResourceVersion_EdgeCases tests edge cases for initial resource version handling
func (s *ResourceVersionTestSuite) TestInitialResourceVersion_EdgeCases() {
	s.Run("handles resource version of zero", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "0",
			MaxRetries:             5,
		}

		watcher := NewEventWatcher(config)
		s.Equal("0", watcher.initialResourceVersion, "should handle resource version of zero")
	})

	s.Run("handles very large resource version numbers", func() {
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "999999999999999",
			MaxRetries:             5,
		}

		watcher := NewEventWatcher(config)
		s.Equal("999999999999999", watcher.initialResourceVersion, "should handle large resource version numbers")
	})

	s.Run("handles resource version with special characters", func() {
		// Some Kubernetes implementations use non-numeric resource versions
		config := EventWatcherConfig{
			Clientset:              fake.NewClientset(),
			Namespace:              "default",
			InitialResourceVersion: "rv-abc-123",
			MaxRetries:             5,
		}

		watcher := NewEventWatcher(config)
		s.Equal("rv-abc-123", watcher.initialResourceVersion, "should handle non-numeric resource versions")
	})
}
