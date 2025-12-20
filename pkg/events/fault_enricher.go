package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// FaultContextEnricher enriches fault signals with additional context.
// It decides whether to fetch container logs based on:
// 1. Whether the signal already has context (e.g., termination message)
// 2. The severity level of the fault
//
// Logs are only fetched when:
// - FaultSignal.Context is empty (no termination message available)
// - Severity is SeverityCritical (e.g., CrashLoopBackOff)
type FaultContextEnricher struct {
	maxContainers        int
	maxBytesPerContainer int
}

// NewFaultContextEnricher creates a new FaultContextEnricher with default limits.
func NewFaultContextEnricher() *FaultContextEnricher {
	return &FaultContextEnricher{
		maxContainers:        DefaultMaxContainersPerNotification,
		maxBytesPerContainer: DefaultMaxLogBytesPerContainer,
	}
}

// NewFaultContextEnricherWithLimits creates a new FaultContextEnricher with custom limits.
func NewFaultContextEnricherWithLimits(maxContainers, maxBytesPerContainer int) *FaultContextEnricher {
	return &FaultContextEnricher{
		maxContainers:        maxContainers,
		maxBytesPerContainer: maxBytesPerContainer,
	}
}

// Enrich enriches a fault signal with additional context by fetching logs if needed.
// It modifies the signal's Context field in place.
//
// Logs are only fetched when:
// 1. The signal's Context is empty (no termination message)
// 2. The signal's Severity is SeverityCritical
//
// Returns an error if log fetching fails, but this is not a critical error
// since the signal already contains basic fault information.
func (e *FaultContextEnricher) Enrich(ctx context.Context, signal *FaultSignal, clientset kubernetes.Interface) error {
	if signal == nil {
		return fmt.Errorf("signal cannot be nil")
	}

	// Skip log fetch if context already exists (has termination message)
	if signal.Context != "" {
		return nil
	}

	// Skip log fetch if severity is not critical
	if signal.Severity != SeverityCritical {
		return nil
	}

	// Only fetch logs for pod-related faults
	if signal.Kind != "Pod" {
		return nil
	}

	// Skip if namespace is empty (shouldn't happen for pods, but defensive check)
	if signal.Namespace == "" {
		return fmt.Errorf("pod fault signal missing namespace")
	}

	// Skip if pod name is empty
	if signal.Name == "" {
		return fmt.Errorf("pod fault signal missing name")
	}

	// Fetch pod logs
	logs, err := e.fetchPodLogs(ctx, clientset, signal.Namespace, signal.Name)
	if err != nil {
		// Log fetch failure is not critical - signal already has basic info
		return fmt.Errorf("failed to fetch logs: %w", err)
	}

	// Serialize logs to JSON and add to context
	if len(logs) > 0 {
		logsJSON, err := json.Marshal(logs)
		if err != nil {
			return fmt.Errorf("failed to serialize logs: %w", err)
		}
		signal.Context = string(logsJSON)
	}

	return nil
}

// fetchPodLogs fetches logs from a pod's containers using kubernetes.Interface.
func (e *FaultContextEnricher) fetchPodLogs(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, podName string,
) ([]ContainerLog, error) {
	// Get pod to discover containers
	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Extract container list from pod spec
	var containerNames []string
	for _, container := range pod.Spec.Containers {
		containerNames = append(containerNames, container.Name)
	}

	// Limit to maxContainers
	if len(containerNames) > e.maxContainers {
		containerNames = containerNames[:e.maxContainers]
	}

	var logs []ContainerLog

	// Fetch logs for each container (current and previous)
	for _, containerName := range containerNames {
		// Try to get current logs
		currentLog, currentErr := e.getPodLogs(ctx, clientset, namespace, podName, containerName, false)
		if currentErr == nil {
			truncated := truncateLog(currentLog, e.maxBytesPerContainer)
			logs = append(logs, ContainerLog{
				Container: containerName,
				Previous:  false,
				HasPanic:  detectPanic(truncated),
				Sample:    truncated,
			})
		} else {
			// Include error information if log fetch failed (e.g., RBAC denied)
			logs = append(logs, ContainerLog{
				Container: containerName,
				Previous:  false,
				HasPanic:  false,
				Sample:    "",
				Error:     currentErr.Error(),
			})
		}

		// Try to get previous logs (if container has restarted)
		previousLog, previousErr := e.getPodLogs(ctx, clientset, namespace, podName, containerName, true)
		if previousErr == nil && previousLog != "" {
			truncated := truncateLog(previousLog, e.maxBytesPerContainer)
			logs = append(logs, ContainerLog{
				Container: containerName,
				Previous:  true,
				HasPanic:  detectPanic(truncated),
				Sample:    truncated,
			})
		}
		// Don't add error for previous logs if they don't exist (common case)
	}

	return logs, nil
}

// getPodLogs retrieves logs from a specific container in a pod using kubernetes.Interface.
func (e *FaultContextEnricher) getPodLogs(
	ctx context.Context,
	clientset kubernetes.Interface,
	namespace, podName, containerName string,
	previous bool,
) (string, error) {
	opts := &v1.PodLogOptions{
		Container: containerName,
		Previous:  previous,
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			// Log the close error, but don't override the main error
			err = fmt.Errorf("failed to close log stream: %w (original error: %v)", closeErr, err)
		}
	}()

	bytes, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}
