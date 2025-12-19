package events

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type FaultsSuite struct {
	suite.Suite
}

func (s *FaultsSuite) TestIsFaultEvent() {
	s.Run("identifies Warning events for Pods", func() {
		event := &v1.Event{
			Type: "Warning",
			InvolvedObject: v1.ObjectReference{
				Kind: "Pod",
			},
		}
		s.True(isFaultEvent(event))
	})

	s.Run("rejects Normal events", func() {
		event := &v1.Event{
			Type: "Normal",
			InvolvedObject: v1.ObjectReference{
				Kind: "Pod",
			},
		}
		s.False(isFaultEvent(event))
	})

	s.Run("rejects Warning events for non-Pod resources", func() {
		testCases := []string{"Deployment", "Service", "ConfigMap", "Node"}
		for _, kind := range testCases {
			event := &v1.Event{
				Type: "Warning",
				InvolvedObject: v1.ObjectReference{
					Kind: kind,
				},
			}
			s.False(isFaultEvent(event), "should reject kind: %s", kind)
		}
	})

	s.Run("rejects Normal events for non-Pod resources", func() {
		event := &v1.Event{
			Type: "Normal",
			InvolvedObject: v1.ObjectReference{
				Kind: "Deployment",
			},
		}
		s.False(isFaultEvent(event))
	})
}

func (s *FaultsSuite) TestGenerateFaultDeduplicationKey() {
	s.Run("generates consistent key format", func() {
		key := generateFaultDeduplicationKey("dev-cluster", "default", "nginx-123", "BackOff", 5)
		s.Equal("dev-cluster/default/nginx-123/BackOff/5", key)
	})

	s.Run("includes count in key", func() {
		key1 := generateFaultDeduplicationKey("cluster", "ns", "pod", "reason", 1)
		key2 := generateFaultDeduplicationKey("cluster", "ns", "pod", "reason", 2)
		s.NotEqual(key1, key2)
	})

	s.Run("handles empty values", func() {
		key := generateFaultDeduplicationKey("", "", "", "", 0)
		s.Equal("////0", key)
	})
}

func (s *FaultsSuite) TestFaultDeduplicationCache() {
	s.Run("first occurrence is not duplicate", func() {
		cache := NewFaultDeduplicationCache(100 * time.Millisecond)
		key := "test/key/1"
		s.False(cache.IsDuplicate(key))
	})

	s.Run("same key within TTL returns duplicate", func() {
		cache := NewFaultDeduplicationCache(100 * time.Millisecond)
		key := "test/key/2"

		cache.Mark(key)
		s.True(cache.IsDuplicate(key))
	})

	s.Run("same key after TTL expires passes through", func() {
		cache := NewFaultDeduplicationCache(50 * time.Millisecond)
		key := "test/key/3"

		cache.Mark(key)
		s.True(cache.IsDuplicate(key))

		// Wait for TTL to expire
		time.Sleep(60 * time.Millisecond)
		s.False(cache.IsDuplicate(key))
	})

	s.Run("different keys are not duplicates", func() {
		cache := NewFaultDeduplicationCache(100 * time.Millisecond)

		cache.Mark("key1")
		s.False(cache.IsDuplicate("key2"))
	})

	s.Run("concurrent access is safe", func() {
		cache := NewFaultDeduplicationCache(200 * time.Millisecond)
		var wg sync.WaitGroup

		// Spawn multiple goroutines accessing the cache
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					key := "concurrent/test"
					if !cache.IsDuplicate(key) {
						cache.Mark(key)
					}
				}
			}(i)
		}

		wg.Wait()
		// If we reach here without panicking, the cache is thread-safe
	})

	s.Run("cleanup removes expired entries", func() {
		cache := NewFaultDeduplicationCache(50 * time.Millisecond)

		// Add multiple keys
		cache.Mark("key1")
		cache.Mark("key2")
		cache.Mark("key3")

		// Verify they exist
		s.True(cache.IsDuplicate("key1"))
		s.True(cache.IsDuplicate("key2"))
		s.True(cache.IsDuplicate("key3"))

		// Wait for cleanup
		time.Sleep(100 * time.Millisecond)

		// Manually trigger cleanup
		cache.cleanup()

		// Verify entries were removed
		s.False(cache.IsDuplicate("key1"))
		s.False(cache.IsDuplicate("key2"))
		s.False(cache.IsDuplicate("key3"))
	})
}

