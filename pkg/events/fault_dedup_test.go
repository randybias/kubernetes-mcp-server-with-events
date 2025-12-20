package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/types"
)

type FaultDeduplicatorSuite struct {
	suite.Suite
	dedup       *FaultDeduplicator
	currentTime time.Time
}

func (s *FaultDeduplicatorSuite) SetupTest() {
	// Create deduplicator with 15-minute TTL and controlled time
	s.currentTime = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	s.dedup = NewFaultDeduplicatorWithTTL(15 * time.Minute)
	s.dedup.now = func() time.Time {
		return s.currentTime
	}
}

func (s *FaultDeduplicatorSuite) advanceTime(d time.Duration) {
	s.currentTime = s.currentTime.Add(d)
}

func (s *FaultDeduplicatorSuite) TestFirstSignalPassesThrough() {
	s.Run("first signal for a resource should be emitted", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		shouldEmit := s.dedup.ShouldEmit(signal)
		s.True(shouldEmit, "first signal should be emitted")
		s.Equal(1, s.dedup.Count(), "should track one incident")
	})
}

func (s *FaultDeduplicatorSuite) TestSecondSignalWithinWindowIsDeduplicated() {
	s.Run("second signal for same incident within TTL should be suppressed", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Advance time by 5 minutes (within 15-minute TTL)
		s.advanceTime(5 * time.Minute)
		signal.Timestamp = s.currentTime

		// Second signal should be suppressed
		s.False(s.dedup.ShouldEmit(signal), "second signal within TTL should be suppressed")
		s.Equal(1, s.dedup.Count(), "should still track one incident")
	})
}

func (s *FaultDeduplicatorSuite) TestSignalAfterTTLExpiresPassesThrough() {
	s.Run("signal after TTL expires should be treated as new incident", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Advance time by exactly 15 minutes (TTL boundary)
		s.advanceTime(15 * time.Minute)
		signal.Timestamp = s.currentTime

		// Signal at TTL boundary should emit (incident expired)
		s.True(s.dedup.ShouldEmit(signal), "signal after TTL should be emitted")
		s.Equal(1, s.dedup.Count(), "should still track one incident (reused key)")
	})
}

func (s *FaultDeduplicatorSuite) TestSignalJustBeforeTTLExpiresIsSuppressed() {
	s.Run("signal just before TTL expires should still be suppressed", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Advance time by 14 minutes 59 seconds (just before TTL)
		s.advanceTime(14*time.Minute + 59*time.Second)
		signal.Timestamp = s.currentTime

		// Signal just before TTL should be suppressed
		s.False(s.dedup.ShouldEmit(signal), "signal just before TTL should be suppressed")
	})
}

func (s *FaultDeduplicatorSuite) TestDifferentFaultTypesAreIndependent() {
	s.Run("different fault types for same resource should be independent", func() {
		crashSignal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		crashLoopSignal := FaultSignal{
			FaultType:     FaultTypeCrashLoop,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityWarning,
			Timestamp:     s.currentTime,
		}

		// Both signals should emit (different fault types)
		s.True(s.dedup.ShouldEmit(crashSignal), "crash signal should be emitted")
		s.True(s.dedup.ShouldEmit(crashLoopSignal), "crash loop signal should be emitted")
		s.Equal(2, s.dedup.Count(), "should track two independent incidents")
	})
}

func (s *FaultDeduplicatorSuite) TestDifferentResourcesAreIndependent() {
	s.Run("same fault type for different resources should be independent", func() {
		signal1 := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod-1",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		signal2 := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-456"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod-2",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// Both signals should emit (different resources)
		s.True(s.dedup.ShouldEmit(signal1), "signal for pod-123 should be emitted")
		s.True(s.dedup.ShouldEmit(signal2), "signal for pod-456 should be emitted")
		s.Equal(2, s.dedup.Count(), "should track two independent incidents")
	})
}

func (s *FaultDeduplicatorSuite) TestDifferentContainersAreIndependent() {
	s.Run("same fault type for different containers in same pod should be independent", func() {
		signal1 := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		signal2 := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "sidecar",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// Both signals should emit (different containers)
		s.True(s.dedup.ShouldEmit(signal1), "signal for app container should be emitted")
		s.True(s.dedup.ShouldEmit(signal2), "signal for sidecar container should be emitted")
		s.Equal(2, s.dedup.Count(), "should track two independent incidents")
	})
}

func (s *FaultDeduplicatorSuite) TestMultipleSignalsWithinWindowAreSuppressed() {
	s.Run("multiple signals within TTL window should all be suppressed", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal at T=0 should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Send signals at various absolute times within TTL (15 minutes)
		// All should be suppressed since they're within the window from T=0
		absoluteTimes := []time.Duration{
			1 * time.Minute,  // T=1m
			3 * time.Minute,  // T=3m
			7 * time.Minute,  // T=7m
			14 * time.Minute, // T=14m
		}

		baseTime := s.currentTime
		for _, absTime := range absoluteTimes {
			s.currentTime = baseTime.Add(absTime)
			signal.Timestamp = s.currentTime
			s.False(s.dedup.ShouldEmit(signal), "signal at T=%v should be suppressed", absTime)
		}

		s.Equal(1, s.dedup.Count(), "should still track one incident")
	})
}

