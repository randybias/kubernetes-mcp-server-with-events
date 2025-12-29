## REMOVED Requirements

### ~~Requirement: Detect pod crash via state transition~~

**Removed because**: PodCrash detection creates noise by alerting on transient failures that self-heal. CrashLoop detection already covers persistent pod failure patterns.

**Original scenario**:
- ~~**GIVEN** an active `mode="faults"` subscription~~
- ~~**WHEN** a Pod's `RestartCount` increases from N to N+1 with a `Terminated` state containing an error~~
- ~~**THEN** the server emits a fault notification with `faultType="PodCrash"`~~
- ~~**AND** the notification includes the termination message if available.~~

## MODIFIED Requirements

### Requirement: Resource-Based Fault Subscription Mode

The server SHALL support a `mode="faults"` subscription that watches Kubernetes resources (Pods, Nodes, Deployments, Jobs) directly using SharedIndexInformers instead of v1.Event resources. This mode MUST detect faults through state transitions (edge-triggered) rather than Event emission. For pods, the server SHALL detect CrashLoopBackOff state (indicating persistent crash pattern) rather than individual container restarts, reducing noise from transient failures. The subscription response MUST indicate `mode="faults"` so clients know to expect resource-based notification payloads.

#### Scenario: Detect CrashLoopBackOff state (clarified)
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Pod container enters `Waiting` state with `Reason="CrashLoopBackOff"`
- **THEN** the server emits a fault notification with `faultType="CrashLoop"`
- **AND** subsequent CrashLoopBackOff signals for the same container are deduplicated as a single active fault condition
- **AND** individual container restarts (without CrashLoopBackOff) do NOT generate notifications (they are considered transient)

### Requirement: Resource Fault Notification Format

For `mode="faults"` subscriptions, the server SHALL emit notifications using `notifications/message` with `logger="kubernetes/faults"` and `level="warning"`. The `data` field MUST include: `subscriptionId`, `cluster`, `faultId` (stable deterministic identifier for the fault condition), `faultType` (CrashLoop, NodeUnhealthy, DeploymentFailure, JobFailure), `severity` (info, warning, critical), `resource` object (apiVersion, kind, name, namespace, uid), `context` (termination message or logs if fetched), and RFC3339 `timestamp`. The `faultId` MUST be generated deterministically from `(cluster, faultType, resourceUID, containerName)` such that the same fault condition always produces the same identifier across re-emissions. The payload structure is optimized for state-based fault signals and downstream deduplication.

#### Scenario: Resource fault notification payload (updated)
- **WHEN** a CrashLoop is detected
- **THEN** the notification contains `logger="kubernetes/faults"`, `level="warning"`
- **AND** `data` includes `faultId` (deterministic identifier), `faultType="CrashLoop"`, `severity="critical"`, resource reference, and context from termination message or logs

**Note**: Changed from `faultType="PodCrash"` with `severity="warning"` to `faultType="CrashLoop"` with `severity="critical"` to reflect focus on persistent failures only.

### Requirement: Termination Message Context Extraction

For Pod faults, the server SHALL extract the termination message from `Pod.Status.ContainerStatuses[].State.Terminated.Message` as the primary context source. Log fetching SHALL only be triggered when the termination message is empty or missing AND the fault is CrashLoop (high-severity). This reduces API calls and latency compared to unconditional log fetching.

#### Scenario: Skip log fetch for low-severity faults (removed)

**Removed because**: With PodCrash gone, there is no "low-severity" pod fault anymore. CrashLoop is always critical.

~~**Original**:~~
- ~~**WHEN** a single Pod restart is detected (not CrashLoop) with an empty termination message~~
- ~~**THEN** the server does NOT fetch logs (low-severity)~~
- ~~**AND** the notification includes only the fault metadata without log context.~~

## ADDED Requirements

### Requirement: Focus on Persistent Pod Failures

The server SHALL only emit pod-related fault notifications for persistent failure patterns, not transient crashes. Individual container restarts that self-heal SHALL NOT generate fault notifications. This design reduces alert noise and focuses on failures that require human intervention.

#### Scenario: Transient crash is not emitted
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Pod container crashes once (RestartCount increases)
- **AND** the container successfully restarts and enters Running state
- **THEN** the server does NOT emit any fault notification
- **AND** no alert is generated for this transient failure

#### Scenario: Persistent crash pattern is emitted
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Pod container crashes repeatedly
- **AND** Kubernetes applies backoff logic and sets `Waiting.Reason="CrashLoopBackOff"`
- **THEN** the server emits a fault notification with `faultType="CrashLoop"`
- **AND** this indicates a persistent problem requiring intervention

#### Scenario: Self-healing does not trigger re-notification
- **GIVEN** a fault notification was emitted for a CrashLoop
- **WHEN** the underlying issue is fixed (e.g., config corrected, dependency restored)
- **AND** the container successfully starts and enters Running state
- **THEN** the CrashLoop state clears
- **AND** subsequent restarts (if any) do not generate new notifications unless CrashLoopBackOff recurs
