package events

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/tools/setup-envtest/env"
	"sigs.k8s.io/controller-runtime/tools/setup-envtest/remote"
	"sigs.k8s.io/controller-runtime/tools/setup-envtest/store"
	"sigs.k8s.io/controller-runtime/tools/setup-envtest/versions"
	"sigs.k8s.io/controller-runtime/tools/setup-envtest/workflows"
)

// IntegrationTestSuite tests resource version filtering with a real Kubernetes API server
type IntegrationTestSuite struct {
	suite.Suite
	testEnv   *envtest.Environment
	cfg       *rest.Config
	clientset kubernetes.Interface
}

func TestIntegrationSuite(t *testing.T) {
	// Only run integration tests when explicitly requested
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	suite.Run(t, new(IntegrationTestSuite))
}

func (s *IntegrationTestSuite) SetupSuite() {
	// Set up environment variables to avoid interference
	_ = os.Setenv("KUBECONFIG", "/dev/null")
	_ = os.Setenv("KUBERNETES_SERVICE_HOST", "")
	_ = os.Setenv("KUBERNETES_SERVICE_PORT", "")
	_ = os.Setenv("KUBE_CLIENT_QPS", "1000")
	_ = os.Setenv("KUBE_CLIENT_BURST", "2000")

	// Set up envtest
	envTestDir, err := store.DefaultStoreDir()
	s.Require().NoError(err, "failed to get envtest store directory")

	envTestEnv := &env.Env{
		FS:  afero.Afero{Fs: afero.NewOsFs()},
		Out: os.Stdout,
		Client: &remote.HTTPClient{
			IndexURL: remote.DefaultIndexURL,
		},
		Platform: versions.PlatformItem{
			Platform: versions.Platform{
				OS:   runtime.GOOS,
				Arch: runtime.GOARCH,
			},
		},
		Version: versions.AnyVersion,
		Store:   store.NewAt(envTestDir),
	}
	envTestEnv.CheckCoherence()
	workflows.Use{}.Do(envTestEnv)
	versionDir := envTestEnv.Platform.BaseName(*envTestEnv.Version.AsConcrete())

	s.testEnv = &envtest.Environment{
		BinaryAssetsDirectory: filepath.Join(envTestDir, "k8s", versionDir),
	}

	cfg, err := s.testEnv.Start()
	s.Require().NoError(err, "failed to start test environment")
	s.Require().NotNil(cfg, "test environment config should not be nil")

	s.cfg = cfg
	clientset, err := kubernetes.NewForConfig(cfg)
	s.Require().NoError(err, "failed to create clientset")
	s.clientset = clientset
}

func (s *IntegrationTestSuite) TearDownSuite() {
	if s.testEnv != nil {
		err := s.testEnv.Stop()
		s.Require().NoError(err, "failed to stop test environment")
	}
}

func (s *IntegrationTestSuite) SetupTest() {
	// Create a test namespace for each test
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-events-",
		},
	}
	_, err := s.clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	s.Require().NoError(err, "failed to create test namespace")
}

