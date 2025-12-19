# Events Package

This package implements Kubernetes event watching with automatic reconnection, exponential backoff, and deduplication for the MCP server event subscription feature.

## Components

### watcher.go
Implements `EventWatcher` which manages watching Kubernetes events with:
- Automatic reconnection with exponential backoff (1s, 2s, 4s, 8s, 16s, 30s capped)
- Resource version tracking for resume capability
- 5-retry limit before entering degraded state
- Client-side filtering for namespaces, event types, and reasons
- Integration with deduplication cache

### dedup.go
Implements `DeduplicationCache` which provides:
- TTL-based deduplication (5s for events mode, 60s for faults mode)
- Thread-safe concurrent access
- Automatic cleanup of expired entries
- Key format: `<cluster>/<ns>/<name>/<uid>/<resourceVersion>` for events

### notification.go
Provides event serialization structures:
- `EventNotification` - payload for kubernetes/events notifications
- `EventDetails` - serialized event information
- `SubscriptionErrorNotification` - payload for subscription errors
- Logger name constants for notification delivery

### filters.go
Implements `SubscriptionFilters` for filtering events by:
- Namespaces (multiple)
- Label selectors
- Involved object (kind, name, namespace)
- Event type (Normal, Warning)
- Reason (prefix match)

## Tests

### dedup_test.go
Unit tests for deduplication cache covering:
- First event passes through (not duplicate)
- Same key within TTL returns duplicate
- Expired entries pass through
- Different resource versions are not duplicates
- Concurrent access safety
- Multiple independent caches

### watcher_test.go
Unit tests for event watcher covering:
- Exponential backoff calculation (1s-30s capped)
- Watch reconnection after channel close
- Resource version tracking
- Client-side filtering (namespace, type, reason)
- Deduplication integration

## Integration

The watcher integrates with the EventSubscriptionManager (manager.go) which:
- Creates subscriptions with unique IDs
- Starts watchers for each subscription
- Delivers notifications via MCP server sessions
- Handles session lifecycle and cleanup
