package detectors

import (
	"testing"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// CrashLoopDetectorSuite contains tests for CrashLoopDetector
type CrashLoopDetectorSuite struct {
	suite.Suite
	detector *CrashLoopDetector
}

func TestCrashLoopDetectorSuite(t *testing.T) {
	suite.Run(t, new(CrashLoopDetectorSuite))
}

// SetupTest runs before each test
func (s *CrashLoopDetectorSuite) SetupTest() {
	s.detector = NewCrashLoopDetector()
}

// TestCrashLoopDetector_TransitionIntoCrashLoopBackOff tests detection of transition into CrashLoopBackOff
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_TransitionIntoCrashLoopBackOff() {
	s.Run("transition from Running to CrashLoopBackOff emits signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 2, nil)
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason:  "CrashLoopBackOff",
			Message: "back-off 5m0s restarting failed container",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1, "expected one fault signal for CrashLoopBackOff transition")
		signal := signals[0]

		s.Equal(events.FaultTypeCrashLoop, signal.FaultType)
		s.Equal(types.UID(newPod.UID), signal.ResourceUID)
		s.Equal("Pod", signal.Kind)
		s.Equal("test-pod", signal.Name)
		s.Equal("default", signal.Namespace)
		s.Equal("app-container", signal.ContainerName)
		s.Equal(events.SeverityCritical, signal.Severity)
		s.Contains(signal.Context, "CrashLoopBackOff")
		s.Contains(signal.Context, "restart count: 3")
		s.False(signal.Timestamp.IsZero())
	})

	s.Run("transition from Terminated to CrashLoopBackOff emits signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, nil)
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil
		oldPod.Status.ContainerStatuses[0].State.Terminated = &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
		}

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 2, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeCrashLoop, signals[0].FaultType)
		s.Equal(events.SeverityCritical, signals[0].Severity)
	})

	s.Run("transition from other Waiting reason to CrashLoopBackOff emits signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, &corev1.ContainerStateWaiting{
			Reason: "ContainerCreating",
		})

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeCrashLoop, signals[0].FaultType)
	})
}

// TestCrashLoopDetector_NoTransitionNoSignal tests that no signal is emitted when already in CrashLoopBackOff
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_NoTransitionNoSignal() {
	s.Run("already in CrashLoopBackOff does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 2, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when already in CrashLoopBackOff")
	})

	s.Run("staying in CrashLoopBackOff with same restart count does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 5, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 5, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when state is unchanged")
	})
}

// TestCrashLoopDetector_TransitionOutOfCrashLoopBackOff tests transition out of CrashLoopBackOff
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_TransitionOutOfCrashLoopBackOff() {
	s.Run("transition out of CrashLoopBackOff to Running does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, nil)
		newPod.Status.ContainerStatuses[0].State.Waiting = nil
		newPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when transitioning out of CrashLoopBackOff")
	})

	s.Run("transition out of CrashLoopBackOff to another Waiting reason does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason: "ImagePullBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when transitioning to different Waiting reason")
	})
}

// TestCrashLoopDetector_MultipleContainers tests detection with multiple containers
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_MultipleContainers() {
	s.Run("detects CrashLoopBackOff in one of multiple containers", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}

		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1, "expected signal only for container in CrashLoopBackOff")
		s.Equal("app-container", signals[0].ContainerName)
	})

	s.Run("detects CrashLoopBackOff in multiple containers", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}

		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 2, "expected signals for both containers in CrashLoopBackOff")

		containerNames := []string{signals[0].ContainerName, signals[1].ContainerName}
		s.Contains(containerNames, "app-container")
		s.Contains(containerNames, "sidecar-container")
	})

	s.Run("detects transition in one container while other already in CrashLoopBackOff", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 0,
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 2,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
				},
			},
		}

		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name:         "app-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 3,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1, "expected signal only for container transitioning into CrashLoopBackOff")
		s.Equal("app-container", signals[0].ContainerName)
	})
}

