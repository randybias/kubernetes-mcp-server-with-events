package events

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// SubscriptionFilters defines the filtering criteria for event subscriptions.
// All fields are optional; empty values mean no filtering on that dimension.
type SubscriptionFilters struct {
	// Namespaces limits events to these namespaces.
	// Empty means all namespaces (cluster-wide).
	Namespaces []string

	// LabelSelector filters events by labels on the involved object.
	// Uses standard Kubernetes label selector syntax.
	// Empty means no label filtering.
	LabelSelector string

	// InvolvedKind filters events by the kind of the involved object.
	// Examples: "Pod", "Deployment", "Node"
	// Empty means all kinds.
	InvolvedKind string

	// InvolvedName filters events by the name of the involved object.
	// Empty means all names.
	InvolvedName string

	// InvolvedNamespace filters events by the namespace of the involved object.
	// Empty means all namespaces.
	InvolvedNamespace string

	// Type filters events by type: "Normal" or "Warning".
	// Empty means both types.
	Type string

	// Reason filters events by reason prefix match.
	// Examples: "BackOff", "Failed", "Killing"
	// Empty means all reasons.
	Reason string
}

// Validate checks if the filters are valid.
// Returns an error if any filter has invalid syntax.
func (f *SubscriptionFilters) Validate() error {
	// Validate label selector syntax if provided
	if f.LabelSelector != "" {
		_, err := labels.Parse(f.LabelSelector)
		if err != nil {
			return fmt.Errorf("invalid label selector: %w", err)
		}
	}

	// Validate type field if provided
	if f.Type != "" && f.Type != "Normal" && f.Type != "Warning" {
		return fmt.Errorf("invalid type: must be 'Normal', 'Warning', or empty")
	}

	return nil
}

// ValidateForMode validates filters for a specific subscription mode.
// Some modes have additional restrictions.
func (f *SubscriptionFilters) ValidateForMode(mode string) error {
	// First run standard validation
	if err := f.Validate(); err != nil {
		return err
	}

	// Faults mode only supports Warning events
	if mode == "faults" && f.Type == "Normal" {
		return fmt.Errorf("faults mode cannot filter for Normal events")
	}

	return nil
}

