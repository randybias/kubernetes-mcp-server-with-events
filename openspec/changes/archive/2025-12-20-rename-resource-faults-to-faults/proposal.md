# Change: Rename resource-faults to faults

## Why

The current `refactor-fault-detection-signals` change introduced `mode="resource-faults"` as a new, superior fault detection mechanism that watches Kubernetes resources directly using SharedIndexInformers instead of relying on v1.Event resources. This new approach provides:

- **Higher signal/lower noise** - Edge-triggered detection on actual resource state transitions
- **Better reliability** - No dependency on best-effort Kubernetes Events
- **Fewer API calls** - Termination messages extracted from Pod status before log fetches
- **No notification storms** - Semantic deduplication prevents duplicate signals

The old `mode="faults"` (event-based) is now obsolete and redundant. Keeping both modes creates confusion for users and unnecessary maintenance burden.

**User need:** The user explicitly does not want backwards compatibility and prefers a clean, simple API where `mode="faults"` refers to the superior resource-based implementation.

## What Changes

This proposal **removes** the old event-based `mode="faults"` and **renames** `mode="resource-faults"` to `mode="faults"`:

1. **Remove old fault detection:**
   - Delete event-based fault watcher code that enriches Warning events with logs
   - Remove `logger="kubernetes/faults"` (event-based)
   - Remove old log enrichment pipeline for v1.Events

2. **Rename resource-faults to faults:**
   - Change `mode="resource-faults"` to `mode="faults"` in tool schema
   - Change `logger="kubernetes/resource-faults"` to `logger="kubernetes/faults"`
   - Update all documentation and examples
   - Keep all functionality and data structures unchanged

3. **Supported modes after this change:**
   - `mode="events"` - Raw Kubernetes Event stream (unchanged)
   - `mode="faults"` - Resource-based fault detection (formerly "resource-faults")

## Impact

- **Affected specs:** `kubernetes-event-streaming`
- **Breaking change:** YES - removes `mode="faults"` event-based implementation
- **Migration:** Users of old `mode="faults"` must switch to new `mode="faults"` (which is the renamed resource-faults). The notification payload structure is different but provides better quality signals.
- **No backwards compatibility:** User explicitly requested no backwards compatibility

### Code changes:
- `pkg/events/manager.go` - Remove old fault watcher code path
- `pkg/events/watcher.go` - Remove event-based fault logic (if separate)
- `pkg/events/logs.go` - Remove event-triggered log enrichment
- `pkg/events/notification.go` - Rename ResourceFaultNotification logger
- `pkg/toolsets/core/events.go` - Update mode enum and descriptions
- `pkg/events/README.md` - Update documentation

### Spec deltas:
- **REMOVED:** Old "Fault Subscription Tools" requirement (event-based)
- **REMOVED:** Old "Fault Notifications and Log Enrichment" requirement (event-based)
- **MODIFIED:** "Notification Logger Namespacing" requirement (remove reference to old faults, update resource-faults->faults)
- **MODIFIED:** All requirements added by refactor-fault-detection-signals to change "resource-faults" to "faults"
