# Proposal: Filter Historical Events on Subscription Creation

**Change ID**: `filter-historical-events`
**Type**: Bug Fix / Enhancement
**Affects**: `kubernetes-event-streaming` spec
**Status**: Proposed
**Date**: December 10, 2025

## Problem Statement

When a client creates an event or fault subscription using `events_subscribe`, the Kubernetes watch starts from the beginning of the event list and sends all historical events that exist in the cluster. This causes false positives because events that occurred minutes, hours, or even days ago are sent to the newly-created subscription as if they just happened.

### Current Behavior

1. Client calls `events_subscribe` at time T
2. Server creates EventWatcher which starts a Kubernetes watch
3. Kubernetes API sends ALL existing events that match the filters
4. Client receives events from before time T, treating them as new issues
5. Client may trigger alerts/actions for events that are already resolved

### Example False Positive

```
12:00 PM - Pod crashes with FailedMount error (event A created)
12:05 PM - DevOps team fixes the issue manually
12:10 PM - AI agent subscribes to faults
12:10 PM - Agent receives event A (from 10 minutes ago)
12:10 PM - Agent takes action on already-resolved issue
```

## Proposed Solution

Filter out events that occurred before the subscription was created by starting the Kubernetes watch from the current resource version instead of from the beginning.

### Implementation Approach

1. Before starting the watch, perform a List operation to get the current resource version
2. Pass that resource version to the EventWatcher
3. EventWatcher uses this resource version when starting the watch
4. Kubernetes API only sends events that occur AFTER that resource version

### Key Principle

> "If there is an ongoing fault, we can count on Kubernetes to let us know eventually."

- If a pod is crash-looping, Kubernetes will generate new events
- If an issue was resolved before subscription, the client doesn't need to know about it
- Fresh subscriptions should only see fresh events

## Impact Analysis

### Benefits
- **Eliminates false positives**: Clients only see events that occur after they subscribe
- **Reduces noise**: No flood of historical events on subscription creation
- **Better semantics**: "Subscribe to events" means "notify me of new events going forward"
- **Cleaner agent behavior**: AI agents won't react to stale events

### Risks
- **Missed events during List → Watch gap**: There's a tiny window between the List operation and Watch start where events could be missed
  - **Mitigation**: This is acceptable because Kubernetes generates new events frequently for ongoing issues
- **Changed behavior**: Existing clients may depend on receiving historical events
  - **Mitigation**: Make this the default behavior (most expected), document clearly

### Breaking Change Assessment

**Is this a breaking change?** Potentially yes, but:
1. The current behavior is arguably a bug (sending historical events is unexpected)
2. Most clients would expect "subscribe" to mean "from now on"
3. The documented use case is monitoring ongoing issues, not historical analysis
4. If a client needs historical events, they can use regular Kubernetes API queries

**Recommendation**: Implement as default behavior with clear documentation.

## Alternative Approaches Considered

### 1. Add `includeHistorical` flag
- **Pros**: Backward compatible, gives clients control
- **Cons**: Adds complexity, most clients would set it to `false`
- **Decision**: Not recommended - the current behavior is a bug, not a feature

### 2. Add `sinceTime` parameter
- **Pros**: Flexible, allows clients to specify exact time window
- **Cons**: Complex API, requires clock synchronization, resource version is better
- **Decision**: Not recommended - resource version approach is simpler

### 3. Do nothing, document the behavior
- **Pros**: No code changes needed
- **Cons**: Doesn't solve the false positive problem
- **Decision**: Not recommended - the problem is real and should be fixed

## Success Criteria

1. ✅ New subscriptions only receive events with timestamps after subscription creation time
2. ✅ No historical events sent on subscription creation
3. ✅ Ongoing faults still generate notifications (Kubernetes generates new events)
4. ✅ Existing tests pass with updated expectations
5. ✅ Manual testing confirms no historical events received

## Related Work

- Original implementation: `add-sse-event-subscriptions` (archived 2025-12-09)
- Related spec: `kubernetes-event-streaming` with 10 requirements
- Affects: EventWatcher initialization in `pkg/events/watcher.go`
- Affects: Subscription creation in `pkg/events/manager.go`

## Open Questions

None - the approach is straightforward and well-understood.