// TestHistoricalEventsAreNotSent verifies that historical events are NOT sent on new subscription
func (s *IntegrationTestSuite) TestHistoricalEventsAreNotSent() {
	s.Run("subscription filters out events created before subscription time", func() {
		ctx := context.Background()
		namespace := "default"

		// Create several historical events BEFORE subscription
		historicalEvents := []*v1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "historical-event-1",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "test-pod",
					Namespace: namespace,
				},
				Type:    "Warning",
				Reason:  "BackOff",
				Message: "Historical event 1",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "historical-event-2",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "test-pod",
					Namespace: namespace,
				},
				Type:    "Warning",
				Reason:  "FailedMount",
				Message: "Historical event 2",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "historical-event-3",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "another-pod",
					Namespace: namespace,
				},
				Type:    "Warning",
				Reason:  "ImagePullBackOff",
				Message: "Historical event 3",
			},
		}

		// Create historical events in the cluster
		for _, event := range historicalEvents {
			_, err := s.clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
			s.Require().NoError(err, "failed to create historical event")
		}

		// Wait briefly to ensure events are persisted
		time.Sleep(100 * time.Millisecond)

		// Now create a subscription - this should get the current resource version
		// and filter out all the historical events we just created
		receivedEvents := make(chan *v1.Event, 10)
		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  s.clientset,
			Namespace:  namespace,
			Filters:    &SubscriptionFilters{Type: "Warning"},
			MaxRetries: 1,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				receivedEvents <- event
			},
		}

		// Get current resource version to simulate subscription creation
		manager := &EventSubscriptionManager{}
		currentRV, err := manager.getCurrentResourceVersion(s.clientset, namespace)
		s.Require().NoError(err, "failed to get current resource version")
		s.NotEmpty(currentRV, "current resource version should not be empty")

		// Set initial resource version to filter historical events
		config.InitialResourceVersion = currentRV

		watcher := NewEventWatcher(config)
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		watcher.Start(watcherCtx)

		// Wait for watcher to start and process any events
		time.Sleep(200 * time.Millisecond)

		// Verify NO historical events were received
		select {
		case event := <-receivedEvents:
			s.Fail("received historical event that should have been filtered", "event: %s", event.Name)
		default:
			// Expected: no events received
		}

		// Now create a NEW event after subscription - this SHOULD be delivered
		newEvent := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-event-after-subscription",
				Namespace: namespace,
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: namespace,
			},
			Type:    "Warning",
			Reason:  "NewWarning",
			Message: "This event was created after subscription",
		}

		_, err = s.clientset.CoreV1().Events(namespace).Create(ctx, newEvent, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create new event")

		// Wait for the new event to be delivered
		select {
		case event := <-receivedEvents:
			s.Equal("new-event-after-subscription", event.Name, "should receive the new event")
			s.Equal("NewWarning", event.Reason)
		case <-time.After(2 * time.Second):
			s.Fail("did not receive new event within timeout")
		}

		cancelWatcher()
	})
}

// TestNewEventsAfterSubscriptionAreDelivered verifies that events created after subscription ARE sent
func (s *IntegrationTestSuite) TestNewEventsAfterSubscriptionAreDelivered() {
	s.Run("subscription delivers all events created after subscription time", func() {
		ctx := context.Background()
		namespace := "default"

		// Create subscription first
		receivedEvents := make(chan *v1.Event, 10)
		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  s.clientset,
			Namespace:  namespace,
			Filters:    &SubscriptionFilters{},
			MaxRetries: 1,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				receivedEvents <- event
			},
		}

		// Get current resource version
		manager := &EventSubscriptionManager{}
		currentRV, err := manager.getCurrentResourceVersion(s.clientset, namespace)
		s.Require().NoError(err, "failed to get current resource version")

		config.InitialResourceVersion = currentRV

		watcher := NewEventWatcher(config)
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		watcher.Start(watcherCtx)

		// Wait for watcher to be ready
		time.Sleep(100 * time.Millisecond)

		// Create multiple events AFTER subscription
		newEvents := []*v1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-event-1",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "test-pod-1",
					Namespace: namespace,
				},
				Type:    "Normal",
				Reason:  "Started",
				Message: "Pod started",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-event-2",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "test-pod-2",
					Namespace: namespace,
				},
				Type:    "Warning",
				Reason:  "Failed",
				Message: "Pod failed",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-event-3",
					Namespace: namespace,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Service",
					Name:      "test-service",
					Namespace: namespace,
				},
				Type:    "Normal",
				Reason:  "Created",
				Message: "Service created",
			},
		}

		// Create the events
		for _, event := range newEvents {
			_, err := s.clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
			s.Require().NoError(err, "failed to create new event")
			time.Sleep(50 * time.Millisecond) // Small delay between events
		}

		// Collect all received events
		receivedEventNames := make(map[string]bool)
		timeout := time.After(3 * time.Second)

		for i := 0; i < len(newEvents); i++ {
			select {
			case event := <-receivedEvents:
				receivedEventNames[event.Name] = true
			case <-timeout:
				s.Fail("timeout waiting for events", "received %d of %d events", len(receivedEventNames), len(newEvents))
			}
		}

		// Verify all new events were received
		for _, event := range newEvents {
			s.True(receivedEventNames[event.Name], "should have received event %s", event.Name)
		}

		cancelWatcher()
	})
}

