# Design: Filter Historical Events on Subscription Creation

**Change ID**: `filter-historical-events`
**Related Spec**: `kubernetes-event-streaming`

## Overview

This design addresses the issue where new event subscriptions receive all historical events from the Kubernetes cluster, causing false positives. The solution uses Kubernetes resource versioning to start watches from "now" instead of from the beginning of the event list.

## Current Architecture

### Event Flow Today

```
Client → events_subscribe → SubscriptionManager → startWatcher()
                                                        ↓
                                              EventWatcher.Start()
                                                        ↓
                                              watch.Watch(ListOptions{})
                                                        ↓
                                              Kubernetes API sends ALL events
                                                        ↓
                                              Client receives historical events
```

### Problem: No Resource Version Set

When `startWatch()` creates the watch with empty `ListOptions`, Kubernetes interprets this as "send me everything":

```go
// Current code in watcher.go:143-147
func (w *EventWatcher) startWatch(ctx context.Context) error {
    opts := metav1.ListOptions{
        Watch: true,
    }
    // No ResourceVersion set means "from beginning"
}
```

## Proposed Architecture

### New Event Flow

```
Client → events_subscribe → SubscriptionManager → getCurrentResourceVersion()
                                                        ↓
                                                   List(Limit=1)
                                                        ↓
                                                   Get resourceVersion "12345"
                                                        ↓
                                              startWatcher(initialRV="12345")
                                                        ↓
                                              EventWatcher.Start()
                                                        ↓
                                              watch.Watch(ResourceVersion="12345")
                                                        ↓
                                              Kubernetes API sends only NEW events
```

### Solution: Resource Version Initialization

Add initialization step to capture the current state:

```go
// Proposed code in manager.go:startWatcher()
func (m *EventSubscriptionManager) startWatcher(sub *Subscription) error {
    // ... existing client setup ...

    // Get current resource version to start from "now"
    initialResourceVersion, err := m.getCurrentResourceVersion(clientset, namespace)
    if err != nil {
        return fmt.Errorf("cannot create subscription: failed to get current resource version: %w", err)
    }

    watcher := NewEventWatcher(EventWatcherConfig{
        // ... existing config ...
        InitialResourceVersion: initialResourceVersion,
    })

    // ... rest of method ...
}

// New helper method
func (m *EventSubscriptionManager) getCurrentResourceVersion(clientset kubernetes.Interface, namespace string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    opts := metav1.ListOptions{
        Limit: 1, // We only need the resource version, not the actual events
    }

    var list *v1.EventList
    var err error

    if namespace == "" {
        list, err = clientset.CoreV1().Events(metav1.NamespaceAll).List(ctx, opts)
    } else {
        list, err = clientset.CoreV1().Events(namespace).List(ctx, opts)
    }

    if err != nil {
        return "", fmt.Errorf("failed to list events: %w", err)
    }

    return list.ResourceVersion, nil
}
```

## Implementation Details

### 1. EventWatcherConfig Changes

Add new field to configuration struct:

```go
// In watcher.go
type EventWatcherConfig struct {
    Clientset              kubernetes.Interface
    Namespace              string
    Filters                *SubscriptionFilters
    MaxRetries             int
    OnError                func(error)
    OnDegraded             func()
    DedupCache             *DeduplicationCache
    ProcessEvent           func(*v1.Event)
    InitialResourceVersion string // NEW: Resource version to start watch from
}
```

### 2. EventWatcher Changes

Store and use initial resource version:

```go
// In watcher.go
type EventWatcher struct {
    clientset            kubernetes.Interface
    namespace            string
    filters              *SubscriptionFilters
    maxRetries           int
    retryCount           int
    resourceVersion      string
    initialResourceVersion string // NEW: Set on creation, used on first watch
    onError              func(error)
    onDegraded           func()
    dedupCache           *DeduplicationCache
    processEvent         func(*v1.Event)
    resultChan           chan watch.Event
    stopChan             chan struct{}
}

func NewEventWatcher(config EventWatcherConfig) *EventWatcher {
    return &EventWatcher{
        // ... existing fields ...
        initialResourceVersion: config.InitialResourceVersion, // NEW
    }
}

func (w *EventWatcher) startWatch(ctx context.Context) error {
    opts := metav1.ListOptions{
        Watch: true,
    }

    // Use resourceVersion if resuming from reconnection
    if w.resourceVersion != "" {
        opts.ResourceVersion = w.resourceVersion
        klog.V(2).Infof("Resuming watch from resource version %s", w.resourceVersion)
    } else {
        // NEW: On first watch, use initial resource version to skip historical events
        // initialResourceVersion is REQUIRED - subscription creation fails if not set
        opts.ResourceVersion = w.initialResourceVersion
        klog.V(1).Infof("Starting watch from initial resource version %s (skipping historical events)", w.initialResourceVersion)
    }

    // ... rest of method ...
}
```

### 3. Logging and Observability

Add logging to make the behavior transparent:

```go
// In manager.go
klog.V(1).Infof("Starting subscription %s from resource version %s (filtering historical events)", sub.ID, initialResourceVersion)
```

## Resource Version Semantics

### Kubernetes API Behavior

| ResourceVersion Value | Watch Behavior |
|-----------------------|----------------|
| Empty ("") | Start from beginning (all historical events) |
| "0" | Start from most recent (same as getting current RV first) |
| Specific version ("12345") | Start from that version |

### Why List(Limit=1)?

- **Efficient**: Only fetches metadata, not event content
- **Fast**: Completes in milliseconds
- **Accurate**: Gets the exact current resource version
- **Safe**: Read-only operation with minimal cluster impact

## Edge Cases and Handling

### 1. List Operation Fails

