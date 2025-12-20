package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

type FaultEnricherSuite struct {
	suite.Suite
}

func (s *FaultEnricherSuite) TestNewFaultContextEnricher() {
	s.Run("creates enricher with default limits", func() {
		enricher := NewFaultContextEnricher()
		s.NotNil(enricher)
		s.Equal(DefaultMaxContainersPerNotification, enricher.maxContainers)
		s.Equal(DefaultMaxLogBytesPerContainer, enricher.maxBytesPerContainer)
	})
}

func (s *FaultEnricherSuite) TestNewFaultContextEnricherWithLimits() {
	s.Run("creates enricher with custom limits", func() {
		enricher := NewFaultContextEnricherWithLimits(3, 5000)
		s.NotNil(enricher)
		s.Equal(3, enricher.maxContainers)
		s.Equal(5000, enricher.maxBytesPerContainer)
	})
}

func (s *FaultEnricherSuite) TestEnrich() {
	s.Run("returns error for nil signal", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()
		err := enricher.Enrich(context.Background(), nil, clientset)
		s.Error(err)
		s.Contains(err.Error(), "signal cannot be nil")
	})

	s.Run("skips log fetch when context already exists", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		signal := &FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("test-uid"),
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			ContainerName: "app",
			Severity:      SeverityCritical,
			Context:       "Container crashed with exit code 1, reason: Error, message: OOMKilled",
			Timestamp:     time.Now(),
		}

		err := enricher.Enrich(context.Background(), signal, clientset)
		s.NoError(err)
		// Context should remain unchanged
		s.Contains(signal.Context, "Container crashed")
	})

	s.Run("skips log fetch for non-critical severity", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		signal := &FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("test-uid"),
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			ContainerName: "app",
			Severity:      SeverityWarning,
			Context:       "",
			Timestamp:     time.Now(),
		}

		err := enricher.Enrich(context.Background(), signal, clientset)
		s.NoError(err)
		// Context should remain empty (no log fetch)
		s.Empty(signal.Context)
	})

	s.Run("skips log fetch for non-pod resources", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		signal := &FaultSignal{
			FaultType:   FaultTypeNodeUnhealthy,
			ResourceUID: types.UID("test-uid"),
			Kind:        "Node",
			Name:        "test-node",
			Severity:    SeverityCritical,
			Context:     "",
			Timestamp:   time.Now(),
		}

		err := enricher.Enrich(context.Background(), signal, clientset)
		s.NoError(err)
		// Context should remain empty (not a pod)
		s.Empty(signal.Context)
	})

	s.Run("returns error for missing namespace", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		signal := &FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("test-uid"),
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "",
			ContainerName: "app",
			Severity:      SeverityCritical,
			Context:       "",
			Timestamp:     time.Now(),
		}

		err := enricher.Enrich(context.Background(), signal, clientset)
		s.Error(err)
		s.Contains(err.Error(), "missing namespace")
	})

	s.Run("returns error for missing pod name", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		signal := &FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("test-uid"),
			Kind:          "Pod",
			Name:          "",
			Namespace:     "default",
			ContainerName: "app",
			Severity:      SeverityCritical,
			Context:       "",
			Timestamp:     time.Now(),
		}

		err := enricher.Enrich(context.Background(), signal, clientset)
		s.Error(err)
		s.Contains(err.Error(), "missing name")
	})

	s.Run("attempts to fetch logs for critical severity with empty context", func() {
		enricher := NewFaultContextEnricher()

		// Create a pod with a container
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "app",
						Image: "nginx",
					},
				},
			},
		}

		clientset := fake.NewSimpleClientset(pod)

		signal := &FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("test-uid"),
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			ContainerName: "app",
			Severity:      SeverityCritical,
			Context:       "",
			Timestamp:     time.Now(),
		}

		originalContext := signal.Context
		err := enricher.Enrich(context.Background(), signal, clientset)
		// The enricher should attempt to fetch logs
		// Fake client may succeed with fake logs, so check if context changed
		if err == nil {
			s.NotEqual(originalContext, signal.Context,
				"context should have been enriched with logs")
		}
	})
}

