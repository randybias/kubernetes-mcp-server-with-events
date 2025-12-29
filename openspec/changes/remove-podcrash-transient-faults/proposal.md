# Change: Remove PodCrash Detection to Eliminate Transient Fault Noise

## Why

The current PodCrash detector emits fault notifications for every container crash, including transient failures that Kubernetes automatically recovers from. This creates noise and false positives, making it difficult to distinguish between:

- **Transient faults**: Container crashes once, Kubernetes restarts it, pod runs successfully (self-healed, not actionable)
- **Hard faults**: Container crashes repeatedly, enters CrashLoopBackOff state (persistent problem, requires intervention)

Users reported seeing duplicate notifications for the same underlying issue:
- First notification: PodCrash (restartCount increased)
- Second notification: CrashLoop (entered CrashLoopBackOff state)

The PodCrash signal is redundant when CrashLoop detection exists, and it fires on transient failures that don't require human attention.

## What Changes

**Remove PodCrash detection entirely:**
- Delete `pkg/events/detectors/pod_crash.go` and `pod_crash_test.go`
- Remove `FaultTypePodCrash` constant from `pkg/events/fault_signal.go`
- Remove `detectors.NewPodCrashDetector()` from fault detector registration in `pkg/mcp/mcp.go`
- Update spec to remove PodCrash scenarios and references
- Update tests to remove PodCrash expectations

**Keep CrashLoop detection:**
- CrashLoop detector remains active, detecting when pods enter CrashLoopBackOff state
- This catches persistent crash patterns that need intervention
- Eliminates noise from single crashes that self-heal

## Impact on Fault Detection

### Before (Current Behavior)
```
T=0:  Container crashes
      → PodCrash notification emitted ✓
T=5:  Container starts successfully
      → No notification (self-healed)

T=60: Container crashes again
      → PodCrash notification emitted ✓
T=65: Container crashes again
      → PodCrash suppressed (within 15min TTL)
T=70: Kubernetes applies backoff
      → CrashLoop notification emitted ✓
```

**Result**: 2 fault types, 3 initial notifications for same underlying problem

### After (Proposed Behavior)
```
T=0:  Container crashes
      → No notification (transient)
T=5:  Container starts successfully
      → Confirmed transient, no noise

T=60: Container crashes again
      → No notification (waiting for pattern)
T=65: Container crashes again
      → No notification (waiting for backoff)
T=70: Kubernetes applies backoff
      → CrashLoop notification emitted ✓
```

**Result**: 1 fault type, 1 notification only when persistent problem confirmed

## What About Edge Cases?

**Q: What if a pod has `restartPolicy=Never` and crashes once?**

A: Not handled by this change. This is acknowledged as a gap that should be addressed in a future change focused on Job/CronJob failure detection patterns (pods with Never/OnFailure restart policies have different semantics).

**Q: What if there's a single crash that causes data corruption or other side effects?**

A: If the crash is severe enough to warrant investigation of a single occurrence, it should be detected through other signals (e.g., application-specific health checks, monitoring, or user reports). The fault detector is focused on persistent infrastructure-level problems.

## Design Decisions

### Decision 1: Remove PodCrash entirely rather than making it conditional

**What**: Delete PodCrash detector completely instead of making it conditional (e.g., only fire if restartPolicy=Never)

**Why**:
- Simpler implementation (less conditional logic)
- CrashLoop already handles the primary use case (persistent crashes with restartPolicy=Always)
- restartPolicy=Never is typically used for Jobs, which should have dedicated Job failure detection
- Clean separation: CrashLoop for daemon/service faults, Job detectors for batch workload faults

**Trade-off**: We acknowledge a gap for restartPolicy=Never pods that crash once, but this is deemed acceptable because:
- It's a rare case (most production workloads use Always or are Jobs)
- It can be addressed in a future Job-specific detector enhancement
- Eliminating transient noise is more valuable than catching this edge case

### Decision 2: No transitional period or feature flag

**What**: Remove PodCrash detection immediately without a deprecation period or opt-in/opt-out flag

**Why**:
- This is a bug fix (removing unwanted behavior), not a feature removal
- Users are complaining about noise, not requesting PodCrash specifically
- Keeping both detectors active creates confusion about which signal to act on
- Simpler codebase without feature flags

## Breaking Changes

**Behavior change**: Downstream systems (like nightcrier) that expect PodCrash fault notifications will stop receiving them.

**Mitigation**:
- PodCrash was recently added, so minimal downstream dependency expected
- CrashLoop notifications will still fire for persistent problems
- Downstream should be monitoring CrashLoop events anyway (more actionable)
- If specific users need single-crash detection, they can build custom detectors

**Migration path**: None needed. CrashLoop detection covers the actionable failure scenarios.
