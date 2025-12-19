package events

import (
	"testing"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FiltersTestSuite struct {
	suite.Suite
}

func TestFiltersSuite(t *testing.T) {
	suite.Run(t, new(FiltersTestSuite))
}

// TestValidate_PassesForValidInputs tests that Validate() passes for valid filter configurations
func (s *FiltersTestSuite) TestValidate_PassesForValidInputs() {
	s.Run("empty filters are valid", func() {
		filters := SubscriptionFilters{}
		err := filters.Validate()
		s.NoError(err)
	})

	s.Run("valid label selector is accepted", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx,tier=frontend",
		}
		err := filters.Validate()
		s.NoError(err)
	})

	s.Run("valid label selector with operators", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx,tier!=backend,environment in (prod,staging)",
		}
		err := filters.Validate()
		s.NoError(err)
	})

	s.Run("Warning type is valid", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}
		err := filters.Validate()
		s.NoError(err)
	})

	s.Run("Normal type is valid", func() {
		filters := SubscriptionFilters{
			Type: "Normal",
		}
		err := filters.Validate()
		s.NoError(err)
	})

	s.Run("all valid fields together", func() {
		filters := SubscriptionFilters{
			Namespaces:        []string{"default", "kube-system"},
			LabelSelector:     "app=nginx",
			InvolvedKind:      "Pod",
			InvolvedName:      "test-pod",
			InvolvedNamespace: "default",
			Type:              "Warning",
			Reason:            "BackOff",
		}
		err := filters.Validate()
		s.NoError(err)
	})
}

// TestValidate_FailsForInvalidLabelSelector tests that Validate() fails for invalid label selectors
func (s *FiltersTestSuite) TestValidate_FailsForInvalidLabelSelector() {
	s.Run("rejects invalid label selector syntax", func() {
		filters := SubscriptionFilters{
			LabelSelector: "invalid=label=selector",
		}
		err := filters.Validate()
		s.Error(err)
		s.Contains(err.Error(), "invalid label selector")
	})

	s.Run("rejects label selector with unmatched parentheses", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app in (prod",
		}
		err := filters.Validate()
		s.Error(err)
		s.Contains(err.Error(), "invalid label selector")
	})

	s.Run("rejects malformed label selector", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app in ()",
		}
		err := filters.Validate()
		s.Error(err)
		s.Contains(err.Error(), "invalid label selector")
	})
}

// TestValidate_RejectsInvalidType tests that Validate() rejects invalid type values
func (s *FiltersTestSuite) TestValidate_RejectsInvalidType() {
	s.Run("rejects invalid type value", func() {
		filters := SubscriptionFilters{
			Type: "Invalid",
		}
		err := filters.Validate()
		s.Error(err)
		s.Contains(err.Error(), "invalid type")
	})

	s.Run("rejects lowercase type values", func() {
		filters := SubscriptionFilters{
			Type: "warning",
		}
		err := filters.Validate()
		s.Error(err)
		s.Contains(err.Error(), "invalid type")
	})
}

// TestValidateForMode_FaultsMode tests that ValidateForMode() enforces faults mode restrictions
func (s *FiltersTestSuite) TestValidateForMode_FaultsMode() {
	s.Run("rejects Normal type in faults mode", func() {
		filters := SubscriptionFilters{
			Type: "Normal",
		}
		err := filters.ValidateForMode("faults")
		s.Error(err)
		s.Contains(err.Error(), "faults mode cannot filter for Normal events")
	})

	s.Run("accepts Warning type in faults mode", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}
		err := filters.ValidateForMode("faults")
		s.NoError(err)
	})

	s.Run("accepts empty type in faults mode", func() {
		filters := SubscriptionFilters{}
		err := filters.ValidateForMode("faults")
		s.NoError(err)
	})

	s.Run("validates label selector in faults mode", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app in (prod",
		}
		err := filters.ValidateForMode("faults")
		s.Error(err)
		s.Contains(err.Error(), "invalid label selector")
	})
}

