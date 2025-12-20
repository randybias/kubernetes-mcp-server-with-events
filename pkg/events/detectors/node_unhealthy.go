package detectors

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// NodeUnhealthyDetector detects when a node transitions to an unhealthy state.
// A node is considered unhealthy when its Ready condition transitions from
// Status="True" to Status="False" or Status="Unknown".
type NodeUnhealthyDetector struct{}

// NewNodeUnhealthyDetector creates a new NodeUnhealthyDetector instance.
func NewNodeUnhealthyDetector() *NodeUnhealthyDetector {
	return &NodeUnhealthyDetector{}
}

// Detect analyzes node state changes and returns fault signals for nodes
// transitioning to unhealthy states. It detects transitions in the Ready
// condition from True to False or Unknown.
func (d *NodeUnhealthyDetector) Detect(oldObj, newObj interface{}) []events.FaultSignal {
	// Handle nil newObj - nothing to detect
	if newObj == nil {
		return []events.FaultSignal{}
	}

	// Type assert to Node
	newNode, ok := newObj.(*corev1.Node)
	if !ok {
		return []events.FaultSignal{}
	}

	// If oldObj is nil (Add event), no transition to detect
	if oldObj == nil {
		return []events.FaultSignal{}
	}

	oldNode, ok := oldObj.(*corev1.Node)
	if !ok {
		return []events.FaultSignal{}
	}

	// Get Ready conditions from both nodes
	oldReady := getReadyCondition(oldNode)
	newReady := getReadyCondition(newNode)

	// If either Ready condition is missing, no transition to detect
	if oldReady == nil || newReady == nil {
		return []events.FaultSignal{}
	}

	// Detect transition from Ready=True to Ready=False or Ready=Unknown
	if oldReady.Status == corev1.ConditionTrue &&
		(newReady.Status == corev1.ConditionFalse || newReady.Status == corev1.ConditionUnknown) {

		context := buildNodeUnhealthyContext(newReady)
		severity := determineNodeSeverity(newReady.Status)

		signal := events.FaultSignal{
			FaultType:   events.FaultTypeNodeUnhealthy,
			ResourceUID: types.UID(newNode.UID),
			Kind:        "Node",
			Name:        newNode.Name,
			Namespace:   "", // Nodes are cluster-scoped
			Severity:    severity,
			Context:     context,
			Timestamp:   time.Now(),
		}

		return []events.FaultSignal{signal}
	}

	return []events.FaultSignal{}
}

// getReadyCondition finds the Ready condition in a node's status.
// Returns nil if the Ready condition is not found.
func getReadyCondition(node *corev1.Node) *corev1.NodeCondition {
	if node == nil {
		return nil
	}

	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeReady {
			return &node.Status.Conditions[i]
		}
	}

	return nil
}

// buildNodeUnhealthyContext creates a human-readable context string from
// the node's Ready condition.
func buildNodeUnhealthyContext(readyCondition *corev1.NodeCondition) string {
	context := fmt.Sprintf("Node became unhealthy: Ready=%s", readyCondition.Status)

	if readyCondition.Reason != "" {
		context += fmt.Sprintf(", reason: %s", readyCondition.Reason)
	}

	if readyCondition.Message != "" {
		context += fmt.Sprintf(", message: %s", readyCondition.Message)
	}

	return context
}

// determineNodeSeverity determines the severity level based on the Ready
// condition status. False is critical (node definitively unhealthy),
// Unknown is warning (node status uncertain).
func determineNodeSeverity(status corev1.ConditionStatus) events.Severity {
	if status == corev1.ConditionFalse {
		return events.SeverityCritical
	}
	// ConditionUnknown
	return events.SeverityWarning
}
