package events

import (
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
)

// EventNotification represents the notification payload for kubernetes/events
type EventNotification struct {
	SubscriptionID string        `json:"subscriptionId"`
	Cluster        string        `json:"cluster"`
	Event          *EventDetails `json:"event"`
}

// EventDetails contains the serialized event information
type EventDetails struct {
	Namespace      string            `json:"namespace"`
	Timestamp      string            `json:"timestamp"`
	Type           string            `json:"type"`
	Reason         string            `json:"reason"`
	Message        string            `json:"message"`
	Labels         map[string]string `json:"labels,omitempty"`
	InvolvedObject *InvolvedObject   `json:"involvedObject"`
	Count          int32             `json:"count,omitempty"`
	FirstTimestamp string            `json:"firstTimestamp,omitempty"`
	LastTimestamp  string            `json:"lastTimestamp,omitempty"`
}

// InvolvedObject represents the object involved in the event
type InvolvedObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Namespace  string `json:"namespace,omitempty"`
	UID        string `json:"uid,omitempty"`
}

// SubscriptionErrorNotification represents the notification payload for subscription errors
type SubscriptionErrorNotification struct {
	SubscriptionID string `json:"subscriptionId"`
	Cluster        string `json:"cluster"`
	Error          string `json:"error"`
	Degraded       bool   `json:"degraded"`
}

// SerializeEvent converts a Kubernetes Event to EventDetails
func SerializeEvent(event *v1.Event) *EventDetails {
	// Determine the best timestamp to use
	timestamp := event.EventTime.Time
	if timestamp.IsZero() && event.Series != nil {
		timestamp = event.Series.LastObservedTime.Time
	} else if timestamp.IsZero() && event.Count > 1 {
		timestamp = event.LastTimestamp.Time
	} else if timestamp.IsZero() {
		timestamp = event.FirstTimestamp.Time
	}

	details := &EventDetails{
		Namespace: event.Namespace,
		Timestamp: formatTimestamp(timestamp),
		Type:      event.Type,
		Reason:    event.Reason,
		Message:   strings.TrimSpace(event.Message),
		InvolvedObject: &InvolvedObject{
			APIVersion: event.InvolvedObject.APIVersion,
			Kind:       event.InvolvedObject.Kind,
			Name:       event.InvolvedObject.Name,
			Namespace:  event.InvolvedObject.Namespace,
			UID:        string(event.InvolvedObject.UID),
		},
	}

	// Add optional fields
	if event.Count > 0 {
		details.Count = event.Count
	}

	if !event.FirstTimestamp.IsZero() {
		details.FirstTimestamp = formatTimestamp(event.FirstTimestamp.Time)
	}

	if !event.LastTimestamp.IsZero() {
		details.LastTimestamp = formatTimestamp(event.LastTimestamp.Time)
	}

	// Add labels from the involved object if available
	// Note: We don't have direct access to the object here,
	// so labels would need to be added by the caller if needed
	details.Labels = make(map[string]string)

	return details
}

// formatTimestamp formats a time.Time to RFC3339 string
func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// Logger name constants for notification delivery
const (
	LoggerEvents            = "kubernetes/events"
	LoggerFaults            = "kubernetes/faults"
	LoggerSubscriptionError = "kubernetes/subscription_error"
)
