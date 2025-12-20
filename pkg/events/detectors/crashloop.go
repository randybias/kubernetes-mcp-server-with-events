package detectors

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// CrashLoopDetector detects when a pod enters CrashLoopBackOff state.
// This is an edge-triggered detector that emits signals when a container
// transitions INTO CrashLoopBackOff state (detected via Waiting.Reason).
// CrashLoopBackOff indicates that Kubernetes is backing off on restarting
// a container due to repeated crashes.
type CrashLoopDetector struct{}

// NewCrashLoopDetector creates a new CrashLoopDetector instance.
func NewCrashLoopDetector() *CrashLoopDetector {
	return &CrashLoopDetector{}
}

// Detect analyzes pod state changes and returns fault signals for containers
// entering CrashLoopBackOff state. It compares container statuses between
// oldObj and newObj, looking for transitions from non-CrashLoopBackOff to
// CrashLoopBackOff state.
func (d *CrashLoopDetector) Detect(oldObj, newObj interface{}) []events.FaultSignal {
	// Handle nil newObj - nothing to detect
	if newObj == nil {
		return []events.FaultSignal{}
	}

	// Type assert to Pod
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return []events.FaultSignal{}
	}

	// If oldObj is nil (Add event), check if already in CrashLoopBackOff
	// but don't emit signal (this is edge-triggered, not level-triggered)
	if oldObj == nil {
		return []events.FaultSignal{}
	}

	oldPod, ok := oldObj.(*corev1.Pod)
	if !ok {
		return []events.FaultSignal{}
	}

	var signals []events.FaultSignal

	// Create a map of old container statuses by container name for easy lookup
	oldStatusMap := make(map[string]corev1.ContainerStatus)
	for _, status := range oldPod.Status.ContainerStatuses {
		oldStatusMap[status.Name] = status
	}

	// Check each container in the new pod
	for _, newStatus := range newPod.Status.ContainerStatuses {
		// Check if the container is currently in CrashLoopBackOff
		if !isInCrashLoopBackOff(newStatus) {
			continue
		}

		oldStatus, exists := oldStatusMap[newStatus.Name]

		// If container didn't exist before, skip (edge-triggered: no transition)
		if !exists {
			continue
		}

		// Check if old container was NOT in CrashLoopBackOff (transition detected)
		if isInCrashLoopBackOff(oldStatus) {
			// Already in CrashLoopBackOff, no transition
			continue
		}

		// We have a transition into CrashLoopBackOff state
		context := buildCrashLoopContext(newStatus, newPod)

		signal := events.FaultSignal{
			FaultType:     events.FaultTypeCrashLoop,
			ResourceUID:   types.UID(newPod.UID),
			Kind:          "Pod",
			Name:          newPod.Name,
			Namespace:     newPod.Namespace,
			ContainerName: newStatus.Name,
			Severity:      events.SeverityCritical,
			Context:       context,
			Timestamp:     time.Now(),
		}

		signals = append(signals, signal)
	}

	return signals
}

// isInCrashLoopBackOff checks if a container status indicates CrashLoopBackOff state.
func isInCrashLoopBackOff(status corev1.ContainerStatus) bool {
	if status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff" {
		return true
	}
	return false
}

// buildCrashLoopContext creates a human-readable context string for CrashLoopBackOff.
func buildCrashLoopContext(status corev1.ContainerStatus, pod *corev1.Pod) string {
	context := fmt.Sprintf("Container entered CrashLoopBackOff state, restart count: %d", status.RestartCount)

	if status.State.Waiting != nil && status.State.Waiting.Message != "" {
		context += fmt.Sprintf(", waiting message: %s", status.State.Waiting.Message)
	}

	// Try to get termination message from last terminated state
	if status.LastTerminationState.Terminated != nil {
		terminated := status.LastTerminationState.Terminated
		if terminated.ExitCode != 0 {
			context += fmt.Sprintf(", last exit code: %d", terminated.ExitCode)
		}
		if terminated.Reason != "" {
			context += fmt.Sprintf(", last reason: %s", terminated.Reason)
		}
		if terminated.Message != "" {
			context += fmt.Sprintf(", termination message: %s", terminated.Message)
		}
	}

	return context
}