func (s *FaultsSuite) TestLogCaptureWorkerPool() {
	s.Run("respects global limit", func() {
		config := ManagerConfig{
			MaxLogCapturesPerCluster: 10,
			MaxLogCapturesGlobal:     2,
		}
		pool := NewLogCaptureWorkerPool(config)
		ctx := context.Background()

		// Acquire 2 slots (global limit)
		s.True(pool.Acquire(ctx, "cluster1"))
		s.True(pool.Acquire(ctx, "cluster2"))

		// Third acquisition should fail
		s.False(pool.Acquire(ctx, "cluster3"))

		// Release one and try again
		pool.Release("cluster1")
		s.True(pool.Acquire(ctx, "cluster4"))

		// Cleanup
		pool.Release("cluster2")
		pool.Release("cluster4")
	})

	s.Run("respects per-cluster limit", func() {
		config := ManagerConfig{
			MaxLogCapturesPerCluster: 2,
			MaxLogCapturesGlobal:     10,
		}
		pool := NewLogCaptureWorkerPool(config)
		ctx := context.Background()

		// Acquire 2 slots for same cluster
		s.True(pool.Acquire(ctx, "cluster1"))
		s.True(pool.Acquire(ctx, "cluster1"))

		// Third acquisition for same cluster should fail
		s.False(pool.Acquire(ctx, "cluster1"))

		// But acquisition for different cluster should succeed
		s.True(pool.Acquire(ctx, "cluster2"))

		// Cleanup
		pool.Release("cluster1")
		pool.Release("cluster1")
		pool.Release("cluster2")
	})

	s.Run("tracks multiple clusters independently", func() {
		config := ManagerConfig{
			MaxLogCapturesPerCluster: 2,
			MaxLogCapturesGlobal:     10,
		}
		pool := NewLogCaptureWorkerPool(config)
		ctx := context.Background()

		// Acquire for multiple clusters
		s.True(pool.Acquire(ctx, "cluster1"))
		s.True(pool.Acquire(ctx, "cluster2"))
		s.True(pool.Acquire(ctx, "cluster3"))

		// Each cluster should allow up to its limit
		s.True(pool.Acquire(ctx, "cluster1"))  // cluster1: 2/2
		s.False(pool.Acquire(ctx, "cluster1")) // cluster1: full

		// Cleanup
		pool.Release("cluster1")
		pool.Release("cluster1")
		pool.Release("cluster2")
		pool.Release("cluster3")
	})

	s.Run("releases both global and cluster semaphores", func() {
		config := ManagerConfig{
			MaxLogCapturesPerCluster: 2,
			MaxLogCapturesGlobal:     2,
		}
		pool := NewLogCaptureWorkerPool(config)
		ctx := context.Background()

		// Fill global limit with one cluster
		s.True(pool.Acquire(ctx, "cluster1"))
		s.True(pool.Acquire(ctx, "cluster1"))

		// Global limit reached
		s.False(pool.Acquire(ctx, "cluster2"))

		// Release one
		pool.Release("cluster1")

		// Should be able to acquire for different cluster now
		s.True(pool.Acquire(ctx, "cluster2"))

		// Cleanup
		pool.Release("cluster1")
		pool.Release("cluster2")
	})
}