func (s *FaultDeduplicatorSuite) TestResetClearsAllIncidents() {
	s.Run("reset should clear all tracked incidents", func() {
		// Create multiple incidents
		for i := 0; i < 5; i++ {
			signal := FaultSignal{
				FaultType:     FaultTypePodCrash,
				ResourceUID:   types.UID("pod-" + string(rune('1'+i))),
				ContainerName: "app",
				Kind:          "Pod",
				Name:          "test-pod",
				Namespace:     "default",
				Severity:      SeverityCritical,
				Timestamp:     s.currentTime,
			}
			s.dedup.ShouldEmit(signal)
		}

		s.Equal(5, s.dedup.Count(), "should track 5 incidents")

		// Reset
		s.dedup.Reset()

		s.Equal(0, s.dedup.Count(), "should track no incidents after reset")
	})
}

func (s *FaultDeduplicatorSuite) TestEmptyContainerName() {
	s.Run("signals with empty container name should be deduplicated correctly", func() {
		signal := FaultSignal{
			FaultType:     FaultTypeNodeUnhealthy,
			ResourceUID:   types.UID("node-123"),
			ContainerName: "", // Empty for node-level faults
			Kind:          "Node",
			Name:          "test-node",
			Severity:      SeverityWarning,
			Timestamp:     s.currentTime,
		}

		// First signal should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Second signal should be suppressed
		s.advanceTime(5 * time.Minute)
		signal.Timestamp = s.currentTime
		s.False(s.dedup.ShouldEmit(signal), "second signal should be suppressed")
	})
}

func (s *FaultDeduplicatorSuite) TestRapidFireSignals() {
	s.Run("rapid-fire signals should be properly deduplicated", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal should emit
		s.True(s.dedup.ShouldEmit(signal), "first signal should be emitted")

		// Send 10 signals in rapid succession (1 second apart)
		emittedCount := 1 // Already emitted first signal
		for i := 0; i < 10; i++ {
			s.advanceTime(1 * time.Second)
			signal.Timestamp = s.currentTime
			if s.dedup.ShouldEmit(signal) {
				emittedCount++
			}
		}

		s.Equal(1, emittedCount, "only first signal should be emitted in rapid fire")
		s.Equal(1, s.dedup.Count(), "should track one incident")
	})
}

func (s *FaultDeduplicatorSuite) TestConcurrentAccess() {
	s.Run("concurrent access should be thread-safe", func() {
		// Create a fresh deduplicator with real time for this test
		dedup := NewFaultDeduplicatorWithTTL(15 * time.Minute)

		const numGoroutines = 10
		const numSignalsPerGoroutine = 100

		done := make(chan bool, numGoroutines)

		// Launch multiple goroutines that emit signals concurrently
		for g := 0; g < numGoroutines; g++ {
			goroutineID := g
			go func() {
				for i := 0; i < numSignalsPerGoroutine; i++ {
					signal := FaultSignal{
						FaultType:     FaultTypePodCrash,
						ResourceUID:   types.UID("pod-" + string(rune('0'+goroutineID))),
						ContainerName: "app",
						Kind:          "Pod",
						Name:          "test-pod",
						Namespace:     "default",
						Severity:      SeverityCritical,
						Timestamp:     time.Now(),
					}
					dedup.ShouldEmit(signal)
				}
				done <- true
			}()
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// Each goroutine should have created one incident (first signal emitted, rest deduplicated)
		s.Equal(numGoroutines, dedup.Count(), "should track one incident per goroutine")
	})
}

func (s *FaultDeduplicatorSuite) TestTTLBoundaryConditions() {
	s.Run("test TTL boundary conditions precisely", func() {
		signal := FaultSignal{
			FaultType:     FaultTypePodCrash,
			ResourceUID:   types.UID("pod-123"),
			ContainerName: "app",
			Kind:          "Pod",
			Name:          "test-pod",
			Namespace:     "default",
			Severity:      SeverityCritical,
			Timestamp:     s.currentTime,
		}

		// First signal at T=0
		s.True(s.dedup.ShouldEmit(signal), "signal at T=0 should be emitted")

		// Test at T=TTL-1ns (just before expiry)
		s.advanceTime(15*time.Minute - 1*time.Nanosecond)
		signal.Timestamp = s.currentTime
		s.False(s.dedup.ShouldEmit(signal), "signal at T=TTL-1ns should be suppressed")

		// Test at T=TTL (exact expiry)
		s.advanceTime(1 * time.Nanosecond) // Now at exact TTL
		signal.Timestamp = s.currentTime
		s.True(s.dedup.ShouldEmit(signal), "signal at T=TTL should be emitted")

		// Test at T=TTL+1ns (just after expiry)
		s.advanceTime(1 * time.Nanosecond)
		signal.Timestamp = s.currentTime
		s.False(s.dedup.ShouldEmit(signal), "signal at T=TTL+1ns should be suppressed (new window started)")
	})
}

func TestFaultDeduplicator(t *testing.T) {
	suite.Run(t, new(FaultDeduplicatorSuite))
}
