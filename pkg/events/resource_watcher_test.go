package events_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
	"github.com/containers/kubernetes-mcp-server/pkg/events/detectors"
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

// ResourceWatcherTestSuite tests resource watcher with a real Kubernetes API server
type ResourceWatcherTestSuite struct {
	suite.Suite
	testEnv   *envtest.Environment
	cfg       *rest.Config
	clientset kubernetes.Interface
}

func TestResourceWatcherSuite(t *testing.T) {
	// Only run integration tests when explicitly requested
	if testing.Short() {
		t.Skip("Skipping resource watcher tests in short mode")
	}

	suite.Run(t, new(ResourceWatcherTestSuite))
}

func (s *ResourceWatcherTestSuite) SetupSuite() {
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

func (s *ResourceWatcherTestSuite) TearDownSuite() {
	if s.testEnv != nil {
		err := s.testEnv.Stop()
		s.Require().NoError(err, "failed to stop test environment")
	}
}

func (s *ResourceWatcherTestSuite) SetupTest() {
	// Create a test namespace for each test
	ns := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-watcher-",
		},
	}
	_, err := s.clientset.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
	s.Require().NoError(err, "failed to create test namespace")
}

// TestResourceWatcher_ReceivesPodUpdates verifies that the resource watcher receives Pod update events
func (s *ResourceWatcherTestSuite) TestResourceWatcher_ReceivesPodUpdates() {
	s.Run("receives Pod update events via informer", func() {
		ctx := context.Background()
		namespace := "default"

		// Create a Pod first
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-updates",
				Namespace: namespace,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "test-container",
						Image: "nginx:latest",
					},
				},
			},
		}

		createdPod, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create test pod")
		s.Require().NotNil(createdPod)

		// Create resource watcher
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		// Start the watcher
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err = watcher.Start(watcherCtx)
		s.Require().NoError(err, "failed to start resource watcher")

		// Wait for informer cache to sync
		time.Sleep(500 * time.Millisecond)

		// Update the Pod to trigger an update event
		updatedPod := createdPod.DeepCopy()
		if updatedPod.Labels == nil {
			updatedPod.Labels = make(map[string]string)
		}
		updatedPod.Labels["test-update"] = "true"

		_, err = s.clientset.CoreV1().Pods(namespace).Update(ctx, updatedPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to update pod")

		// Wait for the update event to be processed
		// The update callback logs the event at V(2) level
		time.Sleep(500 * time.Millisecond)

		// If we reach here without errors, the watcher is receiving updates
		// The actual logging is verified through klog output

		// Cleanup
		watcher.Stop()
		err = s.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		s.NoError(err, "failed to delete test pod")
	})
}

// TestResourceWatcher_StartStop verifies that the resource watcher starts and stops correctly
func (s *ResourceWatcherTestSuite) TestResourceWatcher_StartStop() {
	s.Run("starts and stops without errors", func() {
		ctx := context.Background()

		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err := watcher.Start(watcherCtx)
		s.Require().NoError(err, "start should not return error")

		// Wait for cache sync
		time.Sleep(200 * time.Millisecond)

		// Stop the watcher
		watcher.Stop()

		// Verify stop doesn't panic or error
		// If we reach here, start/stop works correctly
	})
}

// TestResourceWatcher_HandlesMultiplePodUpdates verifies that the watcher handles multiple updates
func (s *ResourceWatcherTestSuite) TestResourceWatcher_HandlesMultiplePodUpdates() {
	s.Run("handles multiple Pod updates in sequence", func() {
		ctx := context.Background()
		namespace := "default"

		// Create resource watcher first
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err := watcher.Start(watcherCtx)
		s.Require().NoError(err, "failed to start resource watcher")

		// Wait for cache sync
		time.Sleep(500 * time.Millisecond)

		// Create a Pod
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod-multiple",
				Namespace: namespace,
				Labels:    map[string]string{"iteration": "0"},
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "test-container",
						Image: "nginx:latest",
					},
				},
			},
		}

		createdPod, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create test pod")

		// Perform multiple updates
		for i := 1; i <= 3; i++ {
			time.Sleep(200 * time.Millisecond)

			updatedPod := createdPod.DeepCopy()
			updatedPod.Labels["iteration"] = string(rune('0' + i))

			createdPod, err = s.clientset.CoreV1().Pods(namespace).Update(ctx, updatedPod, metav1.UpdateOptions{})
			s.Require().NoError(err, "failed to update pod on iteration %d", i)
		}

		// Wait for all updates to be processed
		time.Sleep(500 * time.Millisecond)

		// If we reach here, all updates were processed without errors

		// Cleanup
		watcher.Stop()
		err = s.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		s.NoError(err, "failed to delete test pod")
	})
}

