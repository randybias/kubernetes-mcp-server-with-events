package events

import (
	"context"
	"fmt"
	"io"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/containers/kubernetes-mcp-server/pkg/kubernetes"
)

const (
	// DefaultMaxLogBytesPerContainer is the default maximum bytes to capture per container
	DefaultMaxLogBytesPerContainer = 10240 // 10KB

	// DefaultMaxContainersPerNotification is the default maximum containers to capture logs from
	DefaultMaxContainersPerNotification = 5
)

// ContainerLog represents logs captured from a single container
type ContainerLog struct {
	Container string `json:"container"`
	Previous  bool   `json:"previous"`
	HasPanic  bool   `json:"hasPanic"`
	Sample    string `json:"sample"`
	Error     string `json:"error,omitempty"`
}

// truncateLog truncates log content to the specified byte limit
func truncateLog(log string, maxBytes int) string {
	if len(log) <= maxBytes {
		return log
	}
	return log[:maxBytes]
}

// detectPanic scans log content for panic indicators
// Returns true if any panic/fatal/crash patterns are detected
func detectPanic(log string) bool {
	// Keywords to detect panics and fatal errors (lowercase for case-insensitive matching)
	keywords := []string{
		"panic:",
		"fatal",
		"sigsegv",
		"segfault",
		"goroutine",
	}

	logLower := strings.ToLower(log)
	for _, keyword := range keywords {
		if strings.Contains(logLower, keyword) {
			return true
		}
	}

	return false
}

// capturePodLogs fetches logs from a pod's containers with limits
// Returns a slice of ContainerLog entries (up to maxContainers)
func capturePodLogs(
	ctx context.Context,
	k8s *kubernetes.Kubernetes,
	namespace, podName string,
	maxContainers int,
	maxBytesPerContainer int,
) ([]ContainerLog, error) {
	// Get pod to discover containers
	pod, err := k8s.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Extract container list from pod spec
	var containerNames []string
	for _, container := range pod.Spec.Containers {
		containerNames = append(containerNames, container.Name)
	}

	// Limit to maxContainers
	if len(containerNames) > maxContainers {
		containerNames = containerNames[:maxContainers]
	}

	var logs []ContainerLog

	// Fetch logs for each container (current and previous)
	for _, containerName := range containerNames {
		// Try to get current logs
		currentLog, currentErr := getPodLogs(ctx, k8s, namespace, podName, containerName, false)
		if currentErr == nil {
			truncated := truncateLog(currentLog, maxBytesPerContainer)
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
		previousLog, previousErr := getPodLogs(ctx, k8s, namespace, podName, containerName, true)
		if previousErr == nil && previousLog != "" {
			truncated := truncateLog(previousLog, maxBytesPerContainer)
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

// getPodLogs retrieves logs from a specific container in a pod
func getPodLogs(ctx context.Context, k8s *kubernetes.Kubernetes, namespace, podName, containerName string, previous bool) (string, error) {
	opts := &v1.PodLogOptions{
		Container: containerName,
		Previous:  previous,
	}

	req := k8s.CoreV1().Pods(namespace).GetLogs(podName, opts)
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
