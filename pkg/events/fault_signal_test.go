package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/types"
)

// FaultSignalTestSuite contains tests for FaultSignal struct and related types
type FaultSignalTestSuite struct {
	suite.Suite
}

func TestFaultSignalSuite(t *testing.T) {
	suite.Run(t, new(FaultSignalTestSuite))
}

// TestFaultSignal_Construction tests basic construction of FaultSignal
func (s *FaultSignalTestSuite) TestFaultSignal_Construction() {
	s.Run("creates valid FaultSignal with all fields", func() {
		now := time.Now()
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("test-uid-123"),
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			ContainerName: "app-container",
			Severity:      SeverityCritical,
			Context:       "Error: OOMKilled",
			Timestamp:     now,
		}

		s.Equal(FaultTypePodCrash, signal.FaultType)
		s.Equal(types.UID("test-uid-123"), signal.ResourceUID)
		s.Equal("Pod", signal.Kind)
		s.Equal("test-pod", signal.Name)
		s.Equal("default", signal.Namespace)
		s.Equal("app-container", signal.ContainerName)
		s.Equal(SeverityCritical, signal.Severity)
		s.Equal("Error: OOMKilled", signal.Context)
		s.Equal(now, signal.Timestamp)
	})

	s.Run("creates FaultSignal without optional fields", func() {
		now := time.Now()
		signal := FaultSignal{
			FaultType:   FaultTypeNodeUnhealthy,
			ResourceUID: types.UID("node-uid-456"),
			Kind:        "Node",
			Name:        "worker-node-1",
			Severity:    SeverityWarning,
			Timestamp:   now,
		}

		s.Equal(FaultTypeNodeUnhealthy, signal.FaultType)
		s.Equal(types.UID("node-uid-456"), signal.ResourceUID)
		s.Equal("Node", signal.Kind)
		s.Equal("worker-node-1", signal.Name)
		s.Empty(signal.Namespace, "namespace should be empty for cluster-scoped resource")
		s.Empty(signal.ContainerName, "container name should be empty for node fault")
		s.Equal(SeverityWarning, signal.Severity)
		s.Empty(signal.Context)
		s.Equal(now, signal.Timestamp)
	})
}

// TestFaultSignal_JSONSerialization tests JSON serialization and deserialization
func (s *FaultSignalTestSuite) TestFaultSignal_JSONSerialization() {
	s.Run("serializes to JSON correctly", func() {
		now := time.Date(2025, 12, 19, 10, 30, 0, 0, time.UTC)
		signal := FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("pod-uid-789"),
			Kind:          "Pod",
			Name:          "crashloop-pod",
			Namespace:     "production",
			ContainerName: "main",
			Severity:      SeverityCritical,
			Context:       "Container exited with code 1",
			Timestamp:     now,
		}

		jsonData, err := json.Marshal(signal)
		s.Require().NoError(err, "JSON marshaling should not fail")
		s.NotEmpty(jsonData)

		// Verify key fields are present in JSON
		jsonStr := string(jsonData)
		s.Contains(jsonStr, "CrashLoop")
		s.Contains(jsonStr, "pod-uid-789")
		s.Contains(jsonStr, "crashloop-pod")
		s.Contains(jsonStr, "production")
		s.Contains(jsonStr, "main")
		s.Contains(jsonStr, "critical")
	})

	s.Run("deserializes from JSON correctly", func() {
		jsonData := `{
			"faultType": "PodCrash",
			"resourceUid": "test-uid-abc",
			"kind": "Pod",
			"name": "failed-pod",
			"namespace": "staging",
			"containerName": "sidecar",
			"severity": "warning",
			"context": "Container terminated unexpectedly",
			"timestamp": "2025-12-19T10:30:00Z"
		}`

		var signal FaultSignal
		err := json.Unmarshal([]byte(jsonData), &signal)
		s.Require().NoError(err, "JSON unmarshaling should not fail")

		s.Equal(FaultTypePodCrash, signal.FaultType)
		s.Equal(types.UID("test-uid-abc"), signal.ResourceUID)
		s.Equal("Pod", signal.Kind)
		s.Equal("failed-pod", signal.Name)
		s.Equal("staging", signal.Namespace)
		s.Equal("sidecar", signal.ContainerName)
		s.Equal(SeverityWarning, signal.Severity)
		s.Equal("Container terminated unexpectedly", signal.Context)
	})

	s.Run("handles missing optional fields in JSON", func() {
		jsonData := `{
			"faultType": "NodeUnhealthy",
			"resourceUid": "node-uid-def",
			"kind": "Node",
			"name": "worker-2",
			"severity": "info",
			"timestamp": "2025-12-19T10:30:00Z"
		}`

		var signal FaultSignal
		err := json.Unmarshal([]byte(jsonData), &signal)
		s.Require().NoError(err, "JSON unmarshaling should not fail")

		s.Equal(FaultTypeNodeUnhealthy, signal.FaultType)
		s.Empty(signal.Namespace)
		s.Empty(signal.ContainerName)
		s.Empty(signal.Context)
	})
}

// TestFaultType_Constants tests all FaultType constant values
func (s *FaultSignalTestSuite) TestFaultType_Constants() {
	s.Run("all fault type constants are defined", func() {
		s.Equal(FaultType("PodCrash"), FaultTypePodCrash)
		s.Equal(FaultType("CrashLoop"), FaultTypeCrashLoop)
		s.Equal(FaultType("NodeUnhealthy"), FaultTypeNodeUnhealthy)
		s.Equal(FaultType("DeploymentFailure"), FaultTypeDeploymentFailure)
		s.Equal(FaultType("JobFailure"), FaultTypeJobFailure)
	})

	s.Run("fault type constants are unique", func() {
		faultTypes := map[FaultType]bool{
			FaultTypePodCrash:          true,
			FaultTypeCrashLoop:         true,
			FaultTypeNodeUnhealthy:     true,
			FaultTypeDeploymentFailure: true,
			FaultTypeJobFailure:        true,
		}

		// All 5 types should be unique
		s.Equal(5, len(faultTypes))
	})
}