// TestValidateForMode_EventsMode tests that ValidateForMode() works correctly for events mode
func (s *FiltersTestSuite) TestValidateForMode_EventsMode() {
	s.Run("accepts Normal type in events mode", func() {
		filters := SubscriptionFilters{
			Type: "Normal",
		}
		err := filters.ValidateForMode("events")
		s.NoError(err)
	})

	s.Run("accepts Warning type in events mode", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}
		err := filters.ValidateForMode("events")
		s.NoError(err)
	})

	s.Run("validates label selector in events mode", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app in (prod",
		}
		err := filters.ValidateForMode("events")
		s.Error(err)
		s.Contains(err.Error(), "invalid label selector")
	})
}

// TestMatches_FiltersByNamespace tests that Matches() filters by namespace
func (s *FiltersTestSuite) TestMatches_FiltersByNamespace() {
	s.Run("matches event in specified namespace", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects event not in specified namespace", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system",
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches event in any of multiple namespaces", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default", "kube-system", "production"},
		}

		event1 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
		}

		event2 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "production",
			},
		}

		event3 := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "staging",
			},
		}

		s.True(filters.Matches(event1))
		s.True(filters.Matches(event2))
		s.False(filters.Matches(event3))
	})

	s.Run("empty namespace filter matches all", func() {
		filters := SubscriptionFilters{}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "any-namespace",
			},
		}

		s.True(filters.Matches(event))
	})
}

// TestMatches_FiltersByType tests that Matches() filters by event type
func (s *FiltersTestSuite) TestMatches_FiltersByType() {
	s.Run("matches Warning event when filtered for Warning", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}

		event := &v1.Event{
			Type: "Warning",
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects Normal event when filtered for Warning", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}

		event := &v1.Event{
			Type: "Normal",
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches Normal event when filtered for Normal", func() {
		filters := SubscriptionFilters{
			Type: "Normal",
		}

		event := &v1.Event{
			Type: "Normal",
		}

		s.True(filters.Matches(event))
	})

	s.Run("empty type filter matches all types", func() {
		filters := SubscriptionFilters{}

		warning := &v1.Event{Type: "Warning"}
		normal := &v1.Event{Type: "Normal"}

		s.True(filters.Matches(warning))
		s.True(filters.Matches(normal))
	})
}

// TestMatches_FiltersByReason tests that Matches() filters by reason prefix
func (s *FiltersTestSuite) TestMatches_FiltersByReason() {
	s.Run("matches event with exact reason", func() {
		filters := SubscriptionFilters{
			Reason: "BackOff",
		}

		event := &v1.Event{
			Reason: "BackOff",
		}

		s.True(filters.Matches(event))
	})

	s.Run("matches event with reason prefix", func() {
		filters := SubscriptionFilters{
			Reason: "Failed",
		}

		event1 := &v1.Event{Reason: "Failed"}
		event2 := &v1.Event{Reason: "FailedScheduling"}
		event3 := &v1.Event{Reason: "FailedMount"}

		s.True(filters.Matches(event1))
		s.True(filters.Matches(event2))
		s.True(filters.Matches(event3))
	})

	s.Run("rejects event without reason prefix", func() {
		filters := SubscriptionFilters{
			Reason: "Failed",
		}

		event := &v1.Event{
			Reason: "BackOff",
		}

		s.False(filters.Matches(event))
	})

	s.Run("empty reason filter matches all reasons", func() {
		filters := SubscriptionFilters{}

		event := &v1.Event{
			Reason: "AnyReason",
		}

		s.True(filters.Matches(event))
	})
}

