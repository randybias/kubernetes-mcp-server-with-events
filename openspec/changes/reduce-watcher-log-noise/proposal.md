# Proposal: Reduce Watcher Log Noise

**Change ID**: `reduce-watcher-log-noise`
**Type**: Enhancement
**Affects**: `kubernetes-event-streaming` spec
**Status**: Proposed
**Date**: December 10, 2025

## Problem Statement

The Kubernetes event watcher has two related issues with 410 (Gone) error handling:

1. **CRITICAL BUG: 410 Error Thrashing** - When the Kubernetes API returns HTTP 410 because the resourceVersion is too old, the watcher retries with the same stale resourceVersion, creating an infinite loop that floods logs and prevents recovery.

2. **Log Noise** - When 410 errors are properly handled (after fixing the bug above), the watcher generates excessive INFO-level log output during normal reconnections, producing 5+ log lines for routine operations that happen regularly in production.

### Current Behavior (Bug)

When a watch connection receives a 410 error with a stale resourceVersion, the watcher enters an infinite loop:

```
I1218 08:25:28.102875 watcher.go:241] "Watch error event: &Status{...Code:410,}"
I1218 08:25:28.102892 watcher.go:232] "Watch channel closed, will reconnect"
I1218 08:25:28.102898 watcher.go:127] "Watch failed (attempt 1/5): watch channel closed"
I1218 08:25:28.102902 manager.go:504] "Watch error for subscription sub-xxx: watch channel closed"
I1218 08:25:28.102906 watcher.go:143] "Backing off for 2s before retry"
I1218 08:25:30.103262 watcher.go:168] "Resuming watch from resource version 2256"
I1218 08:25:30.120800 watcher.go:241] "Watch error event: &Status{...Code:410,}"
I1218 08:25:30.120818 watcher.go:232] "Watch channel closed, will reconnect"
...repeats infinitely with same resourceVersion 2256...
```

**Root cause**: The watcher retries with the same stale resourceVersion instead of clearing it and starting fresh.

### Current Behavior (After Bug Fix)

When a watch connection expires and reconnects (a normal occurrence), the following logs are produced:

```
I1210 15:45:29.825211 watcher.go:221] "Watch error event: &Status{...Code:410,}"
I1210 15:45:29.825289 watcher.go:212] "Watch channel closed, will reconnect"
I1210 15:45:29.825300 watcher.go:111] "Watch failed (attempt 1/5): watch channel closed"
I1210 15:45:29.825307 manager.go:450] "Watch error for subscription sub-xxx: watch channel closed"
I1210 15:45:29.825314 watcher.go:127] "Backing off for 2s before retry"
I1210 15:45:31.826460 watcher.go:152] "Resuming watch from resource version 72200"
I1210 15:45:31.826498 watcher.go:191] "Starting cluster-wide watch for events"
I1210 15:45:31.882819 watcher.go:200] "Event watch successfully established"
```

### Why This Is a Problem

1. **CRITICAL: Infinite loop prevents recovery** - The watcher cannot recover from a 410 error, causing the subscription to thrash indefinitely with 2-second retry intervals, flooding logs and wasting resources.

2. **410 errors are expected** - The Kubernetes API returns 410 (Gone) when the resource version becomes too old due to etcd compaction. This is normal Kubernetes behavior, not an error condition.

2. **Reconnections are routine** - Depending on cluster settings, watch reconnections can occur every few minutes to hours. This is handled automatically by the watcher's retry logic.

3. **Log spam** - Each reconnection cycle produces 5-8 log lines at INFO or WARNING level, creating noise that obscures actual issues.

4. **Operator burden** - Operators monitoring logs see constant "errors" that aren't actually problems, leading to alert fatigue or ignoring legitimate issues.

## Proposed Solution

This proposal addresses both issues:

### Part 1: Fix 410 Thrashing Bug (PREREQUISITE)

Detect 410 Gone errors and clear the stale resourceVersion before retrying:

1. Check if watch error event is HTTP 410 (Gone)
2. Clear `w.resourceVersion` so next watch starts fresh
3. Return error to trigger reconnection with clean state
4. Add comprehensive test coverage for 410 error handling

**Files affected**: `pkg/events/watcher.go`, `pkg/events/watcher_test.go`

### Part 2: Adjust Log Levels (After Bug Fix)

Adjust log levels to match the severity of events:

- **Normal lifecycle events** (reconnections, backoff, resume) → DEBUG level (`klog.V(2)` or higher)
- **Noteworthy events** (initial watch establishment) → INFO level (once per subscription)
- **Approaching failure** (multiple retries) → WARN level (only after 3+ failures)
- **Unrecoverable failure** (max retries exhausted) → ERROR level

### Implementation Approach

The changes will be made to two files in `pkg/events/`:

1. **watcher.go** - Adjust log levels in the EventWatcher
2. **manager.go** - Adjust log levels in the subscription manager

### Specific Log Level Changes

#### Currently at WARNING → Move to DEBUG/INFO

- Watch error events (410 Gone) → DEBUG
- Watch channel closed messages → DEBUG
- Individual retry attempts → DEBUG
- Backoff timing messages → DEBUG
- Resume from resource version → DEBUG

#### Keep at WARNING

- Repeated failures (after 3+ attempts) → WARN
- Max retries exhausted → WARN
- Failed notification delivery → WARN
- Event processing failures → WARN

#### Keep at INFO

- Initial watch establishment (once) → INFO
- Subscription creation/cancellation → INFO (via `klog.V(1)`)

### Expected Outcome

After the change, a normal watch reconnection will produce **zero logs at INFO level or above** during routine operations. Operators will only see logs when:

1. A subscription is created or cancelled (INFO)
2. A watch is repeatedly failing to reconnect (WARN after 3+ attempts)
3. A watch exhausts all retry attempts (WARN/ERROR)
4. An actual error occurs during event processing (WARN/ERROR)

## Impact Analysis

### Benefits

1. **Reduced log volume** - Dramatically fewer logs in production
2. **Better signal-to-noise** - Important events stand out
3. **Operator-friendly** - No more "cry wolf" warnings
4. **Debug-ready** - Enable verbose logging when troubleshooting

### Risks

- **None** - This only changes log levels, not behavior
- Operators can enable verbose logging (`-v=2` or higher) when debugging watch issues

### Compatibility

- **Fully backward compatible** - No API or behavior changes
- No spec changes needed - this is an implementation detail

## Tasks

See `tasks.md` for the implementation checklist.

## Alternatives Considered

### Alternative 1: Remove logs entirely
**Rejected** - Debugging watch issues requires visibility into the reconnection process.

### Alternative 2: Rate-limit logs
**Rejected** - Adds complexity. Better to use appropriate log levels and let klog handle verbosity.

### Alternative 3: Create custom logger for watches
**Rejected** - Unnecessary abstraction. klog's verbosity levels are designed for this use case.

## Questions for Review

None - this is a straightforward log level adjustment with no functional impact.
