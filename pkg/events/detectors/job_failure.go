package detectors

import (
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// JobFailureDetector detects when a Job fails. A failure is detected when:
// 1. The Job has a Failed condition with Status="True"
//
// This detector only triggers on transitions - when the Job transitions
// from a non-failed state to a failed state.
type JobFailureDetector struct{}

// NewJobFailureDetector creates a new JobFailureDetector instance.
func NewJobFailureDetector() *JobFailureDetector {
	return &JobFailureDetector{}
}

// Detect analyzes Job state changes and returns fault signals for detected
// failures. It detects transitions to the Failed state by comparing the
// Failed condition between oldObj and newObj.
func (d *JobFailureDetector) Detect(oldObj, newObj interface{}) []events.FaultSignal {
	// Handle nil newObj - nothing to detect
	if newObj == nil {
		return []events.FaultSignal{}
	}

	// Type assert to Job
	newJob, ok := newObj.(*batchv1.Job)
	if !ok {
		return []events.FaultSignal{}
	}

	// If oldObj is nil (Add event), no transition to detect
	if oldObj == nil {
		return []events.FaultSignal{}
	}

	oldJob, ok := oldObj.(*batchv1.Job)
	if !ok {
		return []events.FaultSignal{}
	}

	// Get Failed conditions from both Jobs
	oldFailed := getFailedCondition(oldJob)
	newFailed := getFailedCondition(newJob)

	// If new Failed condition is missing, no failure to detect
	if newFailed == nil {
		return []events.FaultSignal{}
	}

	// Detect transition to Failed state
	// This happens when:
	// 1. New condition has Status="True"
	// 2. AND (oldObj had no Failed condition OR old condition was not True)
	if newFailed.Status == corev1.ConditionTrue {
		// Check if this is a transition (not already in failure state)
		if oldFailed == nil || oldFailed.Status != corev1.ConditionTrue {
			context := buildJobFailureContext(newFailed)
			severity := determineJobFailureSeverity(newJob)

			signal := events.FaultSignal{
				FaultType:   events.FaultTypeJobFailure,
				ResourceUID: types.UID(newJob.UID),
				Kind:        "Job",
				Name:        newJob.Name,
				Namespace:   newJob.Namespace,
				Severity:    severity,
				Context:     context,
				Timestamp:   time.Now(),
			}

			return []events.FaultSignal{signal}
		}
	}

	return []events.FaultSignal{}
}

// getFailedCondition finds the Failed condition in a Job's status.
// Returns nil if the Failed condition is not found.
func getFailedCondition(job *batchv1.Job) *batchv1.JobCondition {
	if job == nil {
		return nil
	}

	for i := range job.Status.Conditions {
		if job.Status.Conditions[i].Type == batchv1.JobFailed {
			return &job.Status.Conditions[i]
		}
	}

	return nil
}

// buildJobFailureContext creates a human-readable context string from
// the Job's Failed condition.
func buildJobFailureContext(failedCondition *batchv1.JobCondition) string {
	context := "Job failed"

	if failedCondition.Reason != "" {
		context += fmt.Sprintf(", reason: %s", failedCondition.Reason)
	}

	if failedCondition.Message != "" {
		context += fmt.Sprintf(", message: %s", failedCondition.Message)
	}

	return context
}

// determineJobFailureSeverity determines the severity level based on the Job's
// failure reason. BackoffLimitExceeded is critical (job exhausted retries),
// other failure reasons are warnings.
func determineJobFailureSeverity(job *batchv1.Job) events.Severity {
	failedCondition := getFailedCondition(job)
	if failedCondition != nil && failedCondition.Reason == "BackoffLimitExceeded" {
		return events.SeverityCritical
	}
	return events.SeverityWarning
}
