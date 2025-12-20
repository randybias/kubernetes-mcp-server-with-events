package detectors

import (
	"testing"

	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// DeploymentFailureDetectorSuite contains tests for DeploymentFailureDetector
type DeploymentFailureDetectorSuite struct {
	suite.Suite
	detector *DeploymentFailureDetector
}

func TestDeploymentFailureDetectorSuite(t *testing.T) {
	suite.Run(t, new(DeploymentFailureDetectorSuite))
}

// SetupTest runs before each test
func (s *DeploymentFailureDetectorSuite) SetupTest() {
	s.detector = NewDeploymentFailureDetector()
}

// TestDeploymentFailureDetector_ProgressDeadlineExceeded tests detection of ProgressDeadlineExceeded
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_ProgressDeadlineExceeded() {
	s.Run("transition to ProgressDeadlineExceeded emits critical signal", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet test-deployment-abc has successfully progressed")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "ReplicaSet test-deployment-abc has timed out progressing")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		signal := signals[0]

		s.Equal(events.FaultTypeDeploymentFailure, signal.FaultType)
		s.Equal(types.UID(newDeployment.UID), signal.ResourceUID)
		s.Equal("Deployment", signal.Kind)
		s.Equal("test-deployment", signal.Name)
		s.Equal("default", signal.Namespace)
		s.Equal(events.SeverityCritical, signal.Severity)
		s.Contains(signal.Context, "ProgressDeadlineExceeded")
		s.Contains(signal.Context, "message: ReplicaSet test-deployment-abc has timed out progressing")
		s.False(signal.Timestamp.IsZero())
	})

	s.Run("transition from no Progressing condition to ProgressDeadlineExceeded emits signal", func() {
		oldDeployment := createDeploymentWithoutProgressingCondition("test-deployment", "default")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment exceeded its progress deadline")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeDeploymentFailure, signals[0].FaultType)
		s.Contains(signals[0].Context, "ProgressDeadlineExceeded")
	})

	s.Run("no transition when already in ProgressDeadlineExceeded state", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "no state change should not emit signal")
	})

	s.Run("no signal when Progressing condition is True", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "healthy deployment should not emit signal")
	})

	s.Run("no signal when Progressing condition is False but not ProgressDeadlineExceeded", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "SomeOtherReason", "Some other message")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "non-ProgressDeadlineExceeded failure should not emit signal")
	})

	s.Run("recovery from ProgressDeadlineExceeded does not emit signal", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "recovery from failure should not emit signal")
	})
}

// TestDeploymentFailureDetector_MultipleConditions tests Deployments with multiple conditions
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_MultipleConditions() {
	s.Run("detects ProgressDeadlineExceeded among multiple conditions", func() {
		oldDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionTrue,
						Reason:  "MinimumReplicasAvailable",
						Message: "Deployment has minimum availability",
					},
					{
						Type:    appsv1.DeploymentProgressing,
						Status:  corev1.ConditionTrue,
						Reason:  "NewReplicaSetAvailable",
						Message: "ReplicaSet is progressing",
					},
				},
			},
		}

		newDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionTrue,
						Reason:  "MinimumReplicasAvailable",
						Message: "Deployment has minimum availability",
					},
					{
						Type:    appsv1.DeploymentProgressing,
						Status:  corev1.ConditionFalse,
						Reason:  "ProgressDeadlineExceeded",
						Message: "ReplicaSet test-deployment-abc has timed out progressing",
					},
				},
			},
		}

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeDeploymentFailure, signals[0].FaultType)
		s.Contains(signals[0].Context, "timed out progressing")
	})

	s.Run("ignores other condition changes when Progressing unchanged", func() {
		oldDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionTrue,
						Reason:  "MinimumReplicasAvailable",
						Message: "Deployment has minimum availability",
					},
					{
						Type:    appsv1.DeploymentProgressing,
						Status:  corev1.ConditionTrue,
						Reason:  "NewReplicaSetAvailable",
						Message: "ReplicaSet is progressing",
					},
				},
			},
		}

		newDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:    appsv1.DeploymentAvailable,
						Status:  corev1.ConditionFalse,
						Reason:  "MinimumReplicasUnavailable",
						Message: "Deployment does not have minimum availability",
					},
					{
						Type:    appsv1.DeploymentProgressing,
						Status:  corev1.ConditionTrue,
						Reason:  "NewReplicaSetAvailable",
						Message: "ReplicaSet is progressing",
					},
				},
			},
		}

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "changes to non-Progressing conditions should not emit signal")
	})
}

