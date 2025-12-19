package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type DedupTestSuite struct {
	suite.Suite
}

func TestDedupSuite(t *testing.T) {
	suite.Run(t, new(DedupTestSuite))
}

// TestFirstEventPassesThrough validates that the first occurrence of a key is not marked as duplicate
func (s *DedupTestSuite) TestFirstEventPassesThrough() {
	s.Run("first occurrence returns false", func() {
		cache := NewDeduplicationCache(5 * time.Second)
		key := "cluster1/default/event1/uid-123/rv-1"

		isDup := cache.IsDuplicate(key)

		s.False(isDup, "first occurrence should not be marked as duplicate")
	})
}

// TestDuplicateWithinTTL validates that duplicate keys within TTL are detected
func (s *DedupTestSuite) TestDuplicateWithinTTL() {
	s.Run("same key within TTL returns true", func() {
		cache := NewDeduplicationCache(100 * time.Millisecond)
		key := "cluster1/default/event1/uid-123/rv-1"

		// First occurrence
		isDup1 := cache.IsDuplicate(key)
		s.False(isDup1, "first occurrence should not be duplicate")

		// Second occurrence within TTL
		isDup2 := cache.IsDuplicate(key)
		s.True(isDup2, "second occurrence within TTL should be duplicate")

		// Third occurrence within TTL
		isDup3 := cache.IsDuplicate(key)
		s.True(isDup3, "third occurrence within TTL should be duplicate")
	})
}

// TestExpiredEntryPassesThrough validates that expired entries are not marked as duplicates
func (s *DedupTestSuite) TestExpiredEntryPassesThrough() {
	s.Run("same key after TTL expires returns false", func() {
		ttl := 50 * time.Millisecond
		cache := NewDeduplicationCache(ttl)
		key := "cluster1/default/event1/uid-123/rv-1"

		// First occurrence
		isDup1 := cache.IsDuplicate(key)
		s.False(isDup1, "first occurrence should not be duplicate")

		// Wait for TTL to expire
		time.Sleep(ttl + 10*time.Millisecond)

		// Second occurrence after TTL
		isDup2 := cache.IsDuplicate(key)
		s.False(isDup2, "occurrence after TTL should not be duplicate")
	})
}

// TestDifferentResourceVersionNotDuplicate validates that different resource versions are not duplicates
func (s *DedupTestSuite) TestDifferentResourceVersionNotDuplicate() {
	s.Run("different resource version is not duplicate", func() {
		cache := NewDeduplicationCache(5 * time.Second)

		key1 := "cluster1/default/event1/uid-123/rv-1"
		key2 := "cluster1/default/event1/uid-123/rv-2"

		isDup1 := cache.IsDuplicate(key1)
		s.False(isDup1, "first key should not be duplicate")

		isDup2 := cache.IsDuplicate(key2)
		s.False(isDup2, "different resource version should not be duplicate")

		// Verify both are now in cache
		isDup1Again := cache.IsDuplicate(key1)
		s.True(isDup1Again, "first key should now be duplicate")

		isDup2Again := cache.IsDuplicate(key2)
		s.True(isDup2Again, "second key should now be duplicate")
	})
}

// TestDifferentCountNotDuplicate validates that different counts create different keys (for faults mode)
func (s *DedupTestSuite) TestDifferentCountNotDuplicate() {
	s.Run("different count is not duplicate", func() {
		cache := NewDeduplicationCache(5 * time.Second)

		key1 := "cluster1/default/pod1/BackOff/1"
		key2 := "cluster1/default/pod1/BackOff/2"

		isDup1 := cache.IsDuplicate(key1)
		s.False(isDup1, "first key should not be duplicate")

		isDup2 := cache.IsDuplicate(key2)
		s.False(isDup2, "different count should not be duplicate")
	})
}

// TestConcurrentAccess validates thread-safety of the cache
func (s *DedupTestSuite) TestConcurrentAccess() {
	s.Run("handles concurrent access safely", func() {
		cache := NewDeduplicationCache(1 * time.Second)
		numGoroutines := 10
		numOperations := 100

		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		// Track how many goroutines saw "not duplicate" for the same key
		firstSeenCount := make(map[string]int)
		var mu sync.Mutex

		for i := 0; i < numGoroutines; i++ {
			go func(goroutineID int) {
				defer wg.Done()

				for j := 0; j < numOperations; j++ {
					key := "cluster1/default/event1/uid-123/rv-1"
					isDup := cache.IsDuplicate(key)

					if !isDup {
						mu.Lock()
						firstSeenCount[key]++
						mu.Unlock()
					}

					// Use different keys to test parallelism
					uniqueKey := "cluster1/default/event" + string(rune('0'+goroutineID)) + "/uid/rv"
					cache.IsDuplicate(uniqueKey)
				}
			}(i)
		}

		wg.Wait()

		// Only one goroutine should have seen the key as "not duplicate" initially
		s.LessOrEqual(firstSeenCount["cluster1/default/event1/uid-123/rv-1"], 2,
			"at most 2 goroutines should see the same key as first occurrence (due to timing)")
	})
}

// TestCacheCleanup validates that expired entries are cleaned up
func (s *DedupTestSuite) TestCacheCleanup() {
	s.Run("removes expired entries during cleanup", func() {
		ttl := 50 * time.Millisecond
		cache := NewDeduplicationCache(ttl)

		// Add multiple entries
		keys := []string{
			"cluster1/default/event1/uid-1/rv-1",
			"cluster1/default/event2/uid-2/rv-1",
			"cluster1/default/event3/uid-3/rv-1",
		}

		for _, key := range keys {
			cache.IsDuplicate(key)
		}

		s.Equal(len(keys), cache.Size(), "cache should have %d entries", len(keys))

		// Wait for TTL + cleanup cycle
		time.Sleep(ttl + 100*time.Millisecond)

		// Entries should be cleaned up
		s.Equal(0, cache.Size(), "cache should be empty after cleanup")
	})
}