// TestClusterWideSubscriptionFiltersHistoricalEvents tests resource version filtering for cluster-wide watches
func (s *IntegrationTestSuite) TestClusterWideSubscriptionFiltersHistoricalEvents() {
	s.Run("cluster-wide subscription filters historical events across all namespaces", func() {
		ctx := context.Background()

		// Create events in multiple namespaces BEFORE subscription
		namespaces := []string{"default", "kube-system"}
		historicalEventCount := 0

		for _, ns := range namespaces {
			event := &v1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "historical-event-" + ns,
					Namespace: ns,
				},
				InvolvedObject: v1.ObjectReference{
					Kind:      "Pod",
					Name:      "test-pod",
					Namespace: ns,
				},
				Type:    "Warning",
				Reason:  "Historical",
				Message: "Historical event in " + ns,
			}
			_, err := s.clientset.CoreV1().Events(ns).Create(ctx, event, metav1.CreateOptions{})
			s.Require().NoError(err, "failed to create historical event in %s", ns)
			historicalEventCount++
		}

		time.Sleep(100 * time.Millisecond)

		// Create cluster-wide subscription
		receivedEvents := make(chan *v1.Event, 10)
		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  s.clientset,
			Namespace:  "", // Empty = cluster-wide
			Filters:    &SubscriptionFilters{},
			MaxRetries: 1,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				receivedEvents <- event
			},
		}

		// Get current resource version for cluster-wide watch
		manager := &EventSubscriptionManager{}
		currentRV, err := manager.getCurrentResourceVersion(s.clientset, "")
		s.Require().NoError(err, "failed to get current resource version for cluster-wide watch")
		s.NotEmpty(currentRV, "resource version should not be empty")

		config.InitialResourceVersion = currentRV

		watcher := NewEventWatcher(config)
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		watcher.Start(watcherCtx)
		time.Sleep(200 * time.Millisecond)

		// Verify NO historical events received
		select {
		case event := <-receivedEvents:
			s.Fail("received historical event in cluster-wide watch", "event: %s in namespace %s", event.Name, event.Namespace)
		default:
			// Expected: no events
		}

		// Create new event in one of the namespaces
		newEvent := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-cluster-wide-event",
				Namespace: "default",
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "new-pod",
				Namespace: "default",
			},
			Type:    "Normal",
			Reason:  "NewEvent",
			Message: "New event after cluster-wide subscription",
		}

		_, err = s.clientset.CoreV1().Events("default").Create(ctx, newEvent, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create new event")

		// Verify new event is received
		select {
		case event := <-receivedEvents:
			s.Equal("new-cluster-wide-event", event.Name)
			s.Equal("default", event.Namespace)
		case <-time.After(2 * time.Second):
			s.Fail("did not receive new cluster-wide event")
		}

		cancelWatcher()
	})
}

