# Design: Remove PodCrash Detection

## Problem Statement

The PodCrash detector creates noise by emitting notifications for transient container crashes that Kubernetes automatically recovers from. This makes it difficult for users to distinguish between:

1. **Actionable faults**: Persistent crashes requiring intervention
2. **Self-healing transients**: Single crashes that resolve automatically

The overlap between PodCrash and CrashLoop detection results in duplicate notifications for the same underlying problem.

## Current State Analysis

### How PodCrash Detection Works

**Trigger**: Edge-triggered on RestartCount increase
```go
For each container:
  IF newStatus.RestartCount > oldStatus.RestartCount
    AND lastTerminationState.exitCode != 0
  THEN emit PodCrash signal
```

**What it detects**:
- ANY container restart with non-zero exit code
- Includes both transient and persistent failures
- No distinction between first crash and Nth crash

### How CrashLoop Detection Works

**Trigger**: Edge-triggered on CrashLoopBackOff state entry
```go
For each container:
  IF newStatus.waiting.reason == "CrashLoopBackOff"
    AND oldStatus.waiting.reason != "CrashLoopBackOff"
  THEN emit CrashLoop signal
```

**What it detects**:
- Only persistent crash patterns (multiple crashes)
- Kubernetes has determined the pod is repeatedly failing
- Always indicates a problem needing intervention

### Current Redundancy

**Scenario**: Pod crashes repeatedly

```
Timeline of events:

T=0:  First crash (RestartCount: 0 → 1, exitCode=1)
      PodCrash: ✓ EMIT
      CrashLoop: No (not in backoff yet)

T=30: Second crash (RestartCount: 1 → 2, exitCode=1)
      PodCrash: Suppressed (within 15min TTL)
      CrashLoop: No (not in backoff yet)

T=60: Third crash, Kubernetes applies backoff
      PodCrash: Suppressed (within 15min TTL)
      CrashLoop: ✓ EMIT (transition to CrashLoopBackOff)

Result: Both PodCrash and CrashLoop fired for same root cause
```

## Proposed Solution

### Remove PodCrash Detector

**Core change**: Delete PodCrashDetector entirely, rely solely on CrashLoop detection

**Rationale**:
1. **CrashLoop is sufficient**: It catches persistent problems that need attention
2. **Eliminates transient noise**: Single crashes that self-heal won't generate alerts
3. **Cleaner signal**: One fault type per persistent problem, not two

### Detection Flow After Change

```
Pod lifecycle with crashes:

Scenario 1: Transient failure (self-heals)
─────────────────────────────────────────
T=0:  Container crashes (OOM, network blip, etc)
      → No notification (transient)
T=5:  Container restarts successfully
      → Confirmed: was transient, no human action needed ✓

Scenario 2: Persistent failure (CrashLoop)
──────────────────────────────────────────
T=0:  Container crashes
      → No notification (might be transient)
T=30: Container crashes again
      → No notification (waiting for pattern)
T=60: Container crashes again, Kubernetes applies backoff
      → CrashLoop notification emitted ✓
      → Human intervention needed

Scenario 3: Pod with restartPolicy=Never (gap)
───────────────────────────────────────────────
T=0:  Container crashes
      → No notification (not detected)
      → Pod stays in Failed state
      → Gap: not detected by current detectors

Note: Scenario 3 should be handled by Job failure detection (future work)
```

## Impact Analysis

### Covered Cases (CrashLoop handles these)

✅ **Persistent pod crashes** (most important)
- Repeated crashes with backoff
- Primary failure mode for long-running services
- Always actionable

✅ **StatefulSet crashes**
- Same persistent pattern detection
- Critical for stateful services

✅ **DaemonSet crashes**
- Node-level service failures
- Important for infrastructure components

### Gap Cases (Not detected after change)

⚠️ **Jobs/CronJobs with restartPolicy=Never**
- Single crash, no restart
- Pod stays in Failed state
- **Mitigation**: Should use Job-specific failure detection (check .status.failed, .status.conditions)

⚠️ **Pods with restartPolicy=OnFailure that fail once**
- Similar to Never, but will retry
- May eventually enter CrashLoop if persistent
- **Mitigation**: Rare case, usually becomes CrashLoop if truly broken

⚠️ **Init container failures**
- Single init container crash may prevent pod from starting
- **Mitigation**: Usually results in waiting state or eventually CrashLoop

### Noise Reduction Analysis

