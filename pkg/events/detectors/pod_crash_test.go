package detectors

import (
	"testing"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// PodCrashDetectorSuite contains tests for PodCrashDetector
type PodCrashDetectorSuite struct {
	suite.Suite
	detector *PodCrashDetector
}

func TestPodCrashDetectorSuite(t *testing.T) {
	suite.Run(t, new(PodCrashDetectorSuite))
}

// SetupTest runs before each test
func (s *PodCrashDetectorSuite) SetupTest() {
	s.detector = NewPodCrashDetector()
}

// TestPodCrashDetector_RestartCountIncrease tests crash detection when RestartCount increases
func (s *PodCrashDetectorSuite) TestPodCrashDetector_RestartCountIncrease() {
	s.Run("RestartCount 0->1 with non-zero exit code emits signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
			Message:  "Container failed",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1, "expected one fault signal for crash")
		signal := signals[0]

		s.Equal(events.FaultTypePodCrash, signal.FaultType)
		s.Equal(types.UID(newPod.UID), signal.ResourceUID)
		s.Equal("Pod", signal.Kind)
		s.Equal("test-pod", signal.Name)
		s.Equal("default", signal.Namespace)
		s.Equal("app-container", signal.ContainerName)
		s.Equal(events.SeverityWarning, signal.Severity)
		s.Contains(signal.Context, "exit code 1")
		s.Contains(signal.Context, "reason: Error")
		s.Contains(signal.Context, "message: Container failed")
		s.False(signal.Timestamp.IsZero())
	})

	s.Run("RestartCount 0->1 with exit code 0 does not emit signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 0,
			Reason:   "Completed",
			Message:  "Container completed successfully",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "graceful restart with exit code 0 should not emit signal")
	})

	s.Run("RestartCount 2->3 with non-zero exit code emits signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 2, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 3, &corev1.ContainerStateTerminated{
			ExitCode: 137,
			Reason:   "OOMKilled",
			Message:  "Out of memory",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		signal := signals[0]

		s.Equal(events.FaultTypePodCrash, signal.FaultType)
		s.Contains(signal.Context, "exit code 137")
		s.Contains(signal.Context, "reason: OOMKilled")
	})
}

// TestPodCrashDetector_RestartCountUnchanged tests no signal when RestartCount is unchanged
func (s *PodCrashDetectorSuite) TestPodCrashDetector_RestartCountUnchanged() {
	s.Run("RestartCount unchanged does not emit signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, nil)

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "unchanged RestartCount should not emit signal")
	})

	s.Run("RestartCount unchanged with Terminated state does not emit signal", func() {
		terminated := &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
		}
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, terminated)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, terminated)

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "unchanged RestartCount should not emit signal even with Terminated state")
	})
}

// TestPodCrashDetector_MultipleContainers tests detection with multiple containers
func (s *PodCrashDetectorSuite) TestPodCrashDetector_MultipleContainers() {
	s.Run("detects crash in one of multiple containers", func() {
		oldPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
				UID:       "test-uid-123",
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app-container", RestartCount: 0},
					{Name: "sidecar-container", RestartCount: 1},
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
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					},
					{Name: "sidecar-container", RestartCount: 1}, // Unchanged
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1, "expected signal only for crashed container")
		s.Equal("app-container", signals[0].ContainerName)
	})

	s.Run("detects crashes in multiple containers", func() {
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
					{
						Name:         "app-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Reason:   "Error",
							},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 2,
								Reason:   "Failed",
							},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 2, "expected signals for both crashed containers")

		containerNames := []string{signals[0].ContainerName, signals[1].ContainerName}
		s.Contains(containerNames, "app-container")
		s.Contains(containerNames, "sidecar-container")
	})
}

// TestPodCrashDetector_ContainerStates tests detection with various container states
func (s *PodCrashDetectorSuite) TestPodCrashDetector_ContainerStates() {
	s.Run("RestartCount increased but container Running does not emit signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
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
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when container is Running")
	})

	s.Run("RestartCount increased but container Waiting does not emit signal", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
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
				},
			},
		}

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when container is Waiting")
	})
}

// TestPodCrashDetector_EdgeCases tests edge cases and error conditions
func (s *PodCrashDetectorSuite) TestPodCrashDetector_EdgeCases() {
	s.Run("returns empty slice for nil newObj", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)

		signals := s.detector.Detect(oldPod, nil)

		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("returns empty slice for nil oldObj", func() {
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 1,
		})

		signals := s.detector.Detect(nil, newPod)

		s.Empty(signals, "nil oldObj means Add event, no crash to detect")
	})

	s.Run("returns empty slice when newObj is not a Pod", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		notAPod := "this is a string, not a Pod"

		signals := s.detector.Detect(oldPod, notAPod)

		s.Empty(signals)
	})

	s.Run("returns empty slice when oldObj is not a Pod", func() {
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, nil)
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
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
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

	s.Run("handles RestartCount decrease", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 5, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 3, &corev1.ContainerStateTerminated{
			ExitCode: 1,
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Empty(signals, "should not emit signal when RestartCount decreases")
	})
}

