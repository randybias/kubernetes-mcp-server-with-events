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

// incidentKey uniquely identifies a fault incident by its type, resource, and container.
// This key is used to track and deduplicate recurring signals from the same incident.
type incidentKey struct {
	FaultType     FaultType
	ResourceUID   types.UID
	ContainerName string
}

// incidentRecord tracks when a fault signal was last emitted for a specific incident.
type incidentRecord struct {
	LastEmitted time.Time
}

// FaultDeduplicator prevents notification storms by suppressing duplicate fault signals
// within a configurable time window. It tracks "active incidents" and only emits signals
// when they represent new incidents or when the deduplication window has expired.
//
// Thread-safe for concurrent use.
type FaultDeduplicator struct {
	mu        sync.RWMutex
	incidents map[incidentKey]*incidentRecord
	ttl       time.Duration
	now       func() time.Time // allows time injection for testing
}

// NewFaultDeduplicator creates a new FaultDeduplicator with the default TTL.
func NewFaultDeduplicator() *FaultDeduplicator {
	return &FaultDeduplicator{
		incidents: make(map[incidentKey]*incidentRecord),
		ttl:       DeduplicationTTL,
		now:       time.Now,
	}
}

// NewFaultDeduplicatorWithTTL creates a new FaultDeduplicator with a custom TTL.
// This is useful for testing or adjusting the deduplication window.
func NewFaultDeduplicatorWithTTL(ttl time.Duration) *FaultDeduplicator {
	return &FaultDeduplicator{
		incidents: make(map[incidentKey]*incidentRecord),
		ttl:       ttl,
		now:       time.Now,
	}
}

// ShouldEmit determines whether a fault signal should be emitted based on deduplication logic.
// It returns true if:
//   - This is the first signal for this incident (new incident)
//   - The previous signal for this incident was emitted more than TTL ago (incident has expired)
//
// It returns false if:
//   - A signal for this incident was emitted within the TTL window (duplicate suppressed)
//
// When ShouldEmit returns true, it automatically records the emission timestamp
// for future deduplication checks.
func (d *FaultDeduplicator) ShouldEmit(signal FaultSignal) bool {
	key := incidentKey{
		FaultType:     signal.FaultType,
		ResourceUID:   signal.ResourceUID,
		ContainerName: signal.ContainerName,
	}

	currentTime := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	record, exists := d.incidents[key]

	// First signal for this incident - emit it
	if !exists {
		d.incidents[key] = &incidentRecord{
			LastEmitted: currentTime,
		}
		return true
	}

	// Check if the incident has expired (TTL elapsed)
	if currentTime.Sub(record.LastEmitted) >= d.ttl {
		// Incident has expired - treat as new incident
		record.LastEmitted = currentTime
		return true
	}

	// Within TTL window - suppress duplicate
	return false
}

// Reset clears all tracked incidents. This is primarily useful for testing.
func (d *FaultDeduplicator) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.incidents = make(map[incidentKey]*incidentRecord)
}

// Count returns the number of currently tracked incidents. This is primarily useful for testing.
func (d *FaultDeduplicator) Count() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.incidents)
}
