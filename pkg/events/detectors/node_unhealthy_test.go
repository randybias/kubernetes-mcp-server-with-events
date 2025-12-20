package detectors

import (
	"testing"

	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// NodeUnhealthyDetectorSuite contains tests for NodeUnhealthyDetector
type NodeUnhealthyDetectorSuite struct {
	suite.Suite
	detector *NodeUnhealthyDetector
}

func TestNodeUnhealthyDetectorSuite(t *testing.T) {
	suite.Run(t, new(NodeUnhealthyDetectorSuite))
}

// SetupTest runs before each test
func (s *NodeUnhealthyDetectorSuite) SetupTest() {
	s.detector = NewNodeUnhealthyDetector()
}

// TestNodeUnhealthyDetector_ReadyTransitions tests detection of Ready condition transitions
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_ReadyTransitions() {
	s.Run("Ready True to False emits critical signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is posting ready status")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet stopped posting ready status")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		signal := signals[0]

		s.Equal(events.FaultTypeNodeUnhealthy, signal.FaultType)
		s.Equal(types.UID(newNode.UID), signal.ResourceUID)
		s.Equal("Node", signal.Kind)
		s.Equal("node-1", signal.Name)
		s.Equal("", signal.Namespace)
		s.Equal(events.SeverityCritical, signal.Severity)
		s.Contains(signal.Context, "Ready=False")
		s.Contains(signal.Context, "reason: KubeletNotReady")
		s.Contains(signal.Context, "message: kubelet stopped posting ready status")
		s.False(signal.Timestamp.IsZero())
	})

	s.Run("Ready True to Unknown emits warning signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is posting ready status")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "Kubelet stopped posting node status")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		signal := signals[0]

		s.Equal(events.FaultTypeNodeUnhealthy, signal.FaultType)
		s.Equal(events.SeverityWarning, signal.Severity)
		s.Contains(signal.Context, "Ready=Unknown")
		s.Contains(signal.Context, "reason: NodeStatusUnknown")
	})

	s.Run("Ready False to True does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "recovery from unhealthy to healthy should not emit signal")
	})

	s.Run("Ready Unknown to True does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status unknown")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "recovery from unknown to healthy should not emit signal")
	})

	s.Run("Ready True to True does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "no state change should not emit signal")
	})

	s.Run("Ready False to False does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is still not ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "no state change should not emit signal")
	})

	s.Run("Ready Unknown to Unknown does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status unknown")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status still unknown")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "no state change should not emit signal")
	})

	s.Run("Ready False to Unknown does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status unknown")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "transition between unhealthy states should not emit signal")
	})

	s.Run("Ready Unknown to False does not emit signal", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status unknown")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "transition between unhealthy states should not emit signal")
	})
}

// TestNodeUnhealthyDetector_MultipleConditions tests nodes with multiple conditions
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_MultipleConditions() {
	s.Run("detects Ready transition among multiple conditions", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:    corev1.NodeMemoryPressure,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletHasSufficientMemory",
						Message: "kubelet has sufficient memory available",
					},
					{
						Type:    corev1.NodeReady,
						Status:  corev1.ConditionTrue,
						Reason:  "KubeletReady",
						Message: "kubelet is posting ready status",
					},
					{
						Type:    corev1.NodeDiskPressure,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletHasNoDiskPressure",
						Message: "kubelet has no disk pressure",
					},
				},
			},
		}

		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:    corev1.NodeMemoryPressure,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletHasSufficientMemory",
						Message: "kubelet has sufficient memory available",
					},
					{
						Type:    corev1.NodeReady,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletNotReady",
						Message: "container runtime is down",
					},
					{
						Type:    corev1.NodeDiskPressure,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletHasNoDiskPressure",
						Message: "kubelet has no disk pressure",
					},
				},
			},
		}

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		s.Equal(events.FaultTypeNodeUnhealthy, signals[0].FaultType)
		s.Contains(signals[0].Context, "container runtime is down")
	})

	s.Run("ignores other condition changes when Ready unchanged", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:    corev1.NodeMemoryPressure,
						Status:  corev1.ConditionFalse,
						Reason:  "KubeletHasSufficientMemory",
						Message: "kubelet has sufficient memory available",
					},
					{
						Type:    corev1.NodeReady,
						Status:  corev1.ConditionTrue,
						Reason:  "KubeletReady",
						Message: "kubelet is posting ready status",
					},
				},
			},
		}

		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:    corev1.NodeMemoryPressure,
						Status:  corev1.ConditionTrue,
						Reason:  "KubeletHasInsufficientMemory",
						Message: "kubelet has memory pressure",
					},
					{
						Type:    corev1.NodeReady,
						Status:  corev1.ConditionTrue,
						Reason:  "KubeletReady",
						Message: "kubelet is posting ready status",
					},
				},
			},
		}

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "changes to non-Ready conditions should not emit signal")
	})
}

