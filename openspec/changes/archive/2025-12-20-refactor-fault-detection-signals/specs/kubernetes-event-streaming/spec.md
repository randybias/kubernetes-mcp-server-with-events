# kubernetes-event-streaming - Resource-Based Fault Detection Delta

## ADDED Requirements

### Requirement: Resource-Based Fault Subscription Mode

The server SHALL support a `mode="resource-faults"` subscription that watches Kubernetes resources (Pods, Nodes, Deployments, Jobs) directly using SharedIndexInformers instead of v1.Event resources. This mode MUST detect faults through state transitions (edge-triggered) rather than Event emission. The subscription response MUST indicate `mode="resource-faults"` so clients know to expect resource-based notification payloads.

#### Scenario: Create resource-faults subscription
- **WHEN** a client calls `events_subscribe` with `mode="resource-faults"` and namespace/label filters
- **THEN** the server starts SharedIndexInformers for relevant resource types
- **AND** returns a `subscriptionId` with `mode="resource-faults"`
- **AND** begins monitoring for state-based fault signals.

#### Scenario: Detect pod crash via state transition
- **GIVEN** an active `mode="resource-faults"` subscription
- **WHEN** a Pod's `RestartCount` increases from N to N+1 with a `Terminated` state containing an error
- **THEN** the server emits a fault notification with `faultType="PodCrash"`
- **AND** the notification includes the termination message if available.

#### Scenario: Detect CrashLoopBackOff state
- **GIVEN** an active `mode="resource-faults"` subscription
- **WHEN** a Pod container enters `Waiting` state with `Reason="CrashLoopBackOff"`
- **THEN** the server emits a fault notification with `faultType="CrashLoop"`
- **AND** subsequent CrashLoopBackOff signals for the same container are deduplicated as a single active incident.

### Requirement: Node and Controller Fault Detection

The server SHALL detect faults in Node and controller resources when `mode="resource-faults"` is active. Node faults MUST be detected when the `Ready` condition transitions from `True` to `False` or `Unknown`. Deployment faults MUST be detected when `ProgressDeadlineExceeded` condition becomes true. Job faults MUST be detected when the `Failed` condition is set.

#### Scenario: Detect node becoming unhealthy
- **GIVEN** an active `mode="resource-faults"` subscription
- **WHEN** a Node's `Ready` condition changes from `True` to `False`
- **THEN** the server emits a fault notification with `faultType="NodeUnhealthy"`
- **AND** the notification includes the condition reason and message.

#### Scenario: Detect deployment progress deadline exceeded
- **GIVEN** an active `mode="resource-faults"` subscription watching a namespace with a Deployment
- **WHEN** the Deployment gains a `ProgressDeadlineExceeded` condition
- **THEN** the server emits a fault notification with `faultType="DeploymentFailure"`
- **AND** the notification includes the deployment name, namespace, and failure reason.

#### Scenario: Detect job failure
- **GIVEN** an active `mode="resource-faults"` subscription
- **WHEN** a Job's `Failed` condition becomes true
- **THEN** the server emits a fault notification with `faultType="JobFailure"`.

### Requirement: Termination Message Context Extraction

For Pod faults, the server SHALL extract the termination message from `Pod.Status.ContainerStatuses[].State.Terminated.Message` as the primary context source. Log fetching SHALL only be triggered when the termination message is empty or missing AND the fault is classified as high-severity (e.g., CrashLoop). This reduces API calls and latency compared to unconditional log fetching.

#### Scenario: Use termination message as primary context
- **WHEN** a Pod crash is detected and the container has a non-empty termination message (e.g., panic trace)
- **THEN** the fault notification includes the termination message as `context`
- **AND** no container log fetch is performed.

#### Scenario: Fetch logs only when termination message is empty
- **WHEN** a CrashLoop is detected and the container has no termination message
- **THEN** the server fetches container logs to provide context
- **AND** the notification includes the fetched logs.

#### Scenario: Skip log fetch for low-severity faults
- **WHEN** a single Pod restart is detected (not CrashLoop) with an empty termination message
- **THEN** the server does NOT fetch logs (low-severity)
- **AND** the notification includes only the fault metadata without log context.

### Requirement: Resource Fault Notification Format

For `mode="resource-faults"` subscriptions, the server SHALL emit notifications using `notifications/message` with `logger="kubernetes/resource-faults"` and `level="warning"`. The `data` field MUST include: `subscriptionId`, `cluster`, `faultType` (PodCrash, CrashLoop, NodeUnhealthy, DeploymentFailure, JobFailure), `severity` (info, warning, critical), `resource` object (apiVersion, kind, name, namespace, uid), `context` (termination message or logs if fetched), and RFC3339 `timestamp`. The payload structure differs from `mode="faults"` to optimize for state-based signals.

#### Scenario: Resource fault notification payload
- **WHEN** a Pod crash is detected
- **THEN** the notification contains `logger="kubernetes/resource-faults"`, `level="warning"`
- **AND** `data` includes `faultType="PodCrash"`, `severity="warning"`, resource reference, and context from termination message.

#### Scenario: Distinguish resource-faults from event-based faults
- **WHEN** the server has both `mode="faults"` and `mode="resource-faults"` subscriptions active
- **THEN** event-based faults use `logger="kubernetes/faults"`
- **AND** resource-based faults use `logger="kubernetes/resource-faults"`
- **AND** clients can filter by logger to receive only their preferred signal type.

### Requirement: Semantic Fault Deduplication

For `mode="resource-faults"` subscriptions, the server SHALL deduplicate fault signals using a semantic key based on `(faultType, resourceUID, containerName)` rather than Event UID/Count. Recurring fault signals (such as repeated CrashLoopBackOff) SHALL be grouped into a single "active incident" to prevent notification storms. A fault incident SHALL be considered resolved when the underlying condition clears (e.g., Pod transitions from CrashLoopBackOff to Running).

#### Scenario: Group recurring CrashLoop signals
- **GIVEN** a Pod repeatedly entering CrashLoopBackOff state
- **WHEN** multiple CrashLoop signals fire within the deduplication window
- **THEN** the server emits only one notification for the active incident
- **AND** subsequent signals update the incident internally without new notifications.

#### Scenario: Emit resolved signal when fault clears
- **GIVEN** an active CrashLoop incident for a Pod
- **WHEN** the Pod transitions to `Running` state and remains stable
- **THEN** the server MAY emit a resolution notification with `resolved=true`
- **AND** the incident is closed, allowing new incidents to be created if the fault recurs.

## MODIFIED Requirements

### Requirement: Notification Logger Namespacing

The server SHALL use distinct `logger` values to categorize notifications:
- `"kubernetes/events"` for flexible event stream notifications (`mode=events`)
- `"kubernetes/faults"` for fault notifications with log enrichment (`mode=faults`)
- `"kubernetes/resource-faults"` for resource-based fault notifications (`mode=resource-faults`)
- `"kubernetes/subscription_error"` for subscription error notifications (watch failures, degraded state)

Clients MAY filter incoming `notifications/message` by `logger` to separate event notifications from other logging traffic.

#### Scenario: Filter notifications by logger
- **WHEN** a client receives multiple `notifications/message` payloads
- **THEN** the client can inspect the `logger` field to determine if the notification is an event (`kubernetes/events`), event-based fault (`kubernetes/faults`), resource-based fault (`kubernetes/resource-faults`), subscription error (`kubernetes/subscription_error`), or unrelated logging.