// TestMatches_FiltersByInvolvedObject tests that Matches() filters by involved object
func (s *FiltersTestSuite) TestMatches_FiltersByInvolvedObject() {
	s.Run("matches by involved object kind", func() {
		filters := SubscriptionFilters{
			InvolvedKind: "Pod",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Kind: "Pod",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects different involved object kind", func() {
		filters := SubscriptionFilters{
			InvolvedKind: "Pod",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Kind: "Deployment",
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches by involved object name", func() {
		filters := SubscriptionFilters{
			InvolvedName: "nginx-pod",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Name: "nginx-pod",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects different involved object name", func() {
		filters := SubscriptionFilters{
			InvolvedName: "nginx-pod",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Name: "redis-pod",
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches by involved object namespace", func() {
		filters := SubscriptionFilters{
			InvolvedNamespace: "production",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Namespace: "production",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects different involved object namespace", func() {
		filters := SubscriptionFilters{
			InvolvedNamespace: "production",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Namespace: "staging",
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches by all involved object fields", func() {
		filters := SubscriptionFilters{
			InvolvedKind:      "Pod",
			InvolvedName:      "nginx-pod",
			InvolvedNamespace: "production",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "nginx-pod",
				Namespace: "production",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects when one involved object field doesn't match", func() {
		filters := SubscriptionFilters{
			InvolvedKind:      "Pod",
			InvolvedName:      "nginx-pod",
			InvolvedNamespace: "production",
		}

		event := &v1.Event{
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "nginx-pod",
				Namespace: "staging", // Wrong namespace
			},
		}

		s.False(filters.Matches(event))
	})
}

// TestMatches_FiltersByLabels tests that Matches() filters by label selector
func (s *FiltersTestSuite) TestMatches_FiltersByLabels() {
	s.Run("matches event with matching labels", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nginx",
				},
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects event without matching labels", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "redis",
				},
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("matches event with multiple labels", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx,tier=frontend",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app":  "nginx",
					"tier": "frontend",
				},
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects event missing one required label", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx,tier=frontend",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nginx",
					// Missing tier label
				},
			},
		}

		s.False(filters.Matches(event))
	})

	s.Run("empty label selector matches all", func() {
		filters := SubscriptionFilters{}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"app": "nginx",
				},
			},
		}

		s.True(filters.Matches(event))
	})
}

// TestMatches_CombinedFilters tests that Matches() correctly combines multiple filters
func (s *FiltersTestSuite) TestMatches_CombinedFilters() {
	s.Run("matches when all filters satisfied", func() {
		filters := SubscriptionFilters{
			Namespaces:    []string{"default"},
			Type:          "Warning",
			Reason:        "Failed",
			InvolvedKind:  "Pod",
			InvolvedName:  "test-pod",
			LabelSelector: "app=nginx",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Labels: map[string]string{
					"app": "nginx",
				},
			},
			Type:   "Warning",
			Reason: "FailedScheduling",
			InvolvedObject: v1.ObjectReference{
				Kind: "Pod",
				Name: "test-pod",
			},
		}

		s.True(filters.Matches(event))
	})

	s.Run("rejects when namespace doesn't match", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
			Type:       "Warning",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "kube-system", // Wrong namespace
			},
			Type: "Warning",
		}

		s.False(filters.Matches(event))
	})

	s.Run("rejects when type doesn't match", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
			Type:       "Warning",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
			Type: "Normal", // Wrong type
		}

		s.False(filters.Matches(event))
	})

	s.Run("rejects when any filter fails", func() {
		filters := SubscriptionFilters{
			Namespaces:   []string{"default"},
			Type:         "Warning",
			InvolvedKind: "Pod",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
			Type: "Warning",
			InvolvedObject: v1.ObjectReference{
				Kind: "Deployment", // Wrong kind
			},
		}

		s.False(filters.Matches(event))
	})
}