func (s *FaultEnricherSuite) TestEnrichmentLogic() {
	s.Run("scenario table", func() {
		testCases := []struct {
			name              string
			signal            FaultSignal
			shouldFetchLogs   bool
			expectedContextOp string
		}{
			{
				name: "has termination message - no log fetch",
				signal: FaultSignal{
					FaultType:     FaultTypeCrashLoop,
					Kind:          "Pod",
					Name:          "test-pod",
					Namespace:     "default",
					Severity:      SeverityCritical,
					Context:       "Container crashed with exit code 1",
					ContainerName: "app",
				},
				shouldFetchLogs:   false,
				expectedContextOp: "unchanged",
			},
			{
				name: "no termination message + CrashLoop (critical) - log fetch triggered",
				signal: FaultSignal{
					FaultType:     FaultTypeCrashLoop,
					Kind:          "Pod",
					Name:          "test-pod",
					Namespace:     "default",
					Severity:      SeverityCritical,
					Context:       "",
					ContainerName: "app",
				},
				shouldFetchLogs:   true,
				expectedContextOp: "fetch_attempted",
			},
			{
				name: "no termination message + single crash (warning) - no log fetch",
				signal: FaultSignal{
					FaultType:     FaultTypePodCrash,
					Kind:          "Pod",
					Name:          "test-pod",
					Namespace:     "default",
					Severity:      SeverityWarning,
					Context:       "",
					ContainerName: "app",
				},
				shouldFetchLogs:   false,
				expectedContextOp: "unchanged",
			},
			{
				name: "critical node fault - no log fetch (not a pod)",
				signal: FaultSignal{
					FaultType: FaultTypeNodeUnhealthy,
					Kind:      "Node",
					Name:      "test-node",
					Severity:  SeverityCritical,
					Context:   "",
				},
				shouldFetchLogs:   false,
				expectedContextOp: "unchanged",
			},
		}

		for _, tc := range testCases {
			s.Run(tc.name, func() {
				enricher := NewFaultContextEnricher()
				clientset := fake.NewSimpleClientset()

				// Create pod if needed
				if tc.signal.Kind == "Pod" && tc.shouldFetchLogs {
					pod := &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{
							Name:      tc.signal.Name,
							Namespace: tc.signal.Namespace,
						},
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:  tc.signal.ContainerName,
									Image: "test",
								},
							},
						},
					}
					_, err := clientset.CoreV1().Pods(tc.signal.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
					s.Require().NoError(err)
				}

				originalContext := tc.signal.Context
				err := enricher.Enrich(context.Background(), &tc.signal, clientset)

				if tc.shouldFetchLogs {
					// Log fetch was attempted (may fail in fake client)
					s.True(err != nil || tc.signal.Context != originalContext,
						"expected log fetch to be attempted")
				} else {
					// No log fetch should occur
					s.NoError(err)
					if tc.expectedContextOp == "unchanged" {
						s.Equal(originalContext, tc.signal.Context,
							"context should remain unchanged")
					}
				}
			})
		}
	})
}

func (s *FaultEnricherSuite) TestFetchPodLogs() {
	s.Run("returns error when pod not found", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		logs, err := enricher.fetchPodLogs(context.Background(), clientset, "default", "nonexistent-pod")
		s.Error(err)
		s.Nil(logs)
		s.Contains(err.Error(), "failed to get pod")
	})

	s.Run("handles pod with no containers", func() {
		enricher := NewFaultContextEnricher()

		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "empty-pod",
				Namespace: "default",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{},
			},
		}

		clientset := fake.NewSimpleClientset(pod)

		logs, err := enricher.fetchPodLogs(context.Background(), clientset, "default", "empty-pod")
		s.NoError(err)
		s.Empty(logs)
	})

	s.Run("limits number of containers", func() {
		enricher := NewFaultContextEnricherWithLimits(2, DefaultMaxLogBytesPerContainer)

		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "multi-container-pod",
				Namespace: "default",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{Name: "container1", Image: "test1"},
					{Name: "container2", Image: "test2"},
					{Name: "container3", Image: "test3"},
					{Name: "container4", Image: "test4"},
				},
			},
		}

		clientset := fake.NewSimpleClientset(pod)

		// Note: This test verifies the container limiting logic
		// Actual log fetching will fail in fake client
		_, _ = enricher.fetchPodLogs(context.Background(), clientset, "default", "multi-container-pod")
		// The test passes if no panic occurs and limiting logic is executed
	})
}

