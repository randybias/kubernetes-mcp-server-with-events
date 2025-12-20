# Implementation Tasks: Reduce Watcher Log Noise

## Overview

This change fixes a critical 410 error handling bug and then adjusts log levels in the Kubernetes event watcher to reduce noise during normal operations. All changes are in `pkg/events/`.

## Tasks

### 0. Fix 410 Error Thrashing Bug (PREREQUISITE)

**Location**: `pkg/events/watcher.go`, `pkg/events/watcher_test.go`

**Changes**:
- [x] 0.1 Add `net/http` import to watcher.go
- [x] 0.2 In error event handling (line ~240), detect 410 Gone errors:
  - Type assert `event.Object` to `*metav1.Status`
  - Check if `status.Code == http.StatusGone` (410)
  - Clear `w.resourceVersion = ""` when 410 detected
  - Return error to trigger reconnection with fresh watch
- [x] 0.3 Add test `TestWatch410ErrorHandling/clears_resourceVersion_on_410_Gone_error`:
  - Verify resourceVersion is cleared after 410 error
  - Verify next watch starts without stale resourceVersion
- [x] 0.4 Add test `TestWatch410ErrorHandling/continues_normal_operation_after_410_error_recovery`:
  - Verify watch recovers and processes events after 410 error
- [x] 0.5 Update test timeouts to account for 2s exponential backoff on retry

**Validation**:
- Build succeeds: `make build`
- Tests pass: `go test ./pkg/events -run TestWatch410ErrorHandling`
- Manual test: Run server with `--log-level 2`, wait for/trigger 410 error, verify recovery without infinite loop

### 1. Adjust log levels in `pkg/events/watcher.go`

**Location**: `pkg/events/watcher.go`

**Changes**:
- Line 220: `klog.V(2).Info("Event watch successfully established")` → Keep at V(2) (already appropriate)
- Line 232: `klog.V(2).Info("Watch channel closed, will reconnect")` → Keep at V(2) (already appropriate)
- Line 241: `klog.Warningf("Watch error event: %v", event.Object)` → Change to `klog.V(2).Infof("Watch error event: %v", event.Object)`
- Line 127: `klog.Warningf("Watch failed (attempt %d/%d): %v", ...)` → Change to conditional:
  - If `w.retryCount < 3`: Use `klog.V(2).Infof(...)`
  - If `w.retryCount >= 3`: Keep `klog.Warningf(...)`

**Validation**: Build and run the server with a watch that expires. Verify no WARNING logs appear during first 2 retry attempts.

### 2. Adjust log levels in `pkg/events/manager.go`

**Location**: `pkg/events/manager.go`

**Changes**:
- Line 504: `klog.Warningf("Watch error for subscription %s: %v", sub.ID, err)` → Change to conditional:
  - Parse error message to detect routine reconnections (e.g., "watch channel closed")
  - If routine reconnection: Use `klog.V(2).Infof(...)`
  - If actual error: Keep `klog.Warningf(...)`

**Validation**: Run the server and verify subscription errors only log at WARNING for non-routine errors.

### 3. Update tests (if needed)

**Location**: `pkg/events/watcher_test.go`

**Changes**: None expected - tests don't check log output

**Validation**: Run `make test` to ensure no tests are affected by log level changes.

### 4. Test with manual watch expiration

**Test scenario**:
1. Start the server with multiple subscriptions
2. Wait for or trigger a watch expiration (410 Gone)
3. Verify automatic reconnection succeeds
4. Check logs:
   - Should see INFO logs for subscription creation
   - Should NOT see WARNING logs during first 2 reconnection attempts
   - Should see DEBUG logs (with `-v=2` flag) showing reconnection details

**Validation**: Log output matches expected behavior in proposal.

### 5. Test with repeated failures

**Test scenario**:
1. Create conditions that cause watch connection failures (e.g., network issues)
2. Let the watcher retry 3+ times
3. Verify WARNING logs appear after 3rd attempt
4. Let it exhaust all retries
5. Verify final WARNING/ERROR log appears

**Validation**: WARNING logs appear appropriately for persistent issues.

### 6. Documentation update

**Location**: None required - this is an internal implementation detail

**Changes**: None - log levels are not part of the user-facing API

## Implementation Notes

- Use `klog.V(2)` for DEBUG-level logs (enabled with `-v=2` or higher)
- Use `klog.V(1)` for verbose INFO-level logs (enabled with `-v=1` or higher)
- Keep subscription lifecycle events at `klog.V(1)` (already done)
- Add retry count threshold check (3 attempts) before escalating to WARNING

## Dependencies

None - this change is independent of other work.

## Rollback Plan

If logs prove too quiet, revert the changed lines back to their original log levels. No functional changes are made, so rollback is safe and simple.
