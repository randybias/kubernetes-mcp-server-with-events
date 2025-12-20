# Design: Resource-Based Fault Detection

## Context

The current fault detection architecture watches v1.Event resources and enriches Warning events with container logs. This design has proven problematic:

1. Events are "best-effort" and often delayed or duplicated
2. Log enrichment requires 7-12 API calls per fault event (1 pod GET + 2 log fetches per container)
3. Under high-volume fault scenarios (20+ pod failures), the 100-event buffer overflows
4. Termination messages (panic traces) are already in Pod.Status, making log fetches redundant

## Goals / Non-Goals

**Goals:**
- Provide high-confidence fault signals with context already present in resource status
- Minimize API calls by using termination messages as primary context source
- Support fault types not detectable via Events (Node failures, Deployment progress deadlines)
- Maintain backwards compatibility with existing `mode="faults"` subscriptions

**Non-Goals:**
- Remove existing Event-based fault detection (preserved for compatibility)
- Replace all uses of log fetching (still needed when termination message is empty)
- Support custom fault detection rules (out of scope for initial implementation)

## Decisions

### Decision: Use SharedIndexInformers instead of Watch API

**What:** Use client-go SharedIndexInformers for Pod, Node, Deployment, and Job resources instead of watching v1.Event.

**Why:** Informers provide:
- Automatic resync and reconnection handling
- In-memory cache for efficient lookups
- Update callbacks with old/new object for edge detection
- Shared across subscriptions (efficient resource usage)

**Alternatives considered:**
- Continue with Event watching + optimization: Rejected because Events are fundamentally unreliable
- Watch individual resources without informers: Rejected because informers handle complexity

### Decision: Edge-Triggered Detection

**What:** Detect faults by comparing old/new object state in OnUpdate callbacks rather than reacting to Events.

**Why:** Edge-triggered detection provides:
- Precise fault timing (not delayed by Event propagation)
- Clear state transitions (RestartCount 0->1 vs any RestartCount event)
- No false positives from Event replays

**Fault triggers:**
- `RestartCount increase`: `newPod.Status.ContainerStatuses[i].RestartCount > oldPod...`
- `CrashLoopBackOff`: `Waiting.Reason == "CrashLoopBackOff"`
- `Node unhealthy`: Ready condition `True -> False/Unknown`
- `Deployment failure`: `ProgressDeadlineExceeded` condition
- `Job failure`: `Failed` condition with `reason != ""

### Decision: Cheap Context Strategy

**What:** Extract context from resource status before attempting log fetches.

**Why:** Pod termination messages contain panic traces and error information. Fetching logs when this information is already available wastes API calls.

**Strategy:**
1. On Pod fault detection, extract `state.terminated.message` from ContainerStatuses
2. If termination message is non-empty, use it as primary context
3. Only trigger log fetch if: (a) termination message is empty AND (b) fault is high-severity (CrashLoop)

### Decision: New Subscription Mode

**What:** Add `mode="resource-faults"` as a new subscription type.

**Why:**
- Preserves backwards compatibility with existing `mode="faults"`
- Different notification payload structure (status snapshot vs. event+logs)
- Allows gradual migration

**Logger:** `kubernetes/resource-faults`

## Risks / Trade-offs

### Risk: Informer Memory Usage
Informers cache all objects in memory. For large clusters with many pods/nodes, this could increase memory consumption.

**Mitigation:** Use filtered informers (namespace selectors, label selectors) to limit cached objects to those matching subscription filters.

### Risk: Missing Events During Informer Startup
After informer starts, there's a sync period where historical state is loaded but no Update events fire.

**Mitigation:** After initial sync, emit current fault states as "initial" signals. Mark them so clients know they're pre-existing conditions.

### Trade-off: Complexity vs. Reliability
Informer-based detection is more complex than Event watching but provides higher reliability.

**Decision:** Accept complexity because the current approach is fundamentally limited.

## Migration Plan

1. **Phase 1:** Implement `mode="resource-faults"` alongside existing `mode="faults"`
2. **Phase 2:** Gather feedback and compare reliability/performance
3. **Phase 3:** Consider deprecating `mode="faults"` if resource-based proves superior

No immediate migration required - both modes will coexist.

## Open Questions

- Should we support custom fault detection rules (e.g., user-defined conditions)?
- What severity levels should trigger log fetching vs. termination-message-only?
- Should we emit "resolved" signals when a fault condition clears (e.g., CrashLoopBackOff -> Running)?
