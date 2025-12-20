package events

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// DeduplicationTTL is the time window during which duplicate fault signals
// for the same incident are suppressed. After this duration, a new signal
// for the same resource/fault combination is treated as a new incident.
const DeduplicationTTL = 15 * time.Minute

// faultConditionKey uniquely identifies a fault condition by its type, resource, and container.
// This key is used to track and deduplicate recurring signals from the same fault condition.
type faultConditionKey struct {
	FaultType     FaultType
	ResourceUID   types.UID
	ContainerName string
}

// faultEmissionRecord tracks when a fault signal was last emitted for a specific fault condition.
type faultEmissionRecord struct {
	LastEmitted time.Time
}

// FaultDeduplicator prevents notification storms by suppressing duplicate fault signals
// within a configurable time window. It tracks active fault conditions and only emits signals
// when they represent new fault conditions or when the deduplication window has expired.
//
// Thread-safe for concurrent use.
type FaultDeduplicator struct {
	mu     sync.RWMutex
	faults map[faultConditionKey]*faultEmissionRecord
	ttl    time.Duration
	now    func() time.Time // allows time injection for testing
}

// NewFaultDeduplicator creates a new FaultDeduplicator with the default TTL.
func NewFaultDeduplicator() *FaultDeduplicator {
	return &FaultDeduplicator{
		faults: make(map[faultConditionKey]*faultEmissionRecord),
		ttl:    DeduplicationTTL,
		now:    time.Now,
	}
}

// NewFaultDeduplicatorWithTTL creates a new FaultDeduplicator with a custom TTL.
// This is useful for testing or adjusting the deduplication window.
func NewFaultDeduplicatorWithTTL(ttl time.Duration) *FaultDeduplicator {
	return &FaultDeduplicator{
		faults: make(map[faultConditionKey]*faultEmissionRecord),
		ttl:    ttl,
		now:    time.Now,
	}
}

// ShouldEmit determines whether a fault signal should be emitted based on deduplication logic.
// It returns true if:
//   - This is the first signal for this fault condition (new fault)
//   - The previous signal for this fault condition was emitted more than TTL ago (fault has expired)
//
// It returns false if:
//   - A signal for this fault condition was emitted within the TTL window (duplicate suppressed)
//
// When ShouldEmit returns true, it automatically records the emission timestamp
// for future deduplication checks.
func (d *FaultDeduplicator) ShouldEmit(signal FaultSignal) bool {
	key := faultConditionKey{
		FaultType:     signal.FaultType,
		ResourceUID:   signal.ResourceUID,
		ContainerName: signal.ContainerName,
	}

	currentTime := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	record, exists := d.faults[key]

	// First signal for this fault condition - emit it
	if !exists {
		d.faults[key] = &faultEmissionRecord{
			LastEmitted: currentTime,
		}
		return true
	}

	// Check if the fault condition has expired (TTL elapsed)
	if currentTime.Sub(record.LastEmitted) >= d.ttl {
		// Fault condition has expired - treat as new emission
		record.LastEmitted = currentTime
		return true
	}

	// Within TTL window - suppress duplicate
	return false
}

// Reset clears all tracked fault conditions. This is primarily useful for testing.
func (d *FaultDeduplicator) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults = make(map[faultConditionKey]*faultEmissionRecord)
}

// Count returns the number of currently tracked fault conditions. This is primarily useful for testing.
func (d *FaultDeduplicator) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.faults)
}