// TestMatchesWithObjectLabels tests the MatchesWithObjectLabels method
func (s *FiltersTestSuite) TestMatchesWithObjectLabels() {
	s.Run("uses provided object labels for matching", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx,tier=frontend",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
			Type: "Warning",
		}

		objectLabels := map[string]string{
			"app":  "nginx",
			"tier": "frontend",
		}

		s.True(filters.MatchesWithObjectLabels(event, objectLabels))
	})

	s.Run("rejects when object labels don't match selector", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx",
		}

		event := &v1.Event{}

		objectLabels := map[string]string{
			"app": "redis",
		}

		s.False(filters.MatchesWithObjectLabels(event, objectLabels))
	})

	s.Run("applies all other filters correctly", func() {
		filters := SubscriptionFilters{
			Namespaces:    []string{"default"},
			Type:          "Warning",
			LabelSelector: "app=nginx",
		}

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
			Type: "Warning",
		}

		objectLabels := map[string]string{
			"app": "nginx",
		}

		s.True(filters.MatchesWithObjectLabels(event, objectLabels))
	})
}

// TestGetNamespaceFilter tests the GetNamespaceFilter method
func (s *FiltersTestSuite) TestGetNamespaceFilter() {
	s.Run("returns namespace when single namespace specified", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
		}

		s.Equal("default", filters.GetNamespaceFilter())
	})

	s.Run("returns empty when multiple namespaces specified", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default", "kube-system"},
		}

		s.Equal("", filters.GetNamespaceFilter())
	})

	s.Run("returns empty when no namespaces specified", func() {
		filters := SubscriptionFilters{}

		s.Equal("", filters.GetNamespaceFilter())
	})
}

// TestGetInvolvedObjectFieldSelector tests the GetInvolvedObjectFieldSelector method
func (s *FiltersTestSuite) TestGetInvolvedObjectFieldSelector() {
	s.Run("builds field selector for kind", func() {
		filters := SubscriptionFilters{
			InvolvedKind: "Pod",
		}

		selector := filters.GetInvolvedObjectFieldSelector()
		s.Equal("involvedObject.kind=Pod", selector)
	})

	s.Run("builds field selector for name", func() {
		filters := SubscriptionFilters{
			InvolvedName: "nginx-pod",
		}

		selector := filters.GetInvolvedObjectFieldSelector()
		s.Equal("involvedObject.name=nginx-pod", selector)
	})

	s.Run("builds field selector for namespace", func() {
		filters := SubscriptionFilters{
			InvolvedNamespace: "production",
		}

		selector := filters.GetInvolvedObjectFieldSelector()
		s.Equal("involvedObject.namespace=production", selector)
	})

	s.Run("combines multiple fields", func() {
		filters := SubscriptionFilters{
			InvolvedKind:      "Pod",
			InvolvedName:      "nginx-pod",
			InvolvedNamespace: "production",
		}

		selector := filters.GetInvolvedObjectFieldSelector()
		s.Contains(selector, "involvedObject.kind=Pod")
		s.Contains(selector, "involvedObject.name=nginx-pod")
		s.Contains(selector, "involvedObject.namespace=production")
	})

	s.Run("returns empty when no involved object filters", func() {
		filters := SubscriptionFilters{}

		selector := filters.GetInvolvedObjectFieldSelector()
		s.Equal("", selector)
	})
}

// TestRequiresClientSideFiltering tests the RequiresClientSideFiltering method
func (s *FiltersTestSuite) TestRequiresClientSideFiltering() {
	s.Run("returns true for multiple namespaces", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default", "kube-system"},
		}

		s.True(filters.RequiresClientSideFiltering())
	})

	s.Run("returns true for reason filtering", func() {
		filters := SubscriptionFilters{
			Reason: "Failed",
		}

		s.True(filters.RequiresClientSideFiltering())
	})

	s.Run("returns false for single namespace", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
		}

		s.False(filters.RequiresClientSideFiltering())
	})

	s.Run("returns false for label selector only", func() {
		filters := SubscriptionFilters{
			LabelSelector: "app=nginx",
		}

		s.False(filters.RequiresClientSideFiltering())
	})

	s.Run("returns false for involved object filters only", func() {
		filters := SubscriptionFilters{
			InvolvedKind: "Pod",
			InvolvedName: "test-pod",
		}

		s.False(filters.RequiresClientSideFiltering())
	})
}

