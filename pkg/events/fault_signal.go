package events

import (
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// FaultType represents the category of fault detected in a Kubernetes resource.
type FaultType string

const (
	// FaultTypePodCrash indicates a container in a pod has crashed
	FaultTypePodCrash FaultType = "PodCrash"
	// FaultTypeCrashLoop indicates a pod is in a crash loop (CrashLoopBackOff)
	FaultTypeCrashLoop FaultType = "CrashLoop"
	// FaultTypeNodeUnhealthy indicates a node is in an unhealthy state
	FaultTypeNodeUnhealthy FaultType = "NodeUnhealthy"
	// FaultTypeDeploymentFailure indicates a deployment has failed to roll out
	FaultTypeDeploymentFailure FaultType = "DeploymentFailure"
	// FaultTypeJobFailure indicates a job has failed
	FaultTypeJobFailure FaultType = "JobFailure"
)

// Severity represents the severity level of a fault signal.
type Severity string

const (
	// SeverityInfo indicates an informational fault signal (low impact)
	SeverityInfo Severity = "info"
	// SeverityWarning indicates a warning-level fault signal (medium impact)
	SeverityWarning Severity = "warning"
	// SeverityCritical indicates a critical fault signal (high impact)
	SeverityCritical Severity = "critical"
)

// FaultSignal represents a detected fault condition in a Kubernetes resource.
// It is produced by fault detectors when analyzing resource state changes.
type FaultSignal struct {
	// FaultType categorizes the type of fault detected
	FaultType FaultType `json:"faultType"`

	// ResourceUID is the unique identifier of the affected resource
	ResourceUID types.UID `json:"resourceUid"`

	// Kind is the Kubernetes resource kind (e.g., Pod, Node, Deployment)
	Kind string `json:"kind"`

	// Name is the name of the affected resource
	Name string `json:"name"`

	// Namespace is the namespace of the affected resource (empty for cluster-scoped resources)
	Namespace string `json:"namespace,omitempty"`

	// ContainerName is the name of the container (for pod-level faults)
	ContainerName string `json:"containerName,omitempty"`

	// Severity indicates the severity level of this fault
	Severity Severity `json:"severity"`

	// Context provides additional information about the fault (e.g., termination message, error logs)
	Context string `json:"context,omitempty"`

	// Timestamp is when the fault was detected
	Timestamp time.Time `json:"timestamp"`
}

// Detector is an interface for fault detection logic that analyzes
// resource state changes and produces fault signals. Each detector
// implementation is responsible for detecting specific fault types
// for a particular Kubernetes resource kind.
//
// Detectors are called on resource updates (when oldObj and newObj differ)
// and perform edge-triggered detection by comparing the old and new states.
type Detector interface {
	// Detect analyzes a resource state change and returns any detected fault signals.
	// Parameters:
	//   - oldObj: the previous state of the resource (can be nil for Add events)
	//   - newObj: the current state of the resource
	// Returns:
	//   - A slice of FaultSignal representing any detected faults (empty if no faults)
	Detect(oldObj, newObj interface{}) []FaultSignal
}