**Current state** (with PodCrash):
```
Transient failure rate: 10 crashes/hour (self-healing)
Persistent failure rate: 1 CrashLoop/hour
Total notifications: 11/hour (10 PodCrash + 1 CrashLoop)
Actionable: 1/hour (only CrashLoop)
Noise ratio: 90% noise (10/11)
```

**Proposed state** (without PodCrash):
```
Transient failure rate: 10 crashes/hour (ignored)
Persistent failure rate: 1 CrashLoop/hour
Total notifications: 1/hour (CrashLoop only)
Actionable: 1/hour
Noise ratio: 0% noise
```

## Implementation Strategy

### Phase 1: Remove Code (This Change)

1. **Delete detector implementation**:
   - Remove `pkg/events/detectors/pod_crash.go`
   - Remove `pkg/events/detectors/pod_crash_test.go`

2. **Remove constant**:
   - Delete `FaultTypePodCrash` from `pkg/events/fault_signal.go`

3. **Unregister detector**:
   - Remove `detectors.NewPodCrashDetector()` from `pkg/mcp/mcp.go` line 136

4. **Update tests**:
   - Remove all tests expecting PodCrash signals
   - Update test cases to only validate CrashLoop detection

5. **Update spec**:
   - Remove PodCrash scenarios from kubernetes-event-streaming spec
   - Update fault type list to exclude PodCrash

### Phase 2: Future Enhancements (Follow-up Changes)

**Not in this change**, but acknowledged as future work:

1. **Job/CronJob failure detection**
   - Dedicated detector for Job.status.failed
   - Handles restartPolicy=Never/OnFailure semantics
   - Checks Job conditions (Complete, Failed)

2. **Init container failure detection**
   - Separate signal for init container failures
   - Distinguish from main container crashes

3. **Deployment-level health**
   - Aggregate pod crash patterns at Deployment level
   - Detect when multiple replicas are failing

## Alternative Approaches Considered

### Alternative 1: Make PodCrash conditional on restartPolicy

**Approach**: Only emit PodCrash for pods with restartPolicy=Never

**Why rejected**:
- Adds complexity (need to check pod.spec.restartPolicy)
- Still doesn't handle Jobs properly (they have restartPolicy but different failure semantics)
- Better to have dedicated Job detector than conditional PodCrash

### Alternative 2: Wait-and-see approach

**Approach**: When PodCrash detected, wait 30 seconds and check if pod recovered before emitting

**Why rejected**:
- Requires maintaining state and timers (complex)
- Delayed detection (30s lag)
- Still doesn't solve the fundamental problem of overlap with CrashLoop
- CrashLoop detection already has the "wait and see" built in (Kubernetes decides)

### Alternative 3: Hierarchical detection

**Approach**: Emit PodCrash first, then "upgrade" to CrashLoop if crashes continue

**Why rejected**:
- Creates dependency between detectors (complex coupling)
- Still emits noise for transient failures
- Users would need to correlate two fault IDs for the same problem
- CrashLoop already captures the complete picture

## Risks and Mitigations

### Risk 1: Missing critical single-crash failures

**Scenario**: A pod with restartPolicy=Never crashes and stays down, no notification

**Likelihood**: Low (most production pods use Always or are Jobs)

**Impact**: Medium (failure not detected automatically)

**Mitigation**:
- Document gap in release notes
- Plan Job-specific detector for next iteration
- Users can build custom detectors for specific needs

### Risk 2: Downstream systems depend on PodCrash

**Scenario**: Nightcrier or other systems have logic specific to PodCrash fault type

**Likelihood**: Low (PodCrash recently added, CrashLoop is more established)

**Impact**: Low (CrashLoop covers the actionable cases)

**Mitigation**:
- Document breaking change
- Ensure CrashLoop detection is robust and tested
- Downstream should prefer CrashLoop anyway (indicates persistent problem)

### Risk 3: Loss of early warning signal

**Scenario**: User wants to know about first crash before CrashLoop develops

**Likelihood**: Medium (some users may want early visibility)

**Impact**: Low (transient crashes are not actionable anyway)

**Mitigation**:
- If early warning needed, users should implement application-level health checks
- Consider metrics/logging for crash visibility without alerting
- CrashLoop typically develops quickly (1-2 minutes) so delay is minimal

## Success Criteria

1. **Noise reduction**: Zero notifications for transient container crashes that self-heal
2. **Coverage maintained**: All persistent crash patterns still detected via CrashLoop
3. **No test regressions**: Existing CrashLoop tests continue to pass
4. **Clean codebase**: No dead code or unused constants remaining
5. **Spec alignment**: Spec accurately reflects single fault type per persistent problem