// TestDeploymentFailureDetector_EdgeCases tests edge cases and error conditions
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_EdgeCases() {
	s.Run("returns empty slice for nil newObj", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")

		signals := s.detector.Detect(oldDeployment, nil)

		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("returns empty slice for nil oldObj", func() {
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")

		signals := s.detector.Detect(nil, newDeployment)

		s.Empty(signals, "nil oldObj means Add event, no transition to detect")
	})

	s.Run("returns empty slice when newObj is not a Deployment", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		notADeployment := "this is a string, not a Deployment"

		signals := s.detector.Detect(oldDeployment, notADeployment)

		s.Empty(signals)
	})

	s.Run("returns empty slice when oldObj is not a Deployment", func() {
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")
		notADeployment := "this is a string, not a Deployment"

		signals := s.detector.Detect(notADeployment, newDeployment)

		s.Empty(signals)
	})

	s.Run("handles Deployment with no conditions", func() {
		oldDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{},
			},
		}
		newDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{},
			},
		}

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "no conditions means no Progressing condition to detect")
	})

	s.Run("handles Deployment with missing Progressing condition in newObj", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithoutProgressingCondition("test-deployment", "default")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "missing Progressing condition in newObj means no failure to detect")
	})

	s.Run("handles Deployment with nil conditions", func() {
		oldDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: nil,
			},
		}
		newDeployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: nil,
			},
		}

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "nil conditions should be handled gracefully")
	})
}

// TestDeploymentFailureDetector_ContextBuilding tests context message construction
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_ContextBuilding() {
	s.Run("context includes ProgressDeadlineExceeded and message", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "ReplicaSet test-deployment-abc has timed out progressing: pod test-deployment-abc-xyz has unready container nginx")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "ProgressDeadlineExceeded")
		s.Contains(context, "message: ReplicaSet test-deployment-abc has timed out progressing")
	})

	s.Run("context does not duplicate ProgressDeadlineExceeded reason", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "ProgressDeadlineExceeded")
		s.NotContains(context, "reason: ProgressDeadlineExceeded")
	})

	s.Run("no signal when Progressing is False but reason is not ProgressDeadlineExceeded", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "", "Deployment has timed out")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Empty(signals, "Progressing=False without ProgressDeadlineExceeded reason should not emit signal")
	})

	s.Run("context is minimal when message is empty", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "ProgressDeadlineExceeded")
		s.NotContains(context, "message:")
	})
}

// TestDeploymentFailureDetector_SeverityLevels tests severity determination
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_SeverityLevels() {
	s.Run("ProgressDeadlineExceeded results in critical severity", func() {
		oldDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")
		newDeployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionFalse, "ProgressDeadlineExceeded", "Deployment has timed out")

		signals := s.detector.Detect(oldDeployment, newDeployment)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityCritical, signals[0].Severity)
	})
}

// TestDeploymentFailureDetector_DetectorInterface verifies interface compliance
func (s *DeploymentFailureDetectorSuite) TestDeploymentFailureDetector_DetectorInterface() {
	s.Run("DeploymentFailureDetector implements Detector interface", func() {
		var _ events.Detector = &DeploymentFailureDetector{}
		var _ events.Detector = s.detector
	})
}

// TestGetProgressingCondition tests the getProgressingCondition helper function
func (s *DeploymentFailureDetectorSuite) TestGetProgressingCondition() {
	s.Run("finds Progressing condition", func() {
		deployment := createDeploymentWithProgressingCondition("test-deployment", "default", corev1.ConditionTrue, "NewReplicaSetAvailable", "ReplicaSet is available")

		condition := getProgressingCondition(deployment)

		s.Require().NotNil(condition)
		s.Equal(appsv1.DeploymentProgressing, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
		s.Equal("NewReplicaSetAvailable", condition.Reason)
		s.Equal("ReplicaSet is available", condition.Message)
	})

	s.Run("returns nil when Progressing condition not found", func() {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}

		condition := getProgressingCondition(deployment)

		s.Nil(condition)
	})

	s.Run("returns nil when Deployment is nil", func() {
		condition := getProgressingCondition(nil)

		s.Nil(condition)
	})

	s.Run("returns nil when Deployment has no conditions", func() {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{},
			},
		}

		condition := getProgressingCondition(deployment)

		s.Nil(condition)
	})

	s.Run("returns nil when Deployment has nil conditions", func() {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: nil,
			},
		}

		condition := getProgressingCondition(deployment)

		s.Nil(condition)
	})

	s.Run("finds Progressing condition among multiple conditions", func() {
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-deployment",
				Namespace: "default",
				UID:       "deployment-uid-123",
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
					{
						Type:   appsv1.DeploymentReplicaFailure,
						Status: corev1.ConditionFalse,
					},
					{
						Type:    appsv1.DeploymentProgressing,
						Status:  corev1.ConditionTrue,
						Reason:  "NewReplicaSetAvailable",
						Message: "ReplicaSet is available",
					},
				},
			},
		}

		condition := getProgressingCondition(deployment)

		s.Require().NotNil(condition)
		s.Equal(appsv1.DeploymentProgressing, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
	})
}

// Helper function to create a Deployment with a Progressing condition
func createDeploymentWithProgressingCondition(name, namespace string, status corev1.ConditionStatus, reason, message string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "deployment-uid-123",
		},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:    appsv1.DeploymentProgressing,
					Status:  status,
					Reason:  reason,
					Message: message,
				},
			},
		},
	}
}

// Helper function to create a Deployment without a Progressing condition
func createDeploymentWithoutProgressingCondition(name, namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       "deployment-uid-123",
		},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}