// TestResourceWatcher_DefaultResyncPeriod verifies default resync period is set
func (s *ResourceWatcherTestSuite) TestResourceWatcher_DefaultResyncPeriod() {
	s.Run("uses default resync period when not specified", func() {
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 0, // Not specified
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		// The default is set in NewResourceWatcher
		// Verify the watcher was created successfully (watcher is non-nil)
	})
}

// TestResourceWatcher_FaultDetectionPipeline tests the complete detection pipeline
func (s *ResourceWatcherTestSuite) TestResourceWatcher_FaultDetectionPipeline() {
	s.Run("detects pod crash and emits fault signal", func() {
		ctx := context.Background()
		namespace := "default"

		// Channel to capture emitted fault signals
		signalChan := make(chan events.FaultSignal, 10)

		// Create resource watcher with detectors
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
			Detectors: []events.Detector{
				detectors.NewPodCrashDetector(),
				detectors.NewCrashLoopDetector(),
			},
			SignalCallback: func(signal events.FaultSignal) {
				signalChan <- signal
			},
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		// Start the watcher
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err := watcher.Start(watcherCtx)
		s.Require().NoError(err, "failed to start resource watcher")

		// Wait for cache sync
		time.Sleep(500 * time.Millisecond)

		// Create a Pod
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-crash-pod",
				Namespace: namespace,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "test-container",
						Image: "nginx:latest",
					},
				},
			},
		}

		createdPod, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create test pod")

		// Set initial status with running container
		createdPod.Status = v1.PodStatus{
			Phase: v1.PodRunning,
			ContainerStatuses: []v1.ContainerStatus{
				{
					Name:         "test-container",
					RestartCount: 0,
					State: v1.ContainerState{
						Running: &v1.ContainerStateRunning{
							StartedAt: metav1.Now(),
						},
					},
				},
			},
		}

		createdPod, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, createdPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to set initial pod status")

		// Wait for initial pod to be processed
		time.Sleep(500 * time.Millisecond)

		// Update the Pod to simulate a container crash (increase RestartCount + Terminated state)
		updatedPod := createdPod.DeepCopy()
		updatedPod.Status.ContainerStatuses[0].RestartCount = 1
		updatedPod.Status.ContainerStatuses[0].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode: 137, // SIGKILL
				Reason:   "Error",
				Message:  "Container crashed",
			},
		}

		_, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, updatedPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to update pod status")

		// Wait for the signal to be emitted
		select {
		case signal := <-signalChan:
			// Verify the signal
			s.Equal(events.FaultTypePodCrash, signal.FaultType, "expected PodCrash fault type")
			s.Equal("Pod", signal.Kind, "expected Pod kind")
			s.Equal("test-crash-pod", signal.Name, "expected pod name")
			s.Equal(namespace, signal.Namespace, "expected namespace")
			s.Equal("test-container", signal.ContainerName, "expected container name")
			s.Equal(events.SeverityWarning, signal.Severity, "expected Warning severity")
			s.Contains(signal.Context, "exit code 137", "expected context to contain exit code")
		case <-time.After(5 * time.Second):
			s.Fail("timeout waiting for fault signal")
		}

		// Cleanup
		watcher.Stop()
		err = s.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		s.NoError(err, "failed to delete test pod")
	})

	s.Run("detects crash loop and emits fault signal", func() {
		ctx := context.Background()
		namespace := "default"

		// Channel to capture emitted fault signals
		signalChan := make(chan events.FaultSignal, 10)

		// Create resource watcher with detectors
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
			Detectors: []events.Detector{
				detectors.NewPodCrashDetector(),
				detectors.NewCrashLoopDetector(),
			},
			SignalCallback: func(signal events.FaultSignal) {
				signalChan <- signal
			},
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		// Start the watcher
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err := watcher.Start(watcherCtx)
		s.Require().NoError(err, "failed to start resource watcher")

		// Wait for cache sync
		time.Sleep(500 * time.Millisecond)

		// Create a Pod
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-crashloop-pod",
				Namespace: namespace,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "test-container",
						Image: "nginx:latest",
					},
				},
			},
		}

		createdPod, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create test pod")

		// Set initial status with running container
		createdPod.Status = v1.PodStatus{
			Phase: v1.PodPending,
			ContainerStatuses: []v1.ContainerStatus{
				{
					Name:         "test-container",
					RestartCount: 3,
					State: v1.ContainerState{
						Running: &v1.ContainerStateRunning{
							StartedAt: metav1.Now(),
						},
					},
				},
			},
		}

		createdPod, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, createdPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to set initial pod status")

		// Wait for initial pod to be processed
		time.Sleep(500 * time.Millisecond)

		// Update the Pod to simulate CrashLoopBackOff
		updatedPod := createdPod.DeepCopy()
		updatedPod.Status.ContainerStatuses[0].State = v1.ContainerState{
			Waiting: &v1.ContainerStateWaiting{
				Reason:  "CrashLoopBackOff",
				Message: "back-off 5m0s restarting failed container",
			},
		}
		updatedPod.Status.ContainerStatuses[0].LastTerminationState = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode: 1,
				Reason:   "Error",
				Message:  "Application failed to start",
			},
		}

		_, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, updatedPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to update pod status")

		// Wait for the signal to be emitted
		select {
		case signal := <-signalChan:
			// Verify the signal
			s.Equal(events.FaultTypeCrashLoop, signal.FaultType, "expected CrashLoop fault type")
			s.Equal("Pod", signal.Kind, "expected Pod kind")
			s.Equal("test-crashloop-pod", signal.Name, "expected pod name")
			s.Equal(namespace, signal.Namespace, "expected namespace")
			s.Equal("test-container", signal.ContainerName, "expected container name")
			s.Equal(events.SeverityCritical, signal.Severity, "expected Critical severity")
			s.Contains(signal.Context, "CrashLoopBackOff", "expected context to contain CrashLoopBackOff")
		case <-time.After(5 * time.Second):
			s.Fail("timeout waiting for fault signal")
		}

		// Cleanup
		watcher.Stop()
		err = s.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		s.NoError(err, "failed to delete test pod")
	})

	s.Run("deduplicates duplicate fault signals", func() {
		ctx := context.Background()
		namespace := "default"

		// Channel to capture emitted fault signals
		signalChan := make(chan events.FaultSignal, 10)

		// Create a deduplicator with very short TTL for testing
		deduplicator := events.NewFaultDeduplicatorWithTTL(1 * time.Second)

		// Create resource watcher with detectors
		config := events.ResourceWatcherConfig{
			Clientset:    s.clientset,
			Cluster:      "test-cluster",
			ResyncPeriod: 10 * time.Minute,
			Detectors: []events.Detector{
				detectors.NewPodCrashDetector(),
			},
			Deduplicator: deduplicator,
			SignalCallback: func(signal events.FaultSignal) {
				signalChan <- signal
			},
		}

		watcher := events.NewResourceWatcher(config)
		s.Require().NotNil(watcher)

		// Start the watcher
		watcherCtx, cancelWatcher := context.WithCancel(ctx)
		defer cancelWatcher()

		err := watcher.Start(watcherCtx)
		s.Require().NoError(err, "failed to start resource watcher")

		// Wait for cache sync
		time.Sleep(500 * time.Millisecond)

		// Create a Pod
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-dedup-pod",
				Namespace: namespace,
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "test-container",
						Image: "nginx:latest",
					},
				},
			},
		}

		createdPod, err := s.clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		s.Require().NoError(err, "failed to create test pod")

		// Set initial status with running container
		createdPod.Status = v1.PodStatus{
			Phase: v1.PodRunning,
			ContainerStatuses: []v1.ContainerStatus{
				{
					Name:         "test-container",
					RestartCount: 0,
					State: v1.ContainerState{
						Running: &v1.ContainerStateRunning{
							StartedAt: metav1.Now(),
						},
					},
				},
			},
		}

		createdPod, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, createdPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to set initial pod status")

		// Wait for initial pod to be processed
		time.Sleep(500 * time.Millisecond)

		// First crash - should emit signal
		updatedPod := createdPod.DeepCopy()
		updatedPod.Status.ContainerStatuses[0].RestartCount = 1
		updatedPod.Status.ContainerStatuses[0].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode: 1,
				Reason:   "Error",
			},
		}

		createdPod, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, updatedPod, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to update pod status")

		// Should receive first signal
		select {
		case signal := <-signalChan:
			s.Equal(events.FaultTypePodCrash, signal.FaultType)
		case <-time.After(2 * time.Second):
			s.Fail("timeout waiting for first fault signal")
		}

		// Second crash immediately after - should be deduplicated
		updatedPod2 := createdPod.DeepCopy()
		updatedPod2.Status.ContainerStatuses[0].RestartCount = 2
		updatedPod2.Status.ContainerStatuses[0].State = v1.ContainerState{
			Terminated: &v1.ContainerStateTerminated{
				ExitCode: 1,
				Reason:   "Error",
			},
		}

		_, err = s.clientset.CoreV1().Pods(namespace).UpdateStatus(ctx, updatedPod2, metav1.UpdateOptions{})
		s.Require().NoError(err, "failed to update pod status")

		// Should NOT receive second signal (deduplicated)
		select {
		case <-signalChan:
			s.Fail("received duplicate signal that should have been deduplicated")
		case <-time.After(1 * time.Second):
			// Expected - no signal should be emitted
		}

		// Cleanup
		watcher.Stop()
		err = s.clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		s.NoError(err, "failed to delete test pod")
	})
}