**Scenario**: Kubernetes API is unavailable or RBAC denies List permission

**Handling**:
```go
if err != nil {
    return fmt.Errorf("cannot create subscription: failed to get current resource version: %w", err)
}
```

**Impact**: Subscription creation fails with clear error message. Client must retry when API is available.

### 2. Events During List → Watch Gap

**Scenario**: Event occurs between List(Limit=1) and Watch() start

**Handling**: Accept as acceptable risk

**Rationale**:
- Gap is typically < 100ms
- If it's an ongoing issue, Kubernetes will generate another event soon
- False negatives (missing one event) are better than false positives (hundreds of historical events)

### 3. Empty Cluster (No Events)

**Scenario**: Cluster has never had any events

**Handling**: List() returns empty ResourceVersion, watch starts from "0"

**Impact**: Works correctly - no historical events to filter

### 4. Namespace vs Cluster-Wide

**Scenario**: Single-namespace subscription vs all-namespaces subscription

**Handling**: Both cases handled by passing correct namespace parameter to List():
```go
if namespace == "" {
    list, err = clientset.CoreV1().Events(metav1.NamespaceAll).List(ctx, opts)
} else {
    list, err = clientset.CoreV1().Events(namespace).List(ctx, opts)
}
```

## Performance Considerations

### Added Latency

- **List(Limit=1)**: ~10-50ms typical
- **Total subscription creation time**: Increases by ~10-50ms
- **Acceptable**: Subscriptions are not created frequently

### Network Overhead

- **One extra API call** per subscription
- **Minimal payload**: Only metadata, no event objects
- **Negligible impact**: Single List is far cheaper than receiving hundreds of historical events

### Memory Impact

- **No additional memory**: Resource version is a string (~20 bytes)
- **Reduces memory**: Fewer events processed means less memory for deduplication caches

## Testing Strategy

### Unit Tests

1. **EventWatcher respects initial resource version**
   ```go
   func TestEventWatcher_UsesInitialResourceVersion(t *testing.T) {
       // Create watcher with initial RV
       // Verify first watch uses that RV
   }
   ```

2. **Subscription fails when List fails**
   ```go
   func TestSubscriptionManager_FailsWhenResourceVersionUnavailable(t *testing.T) {
       // Mock List operation to fail
       // Attempt to create subscription
       // Verify subscription creation returns error
   }
   ```

### Integration Tests

1. **Historical events filtered**
   ```go
   func TestSubscription_FiltersHistoricalEvents(t *testing.T) {
       // Create events in cluster
       // Wait 5 seconds
       // Create subscription
       // Verify NO historical events received
   }
   ```

2. **New events delivered**
   ```go
   func TestSubscription_DeliversNewEvents(t *testing.T) {
       // Create subscription
       // Create new event
       // Verify event IS received
   }
   ```

### Manual Testing

1. **Crashloop scenario**:
   - Deploy pod that crashes repeatedly for 5 minutes
   - Subscribe to faults
   - Verify: NO notifications for the 5 minutes of historical crashes
   - Verify: NEW crashes (after subscription) DO generate notifications

2. **Ongoing fault scenario**:
   - Deploy pod with FailedMount error
   - Wait 2 minutes
   - Subscribe to faults
   - Verify: Kubernetes generates new FailedMount event within 30 seconds
   - Verify: New event IS notified (proves ongoing faults aren't missed)

## Migration and Rollout

### Breaking Change Assessment

**Not a breaking change because**:
1. Clients shouldn't depend on receiving historical events (it's a bug)
2. The documented behavior is "subscribe to events" not "get event history"
3. Clients that need history can use standard Kubernetes API queries

### Rollout Plan

1. **Phase 1**: Implement with logging showing filtered count
2. **Phase 2**: Deploy to test environment, verify with manual testing
3. **Phase 3**: Deploy to production, monitor logs for unexpected behavior
4. **Phase 4**: Update documentation to clarify "from now on" semantics

### Documentation Updates

- README: Add note that subscriptions only notify on new events
- API docs: Clarify that `events_subscribe` creates "forward-looking" subscription
- Troubleshooting: Add section explaining why historical events aren't included

## Alternative Designs Considered

### Alternative 1: Add `includeHistorical` flag

```go
// events_subscribe input
{
  "namespaces": ["default"],
  "includeHistorical": false  // NEW parameter
}
```

**Rejected because**:
- Adds API complexity
- 99% of clients would set it to `false`
- Current behavior is a bug, not a feature to preserve

### Alternative 2: Use `sinceTime` parameter

```go
// events_subscribe input
{
  "namespaces": ["default"],
  "sinceTime": "2025-12-10T12:00:00Z"  // NEW parameter
}
```

**Rejected because**:
- Requires clock synchronization between client and server
- More complex than resource version approach
- Kubernetes resource versioning is designed for this exact use case

### Alternative 3: Client-side filtering by timestamp

**Rejected because**:
- Wastes network bandwidth sending historical events
- Wastes CPU processing events that will be filtered
- Deduplication cache fills with historical events
- Resource version approach is more efficient

## Success Metrics

1. ✅ Zero historical events sent on new subscriptions
2. ✅ All new events (after subscription) are delivered
3. ✅ Subscription creation latency increase < 100ms
4. ✅ No increase in API server load (List is cheaper than sending many events)
5. ✅ No user complaints about missed events

## References

- Kubernetes API Concepts: https://kubernetes.io/docs/reference/using-api/api-concepts/#resource-versions
- Watch API: https://kubernetes.io/docs/reference/using-api/api-concepts/#efficient-detection-of-changes
- Original implementation: `add-sse-event-subscriptions` (archived 2025-12-09)
