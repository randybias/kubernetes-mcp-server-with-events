package detectors

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/containers/kubernetes-mcp-server/pkg/events"
)

// DeploymentFailureDetector detects when a Deployment fails to roll out due to
// exceeding its progress deadline. A failure is detected when:
// 1. The Deployment has a Progressing condition with Status="False"
// 2. The condition's Reason is "ProgressDeadlineExceeded"
//
// This detector only triggers on transitions - when the Deployment transitions
// from a healthy state to a ProgressDeadlineExceeded state.
type DeploymentFailureDetector struct{}

// NewDeploymentFailureDetector creates a new DeploymentFailureDetector instance.
func NewDeploymentFailureDetector() *DeploymentFailureDetector {
	return &DeploymentFailureDetector{}
}

// Detect analyzes Deployment state changes and returns fault signals for detected
// rollout failures. It detects transitions to the ProgressDeadlineExceeded state
// by comparing the Progressing condition between oldObj and newObj.
func (d *DeploymentFailureDetector) Detect(oldObj, newObj interface{}) []events.FaultSignal {
	// Handle nil newObj - nothing to detect
	if newObj == nil {
		return []events.FaultSignal{}
	}

	// Type assert to Deployment
	newDeployment, ok := newObj.(*appsv1.Deployment)
	if !ok {
		return []events.FaultSignal{}
	}

	// If oldObj is nil (Add event), no transition to detect
	if oldObj == nil {
		return []events.FaultSignal{}
	}

	oldDeployment, ok := oldObj.(*appsv1.Deployment)
	if !ok {
		return []events.FaultSignal{}
	}

	// Get Progressing conditions from both Deployments
	oldProgressing := getProgressingCondition(oldDeployment)
	newProgressing := getProgressingCondition(newDeployment)

	// If new Progressing condition is missing, no failure to detect
	if newProgressing == nil {
		return []events.FaultSignal{}
	}

	// Detect transition to ProgressDeadlineExceeded state
	// This happens when:
	// 1. New condition has Status="False" and Reason="ProgressDeadlineExceeded"
	// 2. AND (oldObj had no Progressing condition OR old condition was not ProgressDeadlineExceeded)
	if newProgressing.Status == corev1.ConditionFalse &&
		newProgressing.Reason == "ProgressDeadlineExceeded" {

		// Check if this is a transition (not already in failure state)
		if oldProgressing == nil ||
			oldProgressing.Status != corev1.ConditionFalse ||
			oldProgressing.Reason != "ProgressDeadlineExceeded" {

			context := buildDeploymentFailureContext(newProgressing)

			signal := events.FaultSignal{
				FaultType:   events.FaultTypeDeploymentFailure,
				ResourceUID: types.UID(newDeployment.UID),
				Kind:        "Deployment",
				Name:        newDeployment.Name,
				Namespace:   newDeployment.Namespace,
				Severity:    events.SeverityCritical,
				Context:     context,
				Timestamp:   time.Now(),
			}

			return []events.FaultSignal{signal}
		}
	}

	return []events.FaultSignal{}
}

// getProgressingCondition finds the Progressing condition in a Deployment's status.
// Returns nil if the Progressing condition is not found.
func getProgressingCondition(deployment *appsv1.Deployment) *appsv1.DeploymentCondition {
	if deployment == nil {
		return nil
	}

	for i := range deployment.Status.Conditions {
		if deployment.Status.Conditions[i].Type == appsv1.DeploymentProgressing {
			return &deployment.Status.Conditions[i]
		}
	}

	return nil
}

// buildDeploymentFailureContext creates a human-readable context string from
// the Deployment's Progressing condition.
func buildDeploymentFailureContext(progressingCondition *appsv1.DeploymentCondition) string {
	context := "Deployment rollout failed: ProgressDeadlineExceeded"

	if progressingCondition.Reason != "" && progressingCondition.Reason != "ProgressDeadlineExceeded" {
		context += fmt.Sprintf(", reason: %s", progressingCondition.Reason)
	}

	if progressingCondition.Message != "" {
		context += fmt.Sprintf(", message: %s", progressingCondition.Message)
	}

	return context
}
