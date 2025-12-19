package events

import (
	"sync"
	"time"
)

// DeduplicationCache implements a TTL-based cache for event deduplication
type DeduplicationCache struct {
	mu      sync.RWMutex
	entries map[string]*dedupEntry
	ttl     time.Duration
}

// dedupEntry tracks when an event key was last seen
type dedupEntry struct {
	key       string
	expiresAt time.Time
}

// NewDeduplicationCache creates a new deduplication cache with the given TTL
func NewDeduplicationCache(ttl time.Duration) *DeduplicationCache {
	cache := &DeduplicationCache{
		entries: make(map[string]*dedupEntry),
		ttl:     ttl,
	}

	// Start background cleanup goroutine
	go cache.cleanupLoop()

	return cache
}

// IsDuplicate checks if a key has been seen within the TTL window
// If not seen or expired, marks the key as seen and returns false
// If seen within TTL, returns true
func (c *DeduplicationCache) IsDuplicate(key string) bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if key exists and is not expired
	if entry, exists := c.entries[key]; exists {
		if now.Before(entry.expiresAt) {
			// Key exists and not expired - this is a duplicate
			return true
		}
		// Key expired, remove it
		delete(c.entries, key)
	}

	// Key doesn't exist or was expired - mark as seen
	c.entries[key] = &dedupEntry{
		key:       key,
		expiresAt: now.Add(c.ttl),
	}

	return false
}

// cleanupLoop periodically removes expired entries
func (c *DeduplicationCache) cleanupLoop() {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

// cleanup removes all expired entries from the cache
func (c *DeduplicationCache) cleanup() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
}

// Clear removes all entries from the cache
func (c *DeduplicationCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*dedupEntry)
}

// Size returns the current number of entries in the cache
func (c *DeduplicationCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.entries)
}