// Matches checks if an event matches the subscription filters.
// Returns true if the event passes all non-empty filters.
func (f *SubscriptionFilters) Matches(event *corev1.Event) bool {
	// Check namespace filter (event's own namespace)
	if len(f.Namespaces) > 0 {
		found := false
		for _, ns := range f.Namespaces {
			if event.Namespace == ns {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check type filter
	if f.Type != "" && event.Type != f.Type {
		return false
	}

	// Check reason prefix filter
	if f.Reason != "" && !strings.HasPrefix(event.Reason, f.Reason) {
		return false
	}

	// Check involved object filters
	if f.InvolvedKind != "" && event.InvolvedObject.Kind != f.InvolvedKind {
		return false
	}

	if f.InvolvedName != "" && event.InvolvedObject.Name != f.InvolvedName {
		return false
	}

	if f.InvolvedNamespace != "" && event.InvolvedObject.Namespace != f.InvolvedNamespace {
		return false
	}

	// Check label selector
	if f.LabelSelector != "" {
		selector, err := labels.Parse(f.LabelSelector)
		if err != nil {
			// This should not happen as we validated in Validate()
			return false
		}

		// Events have their own labels, not the involved object's labels
		// However, in practice we'll need to fetch the involved object to get its labels
		// For now, check event labels (this will be enhanced by the watch implementation)
		eventLabels := labels.Set(event.Labels)
		if !selector.Matches(eventLabels) {
			return false
		}
	}

	return true
}

// MatchesWithObjectLabels checks if an event matches the subscription filters,
// using the provided object labels for label selector matching.
// This allows matching against the involved object's labels without fetching it.
func (f *SubscriptionFilters) MatchesWithObjectLabels(event *corev1.Event, objectLabels map[string]string) bool {
	// First check all non-label filters
	if len(f.Namespaces) > 0 {
		found := false
		for _, ns := range f.Namespaces {
			if event.Namespace == ns {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if f.Type != "" && event.Type != f.Type {
		return false
	}

	if f.Reason != "" && !strings.HasPrefix(event.Reason, f.Reason) {
		return false
	}

	if f.InvolvedKind != "" && event.InvolvedObject.Kind != f.InvolvedKind {
		return false
	}

	if f.InvolvedName != "" && event.InvolvedObject.Name != f.InvolvedName {
		return false
	}

	if f.InvolvedNamespace != "" && event.InvolvedObject.Namespace != f.InvolvedNamespace {
		return false
	}

	// Check label selector with provided object labels
	if f.LabelSelector != "" {
		selector, err := labels.Parse(f.LabelSelector)
		if err != nil {
			return false
		}

		labelSet := labels.Set(objectLabels)
		if !selector.Matches(labelSet) {
			return false
		}
	}

	return true
}

// GetNamespaceFilter returns a field selector for namespace filtering,
// suitable for use with client-go watch requests.
// Returns empty string if no namespace filter is set or multiple namespaces are specified.
func (f *SubscriptionFilters) GetNamespaceFilter() string {
	if len(f.Namespaces) == 1 {
		return f.Namespaces[0]
	}
	return ""
}

// GetInvolvedObjectFieldSelector returns a field selector for the involved object,
// suitable for use with client-go watch requests.
// Returns empty string if no involved object filters are set.
func (f *SubscriptionFilters) GetInvolvedObjectFieldSelector() string {
	var parts []string

	if f.InvolvedKind != "" {
		parts = append(parts, fmt.Sprintf("involvedObject.kind=%s", f.InvolvedKind))
	}

	if f.InvolvedName != "" {
		parts = append(parts, fmt.Sprintf("involvedObject.name=%s", f.InvolvedName))
	}

	if f.InvolvedNamespace != "" {
		parts = append(parts, fmt.Sprintf("involvedObject.namespace=%s", f.InvolvedNamespace))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, ",")
}

// RequiresClientSideFiltering returns true if the filters require client-side
// filtering because they cannot be expressed as Kubernetes API field/label selectors.
func (f *SubscriptionFilters) RequiresClientSideFiltering() bool {
	// Multiple namespaces require client-side filtering
	if len(f.Namespaces) > 1 {
		return true
	}

	// Reason prefix matching requires client-side filtering
	if f.Reason != "" {
		return true
	}

	// Type filtering can be done server-side via field selector
	// Label selector can be done server-side
	// Single namespace can be done via namespace-scoped client
	// Involved object filters can be done via field selector

	return false
}

// ToMap converts the filters to a map for JSON serialization.
// Useful for returning filter details in tool responses.
func (f *SubscriptionFilters) ToMap() map[string]interface{} {
	m := make(map[string]interface{})

	if len(f.Namespaces) > 0 {
		m["namespaces"] = f.Namespaces
	}

	if f.LabelSelector != "" {
		m["labelSelector"] = f.LabelSelector
	}

	if f.InvolvedKind != "" {
		m["involvedKind"] = f.InvolvedKind
	}

	if f.InvolvedName != "" {
		m["involvedName"] = f.InvolvedName
	}

	if f.InvolvedNamespace != "" {
		m["involvedNamespace"] = f.InvolvedNamespace
	}

	if f.Type != "" {
		m["type"] = f.Type
	}

	if f.Reason != "" {
		m["reason"] = f.Reason
	}

	return m
}

// ParseFiltersFromMap creates a SubscriptionFilters from a map of arguments.
// This is the inverse of ToMap and is used by tool handlers.
func ParseFiltersFromMap(args map[string]interface{}) SubscriptionFilters {
	filters := SubscriptionFilters{}

	if namespaces, ok := args["namespaces"].([]interface{}); ok {
		for _, ns := range namespaces {
			if nsStr, ok := ns.(string); ok {
				filters.Namespaces = append(filters.Namespaces, nsStr)
			}
		}
	}

	if labelSelector, ok := args["labelSelector"].(string); ok {
		filters.LabelSelector = labelSelector
	}

	if involvedKind, ok := args["involvedKind"].(string); ok {
		filters.InvolvedKind = involvedKind
	}

	if involvedName, ok := args["involvedName"].(string); ok {
		filters.InvolvedName = involvedName
	}

	if involvedNamespace, ok := args["involvedNamespace"].(string); ok {
		filters.InvolvedNamespace = involvedNamespace
	}

	if eventType, ok := args["type"].(string); ok {
		filters.Type = eventType
	}

	if reason, ok := args["reason"].(string); ok {
		filters.Reason = reason
	}

	return filters
}
