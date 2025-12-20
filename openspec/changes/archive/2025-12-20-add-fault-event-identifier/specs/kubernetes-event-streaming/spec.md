## MODIFIED Requirements

### Requirement: Resource Fault Notification Format

For `mode="faults"` subscriptions, the server SHALL emit notifications using `notifications/message` with `logger="kubernetes/faults"` and `level="warning"`. The `data` field MUST include: `subscriptionId`, `cluster`, `faultId` (stable deterministic identifier for the fault condition), `faultType` (PodCrash, CrashLoop, NodeUnhealthy, DeploymentFailure, JobFailure), `severity` (info, warning, critical), `resource` object (apiVersion, kind, name, namespace, uid), `context` (termination message or logs if fetched), and RFC3339 `timestamp`. The `faultId` MUST be generated deterministically from `(cluster, faultType, resourceUID, containerName)` such that the same fault condition always produces the same identifier across re-emissions. The payload structure is optimized for state-based fault signals and downstream deduplication.

#### Scenario: Resource fault notification payload
- **WHEN** a Pod crash is detected
- **THEN** the notification contains `logger="kubernetes/faults"`, `level="warning"`
- **AND** `data` includes `faultId` (deterministic identifier), `faultType="PodCrash"`, `severity="warning"`, resource reference, and context from termination message.

#### Scenario: Fault ID is stable across re-emissions
- **GIVEN** a fault condition that was first detected at time T
- **WHEN** the deduplication TTL expires and the fault is re-emitted at time T+16min
- **THEN** the `faultId` in both notifications is identical
- **AND** downstream systems can use `faultId` to recognize this as the same fault condition.

#### Scenario: Fault ID is unique per fault condition
- **GIVEN** two different pods crash-looping in the same cluster
- **WHEN** fault notifications are emitted for both pods
- **THEN** each notification has a distinct `faultId`
- **AND** the `faultId` values differ because the resourceUID differs.

#### Scenario: Fault ID incorporates cluster for multi-cluster uniqueness
- **GIVEN** two clusters ("dev" and "prod") each with a pod having the same name but different UIDs
- **WHEN** both pods experience CrashLoop faults
- **THEN** the `faultId` values are different because the cluster name is part of the generation.

### Requirement: Semantic Fault Deduplication

For `mode="faults"` subscriptions, the server SHALL deduplicate fault signals using a semantic key based on `(faultType, resourceUID, containerName)` rather than Event UID/Count. This key is termed the "fault condition" and identifies the underlying problem. Recurring fault signals (such as repeated CrashLoopBackOff) SHALL be grouped into a single active fault condition to prevent notification storms. A fault condition SHALL be considered resolved when the underlying problem clears (e.g., Pod transitions from CrashLoopBackOff to Running). The internal terminology SHALL use "fault condition" rather than "incident" to distinguish from downstream incident tracking systems.

#### Scenario: Group recurring CrashLoop signals
- **GIVEN** a Pod repeatedly entering CrashLoopBackOff state
- **WHEN** multiple CrashLoop signals fire within the deduplication window
- **THEN** the server emits only one notification for the active fault condition
- **AND** subsequent signals update the condition internally without new notifications.

#### Scenario: Emit resolved signal when fault clears
- **GIVEN** an active CrashLoop fault condition for a Pod
- **WHEN** the Pod transitions to `Running` state and remains stable
- **THEN** the server MAY emit a resolution notification with `resolved=true`
- **AND** the fault condition is closed, allowing new conditions to be created if the fault recurs.