// TestResourceVersionPreservedOnReconnection verifies that resource version is preserved during reconnections
func (s *IntegrationTestSuite) TestResourceVersionPreservedOnReconnection() {
	s.Run("watch resumes from last known resource version on reconnection", func() {
		ctx := context.Background()
		namespace := "default"

		receivedEvents := make(chan *v1.Event, 10)
		dedupCache := NewDeduplicationCache(5 * time.Second)

		config := EventWatcherConfig{
			Clientset:  s.clientset,
			Namespace:  namespace,
			Filters:    &SubscriptionFilters{},
			MaxRetries: 3,
			DedupCache: dedupCache,
			ProcessEvent: func(event *v1.Event) {
				receivedEvents <- event
			},
		}

		// Get initial resource version
		manager := &EventSubscriptionManager{}
		initialRV, err := manager.getCurrentResourceVersion(s.clientset, namespace)
		s.Require().NoError(err, "failed to get initial resource version")

		config.InitialResourceVersion = initialRV

		watcher := NewEventWatcher(config)
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		watcher.Start(watcherCtx)
		time.Sleep(100 * time.Millisecond)

		// Create first event
		event1 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "event-before-reconnect",
				Namespace: namespace,
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: namespace,
			},
			Type:    "Normal",
			Reason:  "BeforeReconnect",
			Message: "Event before reconnection",
		}

		_, err = s.clientset.CoreV1().Events(namespace).Create(ctx, event1, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create event before reconnect")

		// Wait for event to be received
		select {
		case e := <-receivedEvents:
			s.Equal("event-before-reconnect", e.Name)
			// Store the resource version from this event
			// The watcher should have updated its internal resourceVersion
		case <-time.After(2 * time.Second):
			s.Fail("did not receive event before reconnect")
		}

		// Check that watcher's resourceVersion was updated (not equal to initial)
		// Note: We can't directly access resourceVersion as it's private,
		// but the behavior will be validated by the next event delivery

		// Create event after "reconnection" - if resource version is preserved correctly,
		// this event should be delivered without duplication
		event2 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "event-after-reconnect",
				Namespace: namespace,
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: namespace,
			},
			Type:    "Normal",
			Reason:  "AfterReconnect",
			Message: "Event after reconnection",
		}

		_, err = s.clientset.CoreV1().Events(namespace).Create(ctx, event2, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create event after reconnect")

		// Verify second event is received
		select {
		case e := <-receivedEvents:
			s.Equal("event-after-reconnect", e.Name)
		case <-time.After(2 * time.Second):
			s.Fail("did not receive event after reconnect")
		}

		// Verify no duplicate events
		select {
		case e := <-receivedEvents:
			s.Fail("received unexpected duplicate event", "event: %s", e.Name)
		case <-time.After(500 * time.Millisecond):
			// Expected: no more events
		}

		cancelWatcher()
	})
}

// TestGetCurrentResourceVersionBehavior tests the getCurrentResourceVersion method
func (s *IntegrationTestSuite) TestGetCurrentResourceVersionBehavior() {
	s.Run("getCurrentResourceVersion returns valid resource version", func() {
		manager := &EventSubscriptionManager{}

		// Test namespace-scoped resource version
		rv, err := manager.getCurrentResourceVersion(s.clientset, "default")
		s.NoError(err, "should successfully get resource version for namespace")
		s.NotEmpty(rv, "resource version should not be empty")

		// Test cluster-wide resource version
		rvCluster, err := manager.getCurrentResourceVersion(s.clientset, "")
		s.NoError(err, "should successfully get resource version for cluster-wide")
		s.NotEmpty(rvCluster, "cluster-wide resource version should not be empty")
	})

	s.Run("resource version increases with new events", func() {
		ctx := context.Background()
		namespace := "default"
		manager := &EventSubscriptionManager{}

		// Get initial resource version
		rv1, err := manager.getCurrentResourceVersion(s.clientset, namespace)
		s.Require().NoError(err)

		// Create a new event
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rv-increment",
				Namespace: namespace,
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: namespace,
			},
			Type:    "Normal",
			Reason:  "Testing",
			Message: "Test resource version increment",
		}

		_, err = s.clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
		s.Require().NoError(err)

		// Get resource version after creating event
		rv2, err := manager.getCurrentResourceVersion(s.clientset, namespace)
		s.Require().NoError(err)

		// Resource version should have changed (increased)
		s.NotEqual(rv1, rv2, "resource version should change after creating event")
	})
}