// TestPodCrashDetector_ContextBuilding tests context message construction
func (s *PodCrashDetectorSuite) TestPodCrashDetector_ContextBuilding() {
	s.Run("context includes exit code, reason, and message", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 137,
			Reason:   "OOMKilled",
			Message:  "Container exceeded memory limits",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "exit code 137")
		s.Contains(context, "reason: OOMKilled")
		s.Contains(context, "message: Container exceeded memory limits")
	})

	s.Run("context includes only exit code when reason and message are empty", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "",
			Message:  "",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "exit code 1")
		s.NotContains(context, "reason:")
		s.NotContains(context, "message:")
	})

	s.Run("context includes reason but not message when message is empty", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 143,
			Reason:   "ContainerCannotRun",
			Message:  "",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "exit code 143")
		s.Contains(context, "reason: ContainerCannotRun")
		s.NotContains(context, "message:")
	})
}

// TestPodCrashDetector_DetectorInterface verifies interface compliance
func (s *PodCrashDetectorSuite) TestPodCrashDetector_DetectorInterface() {
	s.Run("PodCrashDetector implements Detector interface", func() {
		var _ events.Detector = &PodCrashDetector{}
		var _ events.Detector = s.detector
	})
}

// TestExtractTerminationMessage tests the extractTerminationMessage helper function
func (s *PodCrashDetectorSuite) TestExtractTerminationMessage() {
	s.Run("extracts termination message from container", func() {
		pod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
			Message:  "Connection refused",
		})

		message := extractTerminationMessage(pod, "app-container")
		s.Equal("Connection refused", message)
	})

	s.Run("returns empty string when container has no termination state", func() {
		pod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)

		message := extractTerminationMessage(pod, "app-container")
		s.Equal("", message)
	})

	s.Run("returns empty string when container is not found", func() {
		pod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)

		message := extractTerminationMessage(pod, "nonexistent-container")
		s.Equal("", message)
	})

	s.Run("returns empty string when pod is nil", func() {
		message := extractTerminationMessage(nil, "app-container")
		s.Equal("", message)
	})

	s.Run("extracts multiline termination message", func() {
		multilineMessage := "panic: runtime error\nstack trace line 1\nstack trace line 2"
		pod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 2,
			Reason:   "Error",
			Message:  multilineMessage,
		})

		message := extractTerminationMessage(pod, "app-container")
		s.Equal(multilineMessage, message)
	})

	s.Run("extracts message from specific container in multi-container pod", func() {
		pod := &corev1.Pod{
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
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
								Message:  "App error",
							},
						},
					},
					{
						Name:         "sidecar-container",
						RestartCount: 1,
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 2,
								Message:  "Sidecar error",
							},
						},
					},
				},
			},
		}

		message := extractTerminationMessage(pod, "sidecar-container")
		s.Equal("Sidecar error", message)
	})
}

// TestTerminationMessage verifies that termination messages are extracted and included in fault signals
func (s *PodCrashDetectorSuite) TestTerminationMessage() {
	s.Run("termination message with panic trace appears in context", func() {
		panicMessage := "panic: runtime error: invalid memory address or nil pointer dereference\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:42 +0x1f"
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 2,
			Reason:   "Error",
			Message:  panicMessage,
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Contains(signals[0].Context, panicMessage, "panic trace should be included in context")
		s.Contains(signals[0].Context, "exit code 2")
		s.Contains(signals[0].Context, "reason: Error")
	})

	s.Run("termination message with error details appears in context", func() {
		errorMessage := "Failed to connect to database: connection timeout after 30s"
		oldPod := createPodWithContainerStatus("test-pod", "default", "db-container", 2, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "db-container", 3, &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
			Message:  errorMessage,
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Contains(signals[0].Context, errorMessage, "error message should be included in context")
	})

	s.Run("empty termination message is handled gracefully", func() {
		oldPod := createPodWithContainerStatus("test-pod", "default", "app-container", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "app-container", 1, &corev1.ContainerStateTerminated{
			ExitCode: 1,
			Reason:   "Error",
			Message:  "",
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Contains(signals[0].Context, "exit code 1")
		s.NotContains(signals[0].Context, "message:", "empty message should not add 'message:' to context")
	})

	s.Run("multiline termination message with OOMKilled", func() {
		oomMessage := "Container was OOMKilled\nMemory limit: 512Mi\nPeak usage: 723Mi\nLast allocation: 180Mi"
		oldPod := createPodWithContainerStatus("test-pod", "default", "memory-hog", 0, nil)
		newPod := createPodWithContainerStatus("test-pod", "default", "memory-hog", 1, &corev1.ContainerStateTerminated{
			ExitCode: 137,
			Reason:   "OOMKilled",
			Message:  oomMessage,
		})

		signals := s.detector.Detect(oldPod, newPod)

		s.Require().Len(signals, 1)
		s.Contains(signals[0].Context, "exit code 137")
		s.Contains(signals[0].Context, "reason: OOMKilled")
		s.Contains(signals[0].Context, oomMessage, "multiline OOM message should be preserved in context")
	})
}

// Helper function to create a pod with a single container status
func createPodWithContainerStatus(name, namespace, containerName string, restartCount int32, terminated *corev1.ContainerStateTerminated) *corev1.Pod {
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

	if terminated != nil {
		pod.Status.ContainerStatuses[0].State.Terminated = terminated
	}

	return pod
}
