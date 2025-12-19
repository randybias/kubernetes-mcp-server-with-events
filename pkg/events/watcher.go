package events

import (
	"context"
	"fmt"
	"net/http"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// exponentialBackoff calculates backoff duration for retry attempts
// Returns: 1s, 2s, 4s, 8s, 16s, 30s (capped at 30s)
func exponentialBackoff(retryCount int) time.Duration {
	if retryCount <= 0 {
		return time.Second
	}

	// Calculate 2^retryCount seconds
	backoff := time.Second * (1 << uint(retryCount))

	// Cap at 30 seconds
	if backoff > 30*time.Second {
		return 30 * time.Second
	}

	return backoff
}

// EventWatcher manages watching Kubernetes events with automatic reconnection
type EventWatcher struct {
	clientset kubernetes.Interface
	namespace string
	// resourceVersion is the current resource version of the watch.
	// This is updated as events are received and used to resume watching
	// from the correct position after a reconnection.
	resourceVersion string
	// initialResourceVersion is the resource version to use on the first watch.
	// This is set once during creation and never changed, allowing the watcher
	// to skip historical events on initial connection while still resuming from
	// the correct position on reconnections.
	initialResourceVersion string
	filters                *SubscriptionFilters
	resultChan             chan watch.Event
	stopChan               chan struct{}
	retryCount             int
	maxRetries             int
	onError                func(error)
	onDegraded             func()
	dedupCache             *DeduplicationCache
	processEvent           func(event *v1.Event)
}

// EventWatcherConfig holds configuration for the event watcher
type EventWatcherConfig struct {
	Clientset    kubernetes.Interface
	Namespace    string
	Filters      *SubscriptionFilters
	MaxRetries   int
	OnError      func(error)
	OnDegraded   func()
	DedupCache   *DeduplicationCache
	ProcessEvent func(event *v1.Event)
	// InitialResourceVersion is the resource version to start watching from.
	// When set, the watcher will skip all historical events and only process
	// events with resource versions greater than this value. This prevents
	// subscriptions from receiving historical events that occurred before
	// the subscription was created.
	// If empty, the watch will start from the beginning (receiving all historical events).
	InitialResourceVersion string
}

// NewEventWatcher creates a new event watcher with the given configuration
func NewEventWatcher(config EventWatcherConfig) *EventWatcher {
	if config.MaxRetries == 0 {
		config.MaxRetries = 5
	}

	return &EventWatcher{
		clientset:              config.Clientset,
		namespace:              config.Namespace,
		filters:                config.Filters,
		maxRetries:             config.MaxRetries,
		onError:                config.OnError,
		onDegraded:             config.OnDegraded,
		dedupCache:             config.DedupCache,
		processEvent:           config.ProcessEvent,
		initialResourceVersion: config.InitialResourceVersion,
		resultChan:             make(chan watch.Event, 100),
		stopChan:               make(chan struct{}),
	}
}

// Start begins watching for events with automatic reconnection
func (w *EventWatcher) Start(ctx context.Context) {
	go w.watchLoop(ctx)
}

// Stop stops the event watcher
func (w *EventWatcher) Stop() {
	close(w.stopChan)
}

// ResultChan returns the channel for receiving watch events
func (w *EventWatcher) ResultChan() <-chan watch.Event {
	return w.resultChan
}

// watchLoop is the main watch loop with reconnection logic
func (w *EventWatcher) watchLoop(ctx context.Context) {
	defer close(w.resultChan)

	for {
		select {
		case <-ctx.Done():
			klog.V(2).Info("Watch context cancelled, stopping event watcher")
			return
		case <-w.stopChan:
			klog.V(2).Info("Stop signal received, stopping event watcher")
			return
		default:
			if err := w.startWatch(ctx); err != nil {
				w.retryCount++
				klog.Warningf("Watch failed (attempt %d/%d): %v", w.retryCount, w.maxRetries, err)

				if w.onError != nil {
					w.onError(err)
				}

				if w.retryCount >= w.maxRetries {
					klog.Warningf("Watch connection failed after %d reconnection attempts", w.maxRetries)
					if w.onDegraded != nil {
						w.onDegraded()
					}
					return
				}

				// Exponential backoff before retry
				backoff := exponentialBackoff(w.retryCount)
				klog.V(2).Infof("Backing off for %v before retry", backoff)

				select {
				case <-time.After(backoff):
					continue
				case <-ctx.Done():
					return
				case <-w.stopChan:
					return
				}
			}
		}
	}
}

// startWatch creates a new watch and processes events
func (w *EventWatcher) startWatch(ctx context.Context) error {
	// Build watch options
	opts := metav1.ListOptions{
		Watch: true,
	}

	// Use resource version if available for resuming
	if w.resourceVersion != "" {
		opts.ResourceVersion = w.resourceVersion
		klog.V(2).Infof("Resuming watch from resource version %s", w.resourceVersion)
	} else if w.initialResourceVersion != "" {
		// On first watch, use initial resource version to skip historical events
		opts.ResourceVersion = w.initialResourceVersion
		klog.V(1).Infof("Starting watch from initial resource version %s (skipping historical events)", w.initialResourceVersion)
	}

	// Add field selectors for involved object if specified
	if w.filters != nil {
		if w.filters.InvolvedKind != "" {
			if opts.FieldSelector != "" {
				opts.FieldSelector += ","
			}
			opts.FieldSelector += fmt.Sprintf("involvedObject.kind=%s", w.filters.InvolvedKind)
		}
		if w.filters.InvolvedName != "" {
			if opts.FieldSelector != "" {
				opts.FieldSelector += ","
			}
			opts.FieldSelector += fmt.Sprintf("involvedObject.name=%s", w.filters.InvolvedName)
		}
		if w.filters.InvolvedNamespace != "" {
			if opts.FieldSelector != "" {
				opts.FieldSelector += ","
			}
			opts.FieldSelector += fmt.Sprintf("involvedObject.namespace=%s", w.filters.InvolvedNamespace)
		}
		if w.filters.Type != "" {
			if opts.FieldSelector != "" {
				opts.FieldSelector += ","
			}
			opts.FieldSelector += fmt.Sprintf("type=%s", w.filters.Type)
		}
	}

	// Create the watcher
	var watcher watch.Interface
	var err error

	if w.namespace != "" {
		klog.V(2).Infof("Starting namespace-scoped watch for events in namespace %s", w.namespace)
		watcher, err = w.clientset.CoreV1().Events(w.namespace).Watch(ctx, opts)
	} else {
		klog.V(2).Info("Starting cluster-wide watch for events")
		watcher, err = w.clientset.CoreV1().Events("").Watch(ctx, opts)
	}

	if err != nil {
		return fmt.Errorf("failed to create event watcher: %w", err)
	}
	defer watcher.Stop()

	klog.V(2).Info("Event watch successfully established")

	// Process events from the watcher
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.stopChan:
			return nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Watch closed, need to reconnect
				klog.V(2).Info("Watch channel closed, will reconnect")
				return fmt.Errorf("watch channel closed")
			}

			// Reset retry count on successful event
			w.retryCount = 0

			// Handle watch errors
			if event.Type == watch.Error {
				// Check if this is a 410 Gone error (resourceVersion too old)
				if status, ok := event.Object.(*metav1.Status); ok {
					klog.Warningf("Watch error event: %v", status)

					// If resourceVersion is too old (410 Gone), clear it so the next
					// watch starts fresh instead of retrying with the same stale version
					if status.Code == http.StatusGone {
						klog.V(2).Infof("ResourceVersion %s is too old (410 Gone), clearing for fresh watch", w.resourceVersion)
						w.resourceVersion = ""
						return fmt.Errorf("watch resource version expired: %s", status.Message)
					}
				}
				continue
			}

			k8sEvent, ok := event.Object.(*v1.Event)
			if !ok {
				klog.Warningf("Unexpected object type in watch event: %T", event.Object)
				continue
			}

			// Update resource version
			if k8sEvent.ResourceVersion != "" {
				w.resourceVersion = k8sEvent.ResourceVersion
			}

			// Apply client-side filters
			if !w.matchesFilters(k8sEvent) {
				continue
			}

			// Check deduplication
			if w.dedupCache != nil {
				key := w.makeDeduplicationKey(k8sEvent)
				if w.dedupCache.IsDuplicate(key) {
					klog.V(2).Infof("Skipping duplicate event: %s", key)
					continue
				}
			}

			// Process the event
			if w.processEvent != nil {
				w.processEvent(k8sEvent)
			}

			// Send to result channel (non-blocking)
			select {
			case w.resultChan <- event:
			default:
				klog.Warning("Event result channel full, dropping event")
			}
		}
	}
}

// matchesFilters checks if an event matches the subscription filters
func (w *EventWatcher) matchesFilters(event *v1.Event) bool {
	if w.filters == nil {
		return true
	}

	// Check namespace filter (if namespaces list is specified)
	if len(w.filters.Namespaces) > 0 {
		matched := false
		for _, ns := range w.filters.Namespaces {
			if event.Namespace == ns {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check type filter
	if w.filters.Type != "" && event.Type != w.filters.Type {
		return false
	}

	// Check reason filter (prefix match)
	if w.filters.Reason != "" {
		if len(event.Reason) < len(w.filters.Reason) {
			return false
		}
		if event.Reason[:len(w.filters.Reason)] != w.filters.Reason {
			return false
		}
	}

	// Note: Label selector filtering would require additional logic
	// to fetch the involved object and check its labels
	// For now, we skip label selector filtering in the watcher

	return true
}

// makeDeduplicationKey creates a unique key for event deduplication
// Key format: <cluster>/<ns>/<name>/<uid>/<resourceVersion>
func (w *EventWatcher) makeDeduplicationKey(event *v1.Event) string {
	return fmt.Sprintf("%s/%s/%s/%s",
		event.Namespace,
		event.Name,
		event.UID,
		event.ResourceVersion,
	)
}