// TestToMap tests the ToMap method
func (s *FiltersTestSuite) TestToMap() {
	s.Run("converts all fields to map", func() {
		filters := SubscriptionFilters{
			Namespaces:        []string{"default", "kube-system"},
			LabelSelector:     "app=nginx",
			InvolvedKind:      "Pod",
			InvolvedName:      "test-pod",
			InvolvedNamespace: "production",
			Type:              "Warning",
			Reason:            "Failed",
		}

		m := filters.ToMap()

		s.Equal([]string{"default", "kube-system"}, m["namespaces"])
		s.Equal("app=nginx", m["labelSelector"])
		s.Equal("Pod", m["involvedKind"])
		s.Equal("test-pod", m["involvedName"])
		s.Equal("production", m["involvedNamespace"])
		s.Equal("Warning", m["type"])
		s.Equal("Failed", m["reason"])
	})

	s.Run("omits empty fields from map", func() {
		filters := SubscriptionFilters{
			Type: "Warning",
		}

		m := filters.ToMap()

		s.Equal("Warning", m["type"])
		s.NotContains(m, "namespaces")
		s.NotContains(m, "labelSelector")
		s.NotContains(m, "involvedKind")
	})
}

// TestParseFiltersFromMap tests the ParseFiltersFromMap function
func (s *FiltersTestSuite) TestParseFiltersFromMap() {
	s.Run("parses all fields from map", func() {
		args := map[string]interface{}{
			"namespaces":        []interface{}{"default", "kube-system"},
			"labelSelector":     "app=nginx",
			"involvedKind":      "Pod",
			"involvedName":      "test-pod",
			"involvedNamespace": "production",
			"type":              "Warning",
			"reason":            "Failed",
		}

		filters := ParseFiltersFromMap(args)

		s.Equal([]string{"default", "kube-system"}, filters.Namespaces)
		s.Equal("app=nginx", filters.LabelSelector)
		s.Equal("Pod", filters.InvolvedKind)
		s.Equal("test-pod", filters.InvolvedName)
		s.Equal("production", filters.InvolvedNamespace)
		s.Equal("Warning", filters.Type)
		s.Equal("Failed", filters.Reason)
	})

	s.Run("handles empty map", func() {
		args := map[string]interface{}{}

		filters := ParseFiltersFromMap(args)

		s.Empty(filters.Namespaces)
		s.Empty(filters.LabelSelector)
		s.Empty(filters.InvolvedKind)
	})

	s.Run("handles missing fields", func() {
		args := map[string]interface{}{
			"type": "Warning",
		}

		filters := ParseFiltersFromMap(args)

		s.Equal("Warning", filters.Type)
		s.Empty(filters.Namespaces)
		s.Empty(filters.LabelSelector)
	})
}

// TestFiltersRoundTrip tests ToMap and ParseFiltersFromMap round trip
func (s *FiltersTestSuite) TestFiltersRoundTrip() {
	s.Run("round trip preserves all data", func() {
		original := SubscriptionFilters{
			Namespaces:        []string{"default", "kube-system"},
			LabelSelector:     "app=nginx",
			InvolvedKind:      "Pod",
			InvolvedName:      "test-pod",
			InvolvedNamespace: "production",
			Type:              "Warning",
			Reason:            "Failed",
		}

		m := original.ToMap()

		// Convert []string to []interface{} to simulate JSON unmarshaling
		if ns, ok := m["namespaces"].([]string); ok {
			nsInterface := make([]interface{}, len(ns))
			for i, v := range ns {
				nsInterface[i] = v
			}
			m["namespaces"] = nsInterface
		}

		parsed := ParseFiltersFromMap(m)

		s.Equal(original.Namespaces, parsed.Namespaces)
		s.Equal(original.LabelSelector, parsed.LabelSelector)
		s.Equal(original.InvolvedKind, parsed.InvolvedKind)
		s.Equal(original.InvolvedName, parsed.InvolvedName)
		s.Equal(original.InvolvedNamespace, parsed.InvolvedNamespace)
		s.Equal(original.Type, parsed.Type)
		s.Equal(original.Reason, parsed.Reason)
	})
}