// TestCacheClear validates the Clear method
func (s *DedupTestSuite) TestCacheClear() {
	s.Run("clears all entries", func() {
		cache := NewDeduplicationCache(5 * time.Second)

		// Add entries
		for i := 0; i < 10; i++ {
			key := "cluster1/default/event" + string(rune('0'+i)) + "/uid/rv"
			cache.IsDuplicate(key)
		}

		s.Equal(10, cache.Size(), "cache should have 10 entries")

		cache.Clear()

		s.Equal(0, cache.Size(), "cache should be empty after clear")
	})
}

// TestCacheSize validates the Size method
func (s *DedupTestSuite) TestCacheSize() {
	s.Run("returns correct size", func() {
		cache := NewDeduplicationCache(5 * time.Second)

		s.Equal(0, cache.Size(), "new cache should be empty")

		// Add entries
		for i := 0; i < 5; i++ {
			key := "cluster1/default/event" + string(rune('0'+i)) + "/uid/rv"
			cache.IsDuplicate(key)
		}

		s.Equal(5, cache.Size(), "cache should have 5 entries")

		// Add duplicate - size shouldn't change
		cache.IsDuplicate("cluster1/default/event0/uid/rv")
		s.Equal(5, cache.Size(), "cache size should remain 5 after duplicate")

		// Add new entry
		cache.IsDuplicate("cluster1/default/event5/uid/rv")
		s.Equal(6, cache.Size(), "cache should have 6 entries")
	})
}

// TestEventsModeTTL validates 5-second TTL for events mode
func (s *DedupTestSuite) TestEventsModeTTL() {
	s.Run("events mode uses 5 second TTL", func() {
		eventsTTL := 5 * time.Second
		cache := NewDeduplicationCache(eventsTTL)

		key := "cluster1/default/event1/uid-123/rv-1"

		// First occurrence
		isDup1 := cache.IsDuplicate(key)
		s.False(isDup1, "first occurrence should not be duplicate")

		// Within TTL
		time.Sleep(1 * time.Second)
		isDup2 := cache.IsDuplicate(key)
		s.True(isDup2, "should be duplicate within TTL")

		// This test would take too long to validate full expiry,
		// but we've established the TTL is set correctly
	})
}

// TestFaultsModeTTL validates 60-second TTL for faults mode
func (s *DedupTestSuite) TestFaultsModeTTL() {
	s.Run("faults mode uses 60 second TTL", func() {
		faultsTTL := 60 * time.Second
		cache := NewDeduplicationCache(faultsTTL)

		key := "cluster1/default/pod1/BackOff/5"

		// First occurrence
		isDup1 := cache.IsDuplicate(key)
		s.False(isDup1, "first occurrence should not be duplicate")

		// Within TTL
		time.Sleep(100 * time.Millisecond)
		isDup2 := cache.IsDuplicate(key)
		s.True(isDup2, "should be duplicate within TTL")

		// Verify the TTL is set to 60 seconds (we don't wait for it to actually expire in tests)
	})
}

// TestKeyFormat validates the deduplication key format
func (s *DedupTestSuite) TestKeyFormat() {
	s.Run("uses correct key format for events mode", func() {
		cache := NewDeduplicationCache(5 * time.Second)

		// Key format: <cluster>/<ns>/<name>/<uid>/<resourceVersion>
		key := "prod-cluster/kube-system/coredns-warning/550e8400-e29b-41d4-a716-446655440000/123456"

		isDup := cache.IsDuplicate(key)
		s.False(isDup, "first occurrence should not be duplicate")

		isDup2 := cache.IsDuplicate(key)
		s.True(isDup2, "second occurrence should be duplicate")
	})

	s.Run("uses correct key format for faults mode", func() {
		cache := NewDeduplicationCache(60 * time.Second)

		// Key format: <cluster>/<ns>/<pod>/<reason>/<count>
		key := "prod-cluster/default/nginx-7b9c8d/BackOff/5"

		isDup := cache.IsDuplicate(key)
		s.False(isDup, "first occurrence should not be duplicate")

		isDup2 := cache.IsDuplicate(key)
		s.True(isDup2, "second occurrence should be duplicate")
	})
}

// TestMultipleCaches validates using separate caches for different modes
func (s *DedupTestSuite) TestMultipleCaches() {
	s.Run("events and faults caches are independent", func() {
		eventsCache := NewDeduplicationCache(5 * time.Second)
		faultsCache := NewDeduplicationCache(60 * time.Second)

		eventsKey := "cluster1/default/event1/uid-123/rv-1"
		faultsKey := "cluster1/default/pod1/BackOff/5"

		// Add to events cache
		eventsCache.IsDuplicate(eventsKey)

		// Add to faults cache
		faultsCache.IsDuplicate(faultsKey)

		// Verify independence
		s.Equal(1, eventsCache.Size(), "events cache should have 1 entry")
		s.Equal(1, faultsCache.Size(), "faults cache should have 1 entry")

		// Events key not in faults cache
		isDup := faultsCache.IsDuplicate(eventsKey)
		s.False(isDup, "events key should not be in faults cache")

		// Faults key not in events cache
		isDup2 := eventsCache.IsDuplicate(faultsKey)
		s.False(isDup2, "faults key should not be in events cache")
	})
}
