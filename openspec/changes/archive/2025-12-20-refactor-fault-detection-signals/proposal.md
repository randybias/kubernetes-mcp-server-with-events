# Change: Refactor Fault Detection for High Signal/Low Noise

## Why

The current fault detection system uses v1.Event watching to detect issues and then performs expensive log lookups to enrich notifications. This approach has fundamental problems:

1. **Kubernetes Events are noisy and unreliable** - Events are "best-effort" delivery, often delayed, and produce many duplicates during fault scenarios
2. **Log enrichment creates backpressure** - Each Warning event triggers 7-12 sequential API calls (pod GET + log fetches per container), causing the event result channel to overflow and drop events during high-volume fault scenarios
3. **Redundant API calls** - The system fetches logs when the information is often already available in `Pod.Status.ContainerStatuses[].State.Terminated.Message` (termination messages include panic traces)
4. **Missing fault types** - Event-based detection misses important fault signals like Node failures and Deployment progress deadline exceeded

## What Changes

This proposal refactors fault detection from **Event-based triggering** to **State-based triggering** using Kubernetes Informers:

- **Add ResourceWatcher using SharedIndexInformers** - Watch Pod, Node, Deployment, and Job resources directly instead of v1.Event resources
- **Implement edge-triggered fault detection** - Detect specific state transitions (RestartCount increases, CrashLoopBackOff, Node Ready condition flips, ProgressDeadlineExceeded) instead of filtering Warning events
- **Cheap Context strategy** - Extract termination messages from Pod status first; only fetch logs when termination message is empty AND fault is high-severity
- **Semantic deduplication** - Deduplicate by fault episode identity `(FaultType, ResourceUID, ContainerName)` rather than Event UID/Count

**Note**: This change extends the existing event streaming capability with a new resource-based fault detection mode. The existing `mode="faults"` (Event-based) will be preserved for backwards compatibility but may be deprecated in future.

## Impact

- Affected specs: `kubernetes-event-streaming`
- Affected code: `pkg/events/` (significant refactoring)
- New components: `ResourceWatcher`, fault detectors, informer factory setup
- Existing components retained: `DeduplicationCache` (with new key strategy), `LogCaptureWorkerPool` (for rare cases)
- **BREAKING**: New `mode="resource-faults"` will have different notification payload structure optimized for state-based signals