// TestNodeUnhealthyDetector_EdgeCases tests edge cases and error conditions
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_EdgeCases() {
	s.Run("returns empty slice for nil newObj", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")

		signals := s.detector.Detect(oldNode, nil)

		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("returns empty slice for nil oldObj", func() {
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")

		signals := s.detector.Detect(nil, newNode)

		s.Empty(signals, "nil oldObj means Add event, no transition to detect")
	})

	s.Run("returns empty slice when newObj is not a Node", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		notANode := "this is a string, not a Node"

		signals := s.detector.Detect(oldNode, notANode)

		s.Empty(signals)
	})

	s.Run("returns empty slice when oldObj is not a Node", func() {
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")
		notANode := "this is a string, not a Node"

		signals := s.detector.Detect(notANode, newNode)

		s.Empty(signals)
	})

	s.Run("handles node with no conditions", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{},
			},
		}
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{},
			},
		}

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "no conditions means no Ready condition to detect")
	})

	s.Run("handles node with missing Ready condition in oldNode", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:   corev1.NodeMemoryPressure,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "missing Ready condition in oldNode means no transition to detect")
	})

	s.Run("handles node with missing Ready condition in newNode", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:   corev1.NodeMemoryPressure,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "missing Ready condition in newNode means no transition to detect")
	})

	s.Run("handles node with nil conditions", func() {
		oldNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: nil,
			},
		}
		newNode := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: nil,
			},
		}

		signals := s.detector.Detect(oldNode, newNode)

		s.Empty(signals, "nil conditions should be handled gracefully")
	})
}

// TestNodeUnhealthyDetector_ContextBuilding tests context message construction
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_ContextBuilding() {
	s.Run("context includes status, reason, and message", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "container runtime network not ready: NetworkReady=false reason:NetworkPluginNotReady message:docker: network plugin is not ready: cni config uninitialized")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Ready=False")
		s.Contains(context, "reason: KubeletNotReady")
		s.Contains(context, "message: container runtime network not ready")
	})

	s.Run("context includes only status when reason and message are empty", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "", "")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Ready=False")
		s.NotContains(context, "reason:")
		s.NotContains(context, "message:")
	})

	s.Run("context includes reason but not message when message is empty", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		context := signals[0].Context

		s.Contains(context, "Ready=Unknown")
		s.Contains(context, "reason: NodeStatusUnknown")
		s.NotContains(context, "message:")
	})
}

// TestNodeUnhealthyDetector_SeverityLevels tests severity determination
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_SeverityLevels() {
	s.Run("Ready False results in critical severity", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionFalse, "KubeletNotReady", "kubelet is not ready")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityCritical, signals[0].Severity)
	})

	s.Run("Ready Unknown results in warning severity", func() {
		oldNode := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")
		newNode := createNodeWithReadyCondition("node-1", corev1.ConditionUnknown, "NodeStatusUnknown", "status unknown")

		signals := s.detector.Detect(oldNode, newNode)

		s.Require().Len(signals, 1)
		s.Equal(events.SeverityWarning, signals[0].Severity)
	})
}

// TestNodeUnhealthyDetector_DetectorInterface verifies interface compliance
func (s *NodeUnhealthyDetectorSuite) TestNodeUnhealthyDetector_DetectorInterface() {
	s.Run("NodeUnhealthyDetector implements Detector interface", func() {
		var _ events.Detector = &NodeUnhealthyDetector{}
		var _ events.Detector = s.detector
	})
}

// TestGetReadyCondition tests the getReadyCondition helper function
func (s *NodeUnhealthyDetectorSuite) TestGetReadyCondition() {
	s.Run("finds Ready condition", func() {
		node := createNodeWithReadyCondition("node-1", corev1.ConditionTrue, "KubeletReady", "kubelet is ready")

		condition := getReadyCondition(node)

		s.Require().NotNil(condition)
		s.Equal(corev1.NodeReady, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
		s.Equal("KubeletReady", condition.Reason)
		s.Equal("kubelet is ready", condition.Message)
	})

	s.Run("returns nil when Ready condition not found", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:   corev1.NodeMemoryPressure,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		condition := getReadyCondition(node)

		s.Nil(condition)
	})

	s.Run("returns nil when node is nil", func() {
		condition := getReadyCondition(nil)

		s.Nil(condition)
	})

	s.Run("returns nil when node has no conditions", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{},
			},
		}

		condition := getReadyCondition(node)

		s.Nil(condition)
	})

	s.Run("returns nil when node has nil conditions", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: nil,
			},
		}

		condition := getReadyCondition(node)

		s.Nil(condition)
	})

	s.Run("finds Ready condition among multiple conditions", func() {
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-1",
				UID:  "node-uid-123",
			},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{
						Type:   corev1.NodeMemoryPressure,
						Status: corev1.ConditionFalse,
					},
					{
						Type:   corev1.NodeDiskPressure,
						Status: corev1.ConditionFalse,
					},
					{
						Type:    corev1.NodeReady,
						Status:  corev1.ConditionTrue,
						Reason:  "KubeletReady",
						Message: "kubelet is ready",
					},
					{
						Type:   corev1.NodePIDPressure,
						Status: corev1.ConditionFalse,
					},
				},
			},
		}

		condition := getReadyCondition(node)

		s.Require().NotNil(condition)
		s.Equal(corev1.NodeReady, condition.Type)
		s.Equal(corev1.ConditionTrue, condition.Status)
	})
}

// Helper function to create a node with a Ready condition
func createNodeWithReadyCondition(name string, status corev1.ConditionStatus, reason, message string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "node-uid-123",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:    corev1.NodeReady,
					Status:  status,
					Reason:  reason,
					Message: message,
				},
			},
		},
	}
}
