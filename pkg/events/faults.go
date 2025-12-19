package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
	v1 "k8s.io/api/core/v1"

	"github.com/containers/kubernetes-mcp-server/pkg/kubernetes"
)

const (
	// FaultDeduplicationWindow is the time window for deduplicating fault events
	FaultDeduplicationWindow = 60 * time.Second
)

// FaultEvent represents a Warning event targeting a Pod with enriched log data
type FaultEvent struct {
	SubscriptionID string         `json:"subscriptionId"`
	Cluster        string         `json:"cluster"`
	Event          EventData      `json:"event"`
	Logs           []ContainerLog `json:"logs,omitempty"`
}

// EventData represents the core event information
type EventData struct {
	Namespace      string            `json:"namespace"`
	Timestamp      string            `json:"timestamp"`
	Type           string            `json:"type"`
	Reason         string            `json:"reason"`
	Message        string            `json:"message"`
	Labels         map[string]string `json:"labels,omitempty"`
	InvolvedObject *InvolvedObject   `json:"involvedObject"`
	Count          int32             `json:"count"`
}

// isFaultEvent checks if an event qualifies as a fault (Warning type, Pod target)
func isFaultEvent(event *v1.Event) bool {
	return event.Type == "Warning" && event.InvolvedObject.Kind == "Pod"
}

// generateFaultDeduplicationKey creates a deduplication key for fault events
// Key format: <cluster>/<namespace>/<pod>/<reason>/<count>
func generateFaultDeduplicationKey(cluster, namespace, podName, reason string, count int32) string {
	return fmt.Sprintf("%s/%s/%s/%s/%d", cluster, namespace, podName, reason, count)
}

// FaultDeduplicationCache manages deduplication of fault events
type FaultDeduplicationCache struct {
	mu      sync.RWMutex
	entries map[string]time.Time
	ttl     time.Duration
}

// NewFaultDeduplicationCache creates a new deduplication cache
func NewFaultDeduplicationCache(ttl time.Duration) *FaultDeduplicationCache {
	cache := &FaultDeduplicationCache{
		entries: make(map[string]time.Time),
		ttl:     ttl,
	}
	// Start cleanup goroutine
	go cache.cleanupLoop()
	return cache
}

// IsDuplicate checks if a key has been seen within the TTL window
func (c *FaultDeduplicationCache) IsDuplicate(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	lastSeen, exists := c.entries[key]
	if !exists {
		return false
	}

	return time.Since(lastSeen) < c.ttl
}

// Mark records a key as seen with the current timestamp
func (c *FaultDeduplicationCache) Mark(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = time.Now()
}

