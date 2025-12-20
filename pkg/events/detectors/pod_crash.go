package detectors

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// PodCrashDetector detects container crashes in pods by comparing RestartCount
// between old and new pod states. A crash is detected when:
// 1. RestartCount increases for a container
// 2. The container is in Terminated state with a non-zero exit code
type PodCrashDetector struct{}

// NewPodCrashDetector creates a new PodCrashDetector instance.
func NewPodCrashDetector() *PodCrashDetector {
	return &PodCrashDetector{}
}

// Detect analyzes pod state changes and returns fault signals for detected crashes.
// It compares container statuses between oldObj and newObj, looking for RestartCount
// increases combined with Terminated state and non-zero exit codes.
func (d *PodCrashDetector) Detect(oldObj, newObj interface{}) []events.FaultSignal {
	// Handle nil newObj - nothing to detect
	if newObj == nil {
		return []events.FaultSignal{}
	}

	// Type assert to Pod
	newPod, ok := newObj.(*corev1.Pod)
	if !ok {
		return []events.FaultSignal{}
	}

	// If oldObj is nil (Add event), no crash to detect
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
		oldStatus, exists := oldStatusMap[newStatus.Name]

		// Skip if container didn't exist before
		if !exists {
			continue
		}

		// Check if RestartCount increased
		if newStatus.RestartCount <= oldStatus.RestartCount {
			continue
		}

		// Check if the container is in Terminated state with non-zero exit code
		if newStatus.State.Terminated == nil {
			continue
		}

		terminated := newStatus.State.Terminated
		if terminated.ExitCode == 0 {
			// Exit code 0 means graceful termination, not a crash
			continue
		}

		// We have a crash: RestartCount increased and container terminated with error
		context := buildCrashContext(terminated)

		signal := events.FaultSignal{
			FaultType:     events.FaultTypePodCrash,
			ResourceUID:   types.UID(newPod.UID),
			Kind:          "Pod",
			Name:          newPod.Name,
			Namespace:     newPod.Namespace,
			ContainerName: newStatus.Name,
			Severity:      events.SeverityWarning,
			Context:       context,
			Timestamp:     time.Now(),
		}

		signals = append(signals, signal)
	}

	return signals
}

// extractTerminationMessage retrieves the termination message from a pod's container status.
// Returns an empty string if the container is not found or has no termination message.
func extractTerminationMessage(pod *corev1.Pod, containerName string) string {
	if pod == nil {
		return ""
	}

	for _, status := range pod.Status.ContainerStatuses {
		if status.Name == containerName {
			if status.State.Terminated != nil {
				return status.State.Terminated.Message
			}
			return ""
		}
	}

	return ""
}

// buildCrashContext creates a human-readable context string from container termination info.
func buildCrashContext(terminated *corev1.ContainerStateTerminated) string {
	context := fmt.Sprintf("Container crashed with exit code %d", terminated.ExitCode)

	if terminated.Reason != "" {
		context += fmt.Sprintf(", reason: %s", terminated.Reason)
	}

	if terminated.Message != "" {
		context += fmt.Sprintf(", message: %s", terminated.Message)
	}

	return context
}