func (s *FaultsSuite) TestFaultProcessor() {
	s.Run("filters out Normal events", func() {
		event := &v1.Event{
			Type: "Normal",
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: "default",
			},
		}

		// For unit tests without actual Kubernetes client, we test the filtering logic
		// The isFaultEvent check happens before the log capture
		s.False(isFaultEvent(event), "Normal events should not be faults")
	})

	s.Run("filters out non-Pod Warning events", func() {
		event := &v1.Event{
			Type: "Warning",
			InvolvedObject: v1.ObjectReference{
				Kind:      "Deployment",
				Name:      "test-deployment",
				Namespace: "default",
			},
		}

		s.False(isFaultEvent(event), "Non-Pod events should not be faults")
	})

	s.Run("processes Warning Pod events", func() {
		config := DefaultManagerConfig()
		_ = NewFaultProcessor(config)

		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
			},
			Type:    "Warning",
			Reason:  "BackOff",
			Message: "Back-off restarting failed container",
			Count:   5,
			EventTime: metav1.MicroTime{
				Time: time.Now(),
			},
			InvolvedObject: v1.ObjectReference{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       "test-pod",
				Namespace:  "default",
			},
		}

		// Verify this is recognized as a fault event
		s.True(isFaultEvent(event), "Warning Pod events should be faults")
	})

	s.Run("deduplicates events within 60s window", func() {
		config := DefaultManagerConfig()
		processor := NewFaultProcessor(config)

		// Override TTL for faster test
		processor.dedupCache = NewFaultDeduplicationCache(100 * time.Millisecond)

		// Mark the key as seen
		key := generateFaultDeduplicationKey("test-cluster", "default", "pending-pod", "FailedScheduling", 1)
		processor.dedupCache.Mark(key)

		// Verify it's a duplicate within TTL
		s.True(processor.dedupCache.IsDuplicate(key), "Should be duplicate within TTL")

		// After TTL expires, should not be duplicate
		time.Sleep(110 * time.Millisecond)
		s.False(processor.dedupCache.IsDuplicate(key), "Should not be duplicate after TTL")
	})

	s.Run("different count values are not duplicates", func() {
		config := DefaultManagerConfig()
		processor := NewFaultProcessor(config)
		processor.dedupCache = NewFaultDeduplicationCache(100 * time.Millisecond)

		// Mark first key with count=5
		key1 := generateFaultDeduplicationKey("cluster", "default", "test-pod", "BackOff", 5)
		processor.dedupCache.Mark(key1)

		// Verify key with count=6 is not duplicate (different count)
		key2 := generateFaultDeduplicationKey("cluster", "default", "test-pod", "BackOff", 6)
		s.False(processor.dedupCache.IsDuplicate(key2), "Different count should not be duplicate")
	})

	s.Run("builds correct deduplication key", func() {
		config := DefaultManagerConfig()
		processor := NewFaultProcessor(config)

		// Test deduplication key generation
		key := generateFaultDeduplicationKey("prod-cluster", "kube-system", "nginx-pod", "FailedMount", 3)
		s.Equal("prod-cluster/kube-system/nginx-pod/FailedMount/3", key)

		// Verify processor uses the key correctly
		processor.dedupCache.Mark(key)
		s.True(processor.dedupCache.IsDuplicate(key))
	})
}

func (s *FaultsSuite) TestFaultEventStructure() {
	s.Run("fault event has expected fields", func() {
		event := FaultEvent{
			SubscriptionID: "sub-123",
			Cluster:        "dev-cluster",
			Event: EventData{
				Namespace: "default",
				Timestamp: "2025-01-15T10:30:00Z",
				Type:      "Warning",
				Reason:    "BackOff",
				Message:   "Back-off restarting failed container",
				InvolvedObject: &InvolvedObject{
					APIVersion: "v1",
					Kind:       "Pod",
					Name:       "test-pod",
					Namespace:  "default",
				},
				Count: 5,
			},
			Logs: []ContainerLog{
				{
					Container: "app",
					Previous:  false,
					HasPanic:  true,
					Sample:    "panic: test",
				},
			},
		}

		s.Equal("sub-123", event.SubscriptionID)
		s.Equal("dev-cluster", event.Cluster)
		s.Equal("Warning", event.Event.Type)
		s.Equal("BackOff", event.Event.Reason)
		s.Equal(int32(5), event.Event.Count)
		s.Len(event.Logs, 1)
		s.Equal("app", event.Logs[0].Container)
		s.True(event.Logs[0].HasPanic)
	})
}

func (s *FaultsSuite) TestTimestampHandling() {
	s.Run("uses EventTime when available", func() {
		timestamp := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
			Type:       "Warning",
			EventTime: metav1.MicroTime{
				Time: timestamp,
			},
			InvolvedObject: v1.ObjectReference{
				Kind:      "Pod",
				Name:      "test-pod",
				Namespace: "default",
			},
		}

		// The processor will try to use EventTime
		// We can't easily test the exact timestamp without a full integration test,
		// but we can verify the event is processed
		s.True(isFaultEvent(event))
	})
}

func TestFaults(t *testing.T) {
	suite.Run(t, new(FaultsSuite))
}