// cleanupLoop periodically removes expired entries
func (c *FaultDeduplicationCache) cleanupLoop() {
	ticker := time.NewTicker(c.ttl)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

// cleanup removes expired entries from the cache
func (c *FaultDeduplicationCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, timestamp := range c.entries {
		if now.Sub(timestamp) >= c.ttl {
			delete(c.entries, key)
		}
	}
}

// LogCaptureWorkerPool manages concurrent log capture operations
type LogCaptureWorkerPool struct {
	perCluster map[string]*semaphore.Weighted
	global     *semaphore.Weighted
	mu         sync.RWMutex
	config     ManagerConfig
}

// NewLogCaptureWorkerPool creates a new worker pool with concurrency limits
func NewLogCaptureWorkerPool(config ManagerConfig) *LogCaptureWorkerPool {
	return &LogCaptureWorkerPool{
		perCluster: make(map[string]*semaphore.Weighted),
		global:     semaphore.NewWeighted(int64(config.MaxLogCapturesGlobal)),
		config:     config,
	}
}

// Acquire attempts to acquire a slot for log capture (both per-cluster and global)
// Returns true if acquired, false if limits are reached
func (p *LogCaptureWorkerPool) Acquire(ctx context.Context, cluster string) bool {
	// Try to acquire global semaphore first
	if !p.global.TryAcquire(1) {
		return false
	}

	// Get or create per-cluster semaphore
	p.mu.Lock()
	clusterSem, exists := p.perCluster[cluster]
	if !exists {
		clusterSem = semaphore.NewWeighted(int64(p.config.MaxLogCapturesPerCluster))
		p.perCluster[cluster] = clusterSem
	}
	p.mu.Unlock()

	// Try to acquire cluster semaphore
	if !clusterSem.TryAcquire(1) {
		// Release global if cluster limit reached
		p.global.Release(1)
		return false
	}

	return true
}

// Release releases both global and per-cluster semaphore slots
func (p *LogCaptureWorkerPool) Release(cluster string) {
	p.mu.RLock()
	clusterSem, exists := p.perCluster[cluster]
	p.mu.RUnlock()

	if exists {
		clusterSem.Release(1)
	}
	p.global.Release(1)
}

// FaultProcessor handles fault event processing with log enrichment
type FaultProcessor struct {
	dedupCache *FaultDeduplicationCache
	workerPool *LogCaptureWorkerPool
	config     ManagerConfig
}

// NewFaultProcessor creates a new fault processor
func NewFaultProcessor(config ManagerConfig) *FaultProcessor {
	return &FaultProcessor{
		dedupCache: NewFaultDeduplicationCache(config.FaultDeduplicationWindow),
		workerPool: NewLogCaptureWorkerPool(config),
		config:     config,
	}
}

// ProcessEvent processes a Kubernetes event and enriches it with logs if it's a fault
// Returns nil if the event should be filtered out (not a fault, duplicate, etc.)
func (p *FaultProcessor) ProcessEvent(
	ctx context.Context,
	k8s *kubernetes.Kubernetes,
	cluster, subscriptionID string,
	event *v1.Event,
) (*FaultEvent, error) {
	// Check if this is a fault event (Warning + Pod)
	if !isFaultEvent(event) {
		return nil, nil
	}

	// Generate deduplication key
	dedupKey := generateFaultDeduplicationKey(
		cluster,
		event.InvolvedObject.Namespace,
		event.InvolvedObject.Name,
		event.Reason,
		event.Count,
	)

	// Check for duplicate
	if p.dedupCache.IsDuplicate(dedupKey) {
		return nil, nil
	}

	// Mark as seen
	p.dedupCache.Mark(dedupKey)

	// Build event data
	timestamp := event.EventTime.Time
	if timestamp.IsZero() && event.Series != nil {
		timestamp = event.Series.LastObservedTime.Time
	} else if timestamp.IsZero() && event.Count > 1 {
		timestamp = event.LastTimestamp.Time
	} else if timestamp.IsZero() {
		timestamp = event.FirstTimestamp.Time
	}

	faultEvent := &FaultEvent{
		SubscriptionID: subscriptionID,
		Cluster:        cluster,
		Event: EventData{
			Namespace: event.Namespace,
			Timestamp: timestamp.Format(time.RFC3339),
			Type:      event.Type,
			Reason:    event.Reason,
			Message:   event.Message,
			InvolvedObject: &InvolvedObject{
				APIVersion: event.InvolvedObject.APIVersion,
				Kind:       event.InvolvedObject.Kind,
				Name:       event.InvolvedObject.Name,
				Namespace:  event.InvolvedObject.Namespace,
				UID:        string(event.InvolvedObject.UID),
			},
			Count: event.Count,
		},
	}

	// Copy labels if present
	if len(event.Labels) > 0 {
		faultEvent.Event.Labels = make(map[string]string)
		for k, v := range event.Labels {
			faultEvent.Event.Labels[k] = v
		}
	}

	// Attempt to capture logs (with concurrency limits)
	if p.workerPool.Acquire(ctx, cluster) {
		defer p.workerPool.Release(cluster)

		logs, err := capturePodLogs(
			ctx,
			k8s,
			event.InvolvedObject.Namespace,
			event.InvolvedObject.Name,
			p.config.MaxContainersPerNotification,
			p.config.MaxLogBytesPerContainer,
		)
		if err == nil {
			faultEvent.Logs = logs
		}
		// Don't fail the whole operation if log capture fails
	}

	return faultEvent, nil
}
