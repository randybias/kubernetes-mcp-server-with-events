# Tasks: Refactor Fault Detection for High Signal/Low Noise

Each phase delivers a testable increment. Do not proceed to the next phase until verification passes.

## Phase 1: Informer Foundation
Goal: Prove we can receive Pod updates via informer.

- [x] 1.1 Create worktree with `wtp add -b feature/resource-fault-detection`
- [x] 1.2 Create `pkg/events/resource_watcher.go` with SharedInformerFactory setup
- [x] 1.3 Add Pod informer registration with simple OnUpdate callback that logs
- [x] 1.4 Write `resource_watcher_test.go` using envtest to verify Pod updates are received
- [x] 1.5 **VERIFY**: `go test -v -run TestResourceWatcher` passes

## Phase 2: Fault Signal Types
Goal: Define the data structures for fault detection.

- [x] 2.1 Define `FaultSignal` struct in `pkg/events/fault_signal.go`:
      - FaultType (PodCrash, CrashLoop, NodeUnhealthy, DeploymentFailure, JobFailure)
      - ResourceUID, Kind, Name, Namespace
      - ContainerName (for pod faults)
      - Severity (info, warning, critical)
      - Context (termination message or logs)
      - Timestamp
- [x] 2.2 Define `Detector` interface: `Detect(oldObj, newObj) []FaultSignal`
- [x] 2.3 Write unit tests for FaultSignal construction and validation
- [x] 2.4 **VERIFY**: `go test -v -run TestFaultSignal` passes

## Phase 3: Pod Crash Detection
Goal: Detect single pod crashes via RestartCount increase.

- [x] 3.1 Implement `PodCrashDetector` in `pkg/events/detectors/pod_crash.go`
      - Compare oldPod vs newPod RestartCount for each container
      - Return FaultSignal when RestartCount increases AND state is Terminated with error
- [x] 3.2 Write test with mock old/new Pod objects covering:
      - RestartCount 0->1 with error exit code -> signal emitted
      - RestartCount 0->1 with exit code 0 -> no signal (graceful restart)
      - RestartCount unchanged -> no signal
- [x] 3.3 **VERIFY**: `go test -v -run TestPodCrashDetector` passes

## Phase 4: Termination Message Extraction
Goal: Extract context from Pod status before considering log fetches.

- [x] 4.1 Add `extractTerminationMessage(pod, containerName)` function
      - Returns message from `Pod.Status.ContainerStatuses[].State.Terminated.Message`
- [x] 4.2 Integrate into PodCrashDetector: populate FaultSignal.Context with termination message
- [x] 4.3 Write test verifying termination message appears in FaultSignal.Context
- [x] 4.4 **VERIFY**: `go test -v -run TestTerminationMessage` passes

## Phase 5: CrashLoop Detection
Goal: Detect CrashLoopBackOff state transitions.

- [x] 5.1 Implement `CrashLoopDetector` in `pkg/events/detectors/crashloop.go`
      - Detect `Waiting.Reason == "CrashLoopBackOff"` in newPod but not oldPod
      - Set Severity to "critical" for CrashLoop
- [x] 5.2 Write test covering:
      - Transition into CrashLoopBackOff -> signal emitted
      - Already in CrashLoopBackOff (no transition) -> no signal
      - Transition out of CrashLoopBackOff -> no signal (or resolution signal, TBD)
- [x] 5.3 **VERIFY**: `go test -v -run TestCrashLoopDetector` passes

## Phase 6: Semantic Deduplication
Goal: Prevent notification storms from recurring signals.

- [x] 6.1 Create `FaultDeduplicator` in `pkg/events/fault_dedup.go`
      - Key: `(faultType, resourceUID, containerName)`
      - Track "active incidents" with TTL
- [x] 6.2 Write test covering:
      - First signal for key -> passes through
      - Second signal for same key within window -> deduplicated
      - Signal after TTL expires -> passes through as new incident
- [x] 6.3 **VERIFY**: `go test -v -run TestFaultDeduplicator` passes

## Phase 7: Conditional Log Fetching
Goal: Only fetch logs when termination message is empty AND severity is high.

- [x] 7.1 Create `FaultContextEnricher` that decides whether to fetch logs
      - If FaultSignal.Context is non-empty -> skip log fetch
      - If Severity < "critical" -> skip log fetch
      - Otherwise -> fetch logs using existing LogCaptureWorkerPool
- [x] 7.2 Write test covering:
      - Has termination message -> no log fetch
      - No termination message + CrashLoop (critical) -> log fetch triggered
      - No termination message + single crash (warning) -> no log fetch
- [x] 7.3 **VERIFY**: `go test -v -run TestFaultContextEnricher` passes

## Phase 8: Wire ResourceWatcher to Detectors
Goal: Connect informer callbacks to detector pipeline.

- [x] 8.1 Add detector registry to ResourceWatcher
- [x] 8.2 Wire OnUpdate callback to run all registered detectors
- [x] 8.3 Add deduplicator and context enricher to pipeline
- [x] 8.4 Write integration test: create Pod, trigger crash, verify FaultSignal emitted
- [x] 8.5 **VERIFY**: `go test -v -run TestResourceWatcherIntegration` passes

## Phase 9: Node Fault Detection
Goal: Detect node health transitions.

- [x] 9.1 Add Node informer to ResourceWatcher
- [x] 9.2 Implement `NodeUnhealthyDetector` - Ready condition True->False/Unknown
- [x] 9.3 Write test with mock Node objects
- [x] 9.4 **VERIFY**: `go test -v -run TestNodeUnhealthyDetector` passes

## Phase 10: Controller Fault Detection
Goal: Detect Deployment and Job failures.

- [x] 10.1 Add Deployment and Job informers to ResourceWatcher
- [x] 10.2 Implement `DeploymentFailureDetector` - ProgressDeadlineExceeded condition
- [x] 10.3 Implement `JobFailureDetector` - Failed condition
- [x] 10.4 Write tests for both detectors
- [x] 10.5 **VERIFY**: `go test -v -run TestDeploymentFailureDetector && go test -v -run TestJobFailureDetector` passes

## Phase 11: Subscription Mode Integration
Goal: Expose resource-faults as a subscription mode.

- [x] 11.1 Add `mode="resource-faults"` option to `events_subscribe` tool
- [x] 11.2 Create notification builder for resource-based faults
      - Logger: `kubernetes/resource-faults`
      - Level: `warning`
      - Data: faultType, severity, resource, context, timestamp
- [x] 11.3 Wire FaultSignal output to notification emission
- [x] 11.4 Write integration test: subscribe with mode=resource-faults, trigger crash, verify notification
- [x] 11.5 **VERIFY**: `go test -v -run TestResourceFaultsSubscription` passes

## Phase 12: End-to-End Verification
Goal: Full manual verification against real cluster.

- [x] 12.1 Run MCP server with `--port` against Kind cluster
- [x] 12.2 Create subscription with `mode="resource-faults"`
- [x] 12.3 Deploy crashing pod, verify notification received with termination message
- [x] 12.4 Deploy CrashLoop pod, verify single notification (not storm)
- [x] 12.5 Cordon node (simulated unhealthy), verify node fault notification
- [x] 12.6 **VERIFY**: All manual tests pass, document results

## Phase 13: Documentation
Goal: Update docs and consider deprecation.

- [x] 13.1 Update README with `mode="resource-faults"` documentation
- [x] 13.2 Update `pkg/events/README.md` with architecture diagram
- [x] 13.3 Decision checkpoint: deprecate `mode="faults"` or keep both?
- [x] 13.4 **VERIFY**: Documentation reviewed and accurate