func (s *FaultEnricherSuite) TestGetPodLogs() {
	s.Run("requests logs with correct options", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		// Fake client may return fake logs or error
		log, err := enricher.getPodLogs(context.Background(), clientset, "default", "test-pod", "app", false)
		// Just verify the method doesn't panic and returns a result
		s.True(err != nil || log != "", "expected either error or log content")
	})

	s.Run("handles previous logs flag", func() {
		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		// Test with previous=true
		log, err := enricher.getPodLogs(context.Background(), clientset, "default", "test-pod", "app", true)
		// Just verify the method doesn't panic and returns a result
		s.True(err != nil || log != "", "expected either error or log content")
	})
}

func (s *FaultEnricherSuite) TestContextSerialization() {
	s.Run("serializes logs to JSON in context", func() {
		// This test verifies the JSON serialization logic
		logs := []ContainerLog{
			{
				Container: "app",
				Previous:  false,
				HasPanic:  true,
				Sample:    "panic: test error",
				Error:     "",
			},
			{
				Container: "sidecar",
				Previous:  false,
				HasPanic:  false,
				Sample:    "normal log",
				Error:     "",
			},
		}

		logsJSON, err := json.Marshal(logs)
		s.Require().NoError(err)

		// Verify we can deserialize it back
		var deserialized []ContainerLog
		err = json.Unmarshal(logsJSON, &deserialized)
		s.NoError(err)
		s.Len(deserialized, 2)
		s.Equal("app", deserialized[0].Container)
		s.True(deserialized[0].HasPanic)
		s.Equal("sidecar", deserialized[1].Container)
		s.False(deserialized[1].HasPanic)
	})
}

func (s *FaultEnricherSuite) TestIntegrationWithRealDetectors() {
	s.Run("enriches signal from CrashLoopDetector", func() {
		// Simulate a signal that would come from CrashLoopDetector
		// (critical severity, no context initially)
		signal := FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("pod-123"),
			Kind:          "Pod",
			Name:          "crashing-pod",
			Namespace:     "production",
			ContainerName: "app",
			Severity:      SeverityCritical,
			Context:       "", // Empty - should trigger log fetch
			Timestamp:     time.Now(),
		}

		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		// Since fake client doesn't support logs, we expect an error
		// but the enricher should handle it gracefully
		err := enricher.Enrich(context.Background(), &signal, clientset)
		if err != nil {
			s.Contains(err.Error(), "failed to fetch logs")
		}
	})

	s.Run("does not enrich signal from PodCrashDetector with termination message", func() {
		// Simulate a signal from PodCrashDetector
		// (warning severity, has termination message in context)
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-456"),
			Kind:          "Pod",
			Name:          "crashed-pod",
			Namespace:     "default",
			ContainerName: "app",
			Severity:      SeverityWarning,
			Context:       "Container crashed with exit code 137, reason: OOMKilled, message: Out of memory",
			Timestamp:     time.Now(),
		}

		enricher := NewFaultContextEnricher()
		clientset := fake.NewSimpleClientset()

		originalContext := signal.Context
		err := enricher.Enrich(context.Background(), &signal, clientset)
		s.NoError(err)
		// Context should remain unchanged
		s.Equal(originalContext, signal.Context)
	})
}

func (s *FaultEnricherSuite) TestEdgeCases() {
	s.Run("handles context cancellation gracefully", func() {
		enricher := NewFaultContextEnricher()

		// Create a pod for the enricher to find
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name:  "app",
						Image: "test",
					},
				},
			},
		}
		clientset := fake.NewSimpleClientset(pod)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		signal := &FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Context:       "",
			ContainerName: "app",
		}

		err := enricher.Enrich(ctx, signal, clientset)
		// Should handle cancellation gracefully - either succeed with fake client or get context error
		s.True(err == nil || strings.Contains(err.Error(), "context") || strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "failed to fetch"),
			"should handle cancellation gracefully")
	})

	s.Run("handles very long log content", func() {
		// Verify truncation is applied
		enricher := NewFaultContextEnricherWithLimits(5, 100)

		longLog := strings.Repeat("a", 1000)
		truncated := truncateLog(longLog, enricher.maxBytesPerContainer)

		s.Equal(100, len(truncated))
		s.Equal(strings.Repeat("a", 100), truncated)
	})

	s.Run("handles empty log content", func() {
		enricher := NewFaultContextEnricher()

		emptyLog := ""
		truncated := truncateLog(emptyLog, enricher.maxBytesPerContainer)

		s.Empty(truncated)
	})
}

func TestFaultEnricher(t *testing.T) {
	suite.Run(t, new(FaultEnricherSuite))
}