// TestSeverity_Constants tests all Severity constant values
func (s *FaultSignalTestSuite) TestSeverity_Constants() {
	s.Run("all severity constants are defined", func() {
		s.Equal(Severity("info"), SeverityInfo)
		s.Equal(Severity("warning"), SeverityWarning)
		s.Equal(Severity("critical"), SeverityCritical)
	})

	s.Run("severity constants are unique", func() {
		severities := map[Severity]bool{
			SeverityInfo:     true,
			SeverityWarning:  true,
			SeverityCritical: true,
		}

		// All 3 severities should be unique
		s.Equal(3, len(severities))
	})

	s.Run("severity values are lowercase", func() {
		s.Equal("info", string(SeverityInfo))
		s.Equal("warning", string(SeverityWarning))
		s.Equal("critical", string(SeverityCritical))
	})
}

// TestFaultSignal_EdgeCases tests edge cases and boundary conditions
func (s *FaultSignalTestSuite) TestFaultSignal_EdgeCases() {
	s.Run("handles empty strings", func() {
		now := time.Now()
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID(""),
			Kind:          "",
			Name:          "",
			Namespace:     "",
			ContainerName: "",
			Severity:      SeverityInfo,
			Context:       "",
			Timestamp:     now,
		}

		// Should not panic with empty strings
		s.Equal(types.UID(""), signal.ResourceUID)
		s.Empty(signal.Kind)
		s.Empty(signal.Name)
	})

	s.Run("handles zero time value", func() {
		signal := FaultSignal{
			FaultType:   FaultTypePodCrash,
			ResourceUID: types.UID("test-uid"),
			Kind:        "Pod",
			Name:        "test-pod",
			Severity:    SeverityInfo,
			Timestamp:   time.Time{}, // Zero value
		}

		s.True(signal.Timestamp.IsZero())
	})

	s.Run("handles very long context strings", func() {
		longContext := string(make([]byte, 10000))
		signal := FaultSignal{
			FaultType:   FaultTypeJobFailure,
			ResourceUID: types.UID("job-uid"),
			Kind:        "Job",
			Name:        "batch-job",
			Severity:    SeverityWarning,
			Context:     longContext,
			Timestamp:   time.Now(),
		}

		s.Len(signal.Context, 10000)
	})

	s.Run("handles special characters in fields", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("uid-with-special-chars-!@#$%"),
			Kind:          "Pod",
			Name:          "pod-with-special-name_123",
			Namespace:     "ns-with-hyphen",
			ContainerName: "container.with.dots",
			Severity:      SeverityWarning,
			Context:       "Error: \"quoted message\" with\nnewlines\tand\ttabs",
			Timestamp:     time.Now(),
		}

		// Should handle special characters without issues
		s.Contains(string(signal.ResourceUID), "!@#$%")
		s.Contains(signal.Name, "_123")
		s.Contains(signal.ContainerName, ".")
		s.Contains(signal.Context, "\"")
		s.Contains(signal.Context, "\n")
		s.Contains(signal.Context, "\t")
	})
}

// TestFaultSignal_TypedFields tests type safety of fields
func (s *FaultSignalTestSuite) TestFaultSignal_TypedFields() {
	s.Run("FaultType is strongly typed", func() {
		ft := FaultTypePodCrash
		s.IsType(FaultType(""), ft)
	})

	s.Run("Severity is strongly typed", func() {
		sev := SeverityCritical
		s.IsType(Severity(""), sev)
	})

	s.Run("ResourceUID is k8s types.UID", func() {
		var uid types.UID = "test-uid"
		signal := FaultSignal{ResourceUID: uid}
		s.IsType(types.UID(""), signal.ResourceUID)
	})

	s.Run("Timestamp is time.Time", func() {
		now := time.Now()
		signal := FaultSignal{Timestamp: now}
		s.IsType(time.Time{}, signal.Timestamp)
	})
}

// TestDetector_Interface tests the Detector interface definition
func (s *FaultSignalTestSuite) TestDetector_Interface() {
	s.Run("mock detector implements Detector interface", func() {
		var _ Detector = &mockDetector{}
	})

	s.Run("mock detector returns fault signals", func() {
		detector := &mockDetector{
			signals: []FaultSignal{
				{
					FaultType:   FaultTypePodCrash,
					ResourceUID: types.UID("test-uid"),
					Kind:        "Pod",
					Name:        "test-pod",
					Severity:    SeverityCritical,
					Timestamp:   time.Now(),
				},
			},
		}

		signals := detector.Detect(nil, nil)
		s.Len(signals, 1)
		s.Equal(FaultTypePodCrash, signals[0].FaultType)
	})

	s.Run("mock detector can return empty slice", func() {
		detector := &mockDetector{
			signals: []FaultSignal{},
		}

		signals := detector.Detect(nil, nil)
		s.Empty(signals)
		s.NotNil(signals)
	})

	s.Run("mock detector handles nil objects", func() {
		detector := &mockDetector{
			signals: []FaultSignal{},
		}

		// Should not panic with nil objects
		signals := detector.Detect(nil, nil)
		s.NotNil(signals)
	})
}

// mockDetector is a simple mock implementation of the Detector interface for testing
type mockDetector struct {
	signals []FaultSignal
}

func (m *mockDetector) Detect(oldObj, newObj interface{}) []FaultSignal {
	return m.signals
}