// TestCrashLoopDetector_EdgeCases tests edge cases and error conditions
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_EdgeCases() {
	s.Run("returns empty slice for nil newObj", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)

		signals := s.detector.Detect(oldPod, nil)

		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("returns empty slice for nil oldObj", func() {
		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(nil, newPod)

		s.Empty(signals, "nil oldObj means Add event, no transition to detect")
	})

	s.Run("returns empty slice when newObj is not a Pod", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		notAPod := "this is a string, not a Pod"

		signals := s.detector.Detect(oldPod, notAPod)

		s.Empty(signals)
	})

	s.Run("returns empty slice when oldObj is not a Pod", func() {
		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})
		notAPod := "this is a string, not a Pod"

		signals := s.detector.Detect(notAPod, newPod)

		s.Empty(signals)
	})

	s.Run("handles pod with no container statuses", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{},
			},
		}
		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals)
	})

	s.Run("handles new container that did not exist in oldPod", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app-container", RestartCount: 0},
				},
			},
		}
		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app-container", RestartCount: 0},
					{
						Name:         "new-sidecar",
						RestartCount: 1,
						State: corev1.ContainerState{
							Waiting: &corev1.ContainerStateWaiting{
								Reason: "CrashLoopBackOff",
							},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal for new container that did not exist before")
	})

	s.Run("handles container removed from newPod", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app-container", RestartCount: 0},
					{Name: "sidecar-container", RestartCount: 0},
				},
			},
		}
		newPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app-container", RestartCount: 0},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not crash when container is removed")
	})
}

// TestCrashLoopDetector_ContextBuilding tests context message construction
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_ContextBuilding() {
	s.Run("context includes restart count and waiting message", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 5, &corev1.ContainerStateWaiting{
			Reason:  "CrashLoopBackOff",
			Message: "back-off 10m0s restarting failed container",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "CrashLoopBackOff")
		s.Contains(context, "restart count: 5")
		s.Contains(context, "waiting message: back-off 10m0s restarting failed container")
	})

	s.Run("context includes last termination info when available", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 2, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 3, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})
		newPod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 137,
				Reason:   "OOMKilled",
				Message:  "Container exceeded memory limit",
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "restart count: 3")
		s.Contains(context, "last exit code: 137")
		s.Contains(context, "last reason: OOMKilled")
		s.Contains(context, "termination message: Container exceeded memory limit")
	})

	s.Run("context handles minimal information gracefully", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "CrashLoopBackOff")
		s.Contains(context, "restart count: 1")
	})

	s.Run("context does not include last exit code when zero", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 1, &corev1.ContainerStateWaiting{
			Reason: "CrashLoopBackOff",
		})
		newPod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				ExitCode: 0,
				Reason:   "Completed",
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.NotContains(context, "last exit code: 0", "should not include exit code 0")
		s.Contains(context, "last reason: Completed")
	})
}

// TestCrashLoopDetector_DetectorInterface verifies interface compliance
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_DetectorInterface() {
	s.Run("CrashLoopDetector implements Detector interface", func() {
		var _ events.Detector = &CrashLoopDetector{}
		var _ events.Detector = s.detector
	})
}

// TestCrashLoopDetector_NonCrashLoopWaitingStates tests that other Waiting states don't trigger
func (s *CrashLoopDetectorSuite) TestCrashLoopDetector_NonCrashLoopWaitingStates() {
	s.Run("ImagePullBackOff does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, &corev1.ContainerStateWaiting{
			Reason: "ImagePullBackOff",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "ImagePullBackOff should not emit CrashLoop signal")
	})

	s.Run("ErrImagePull does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)
		oldPod.Status.ContainerStatuses[0].State.Running = &corev1.ContainerStateRunning{}
		oldPod.Status.ContainerStatuses[0].State.Waiting = nil

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, &corev1.ContainerStateWaiting{
			Reason: "ErrImagePull",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "ErrImagePull should not emit CrashLoop signal")
	})

	s.Run("ContainerCreating does not emit signal", func() {
		oldPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, nil)

		newPod := createPodWithWaitingState("test-pod", "default", "app-container", 0, &corev1.ContainerStateWaiting{
			Reason: "ContainerCreating",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "ContainerCreating should not emit CrashLoop signal")
	})
}

// Helper function to create a pod with a single container in Waiting state
func createPodWithWaitingState(name, namespace, containerName string, restartCount int32, waiting *corev1.ContainerStateWaiting) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "test-uid-123",
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         containerName,
					RestartCount: restartCount,
				},
			},
		},
	}

	if waiting != nil {
		pod.Status.ContainerStatuses[0].State.Waiting = waiting
	}

	return pod
}
