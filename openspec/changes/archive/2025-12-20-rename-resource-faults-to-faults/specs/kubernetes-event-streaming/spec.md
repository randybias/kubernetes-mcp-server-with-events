# kubernetes-event-streaming - Rename resource-faults to faults

## REMOVED Requirements

### Requirement: Fault Subscription Tools

The server SHALL support a `mode="faults"` flow that automatically focuses on Warning events targeting Pods. Fault subscriptions MUST reuse the same lifecycle and filter semantics as flexible subscriptions but SHALL reject `type="Normal"`. Fault subscriptions SHALL only notify on Warning events that occur after the subscription is created, not historical warnings. The subscription response MUST clearly indicate that the mode is `faults` so clients know to expect enriched notifications.

#### Scenario: Create fault subscription
- **WHEN** a client calls `events_subscribe` with `mode="faults"`, `namespaceSelector=["prod-*"]`, and `labelSelector="app=payments"`
- **THEN** the tool returns a `subscriptionId` tied to `mode="faults"` and begins monitoring Warning events that match the namespace/label filters from the current point in time forward.

#### Scenario: Historical fault events are filtered out
- **GIVEN** pods with historical FailedMount warnings from 2 hours ago
- **WHEN** a client creates a fault subscription
- **THEN** the client receives NO notifications for the 2-hour-old FailedMount events.

#### Scenario: New fault events are delivered with logs
- **GIVEN** an active fault subscription
- **WHEN** a pod generates a new FailedMount warning after subscription creation
- **THEN** the client receives a fault notification with container logs for that specific warning event.

### Requirement: Fault Notifications and Log Enrichment

For `mode="faults"` subscriptions, the server SHALL fetch container logs from the relevant Kubernetes API and attach them to notifications. Notifications MUST be published using method `notifications/message` with `logger="kubernetes/faults"` and `level="warning"`. The `data` field MUST include the same `event` fields as the flexible stream plus a `logs` array. For each container, the server MUST attempt to capture both the current logs and `--previous` logs (when available), annotate whether a panic/error signature was detected, and truncate the sample to a maximum of 10KB per container (configurable via `--max-log-bytes-per-container`). A maximum of 5 containers per pod SHALL be included in a single notification (configurable via `--max-containers-per-notification`). Duplicate Warning events for the same `<cluster>/<namespace>/<pod>/<reason>/<count>` combination SHALL NOT trigger redundant log fetches within a 60-second deduplication window. If logs cannot be retrieved (e.g., RBAC denied, pod gone), the payload MUST include an error entry describing the failure instead of silently omitting logs.

#### Scenario: Provide crashloop log bundle
- **WHEN** a Pod emits a `Warning` event with reason `BackOff`
- **THEN** the `notifications/message` payload contains `logger="kubernetes/faults"`, `level="warning"`, and a `data` object with at least one log entry with fields `container`, `previous=false`, `hasPanic` flag (true if `panic:` detected), and a truncated log sample spanning the most recent lines.

#### Scenario: Capture previous container logs
- **WHEN** the same Pod has `--previous` logs available (container restarted)
- **THEN** the notification `data` object includes an additional log entry where `previous=true`, enabling the agent to inspect the failing run.

#### Scenario: Log capture error reporting
- **WHEN** the Kubernetes API returns `Forbidden` while fetching logs
- **THEN** the notification `data` object contains a log entry with `error="forbidden"` (and no sample), allowing the client to react appropriately.

#### Scenario: Deduplicate repeated events
- **WHEN** identical Warning events fire within the 60-second deduplication window
- **THEN** the server emits at most one notification with logs for that combination to avoid flooding the client.

## MODIFIED Requirements

### Requirement: Resource-Based Fault Subscription Mode

The server SHALL support a `mode="faults"` subscription that watches Kubernetes resources (Pods, Nodes, Deployments, Jobs) directly using SharedIndexInformers instead of v1.Event resources. This mode MUST detect faults through state transitions (edge-triggered) rather than Event emission. The subscription response MUST indicate `mode="faults"` so clients know to expect resource-based notification payloads.

#### Scenario: Create faults subscription
- **WHEN** a client calls `events_subscribe` with `mode="faults"` and namespace/label filters
- **THEN** the server starts SharedIndexInformers for relevant resource types
- **AND** returns a `subscriptionId` with `mode="faults"`
- **AND** begins monitoring for state-based fault signals.

#### Scenario: Detect pod crash via state transition
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Pod's `RestartCount` increases from N to N+1 with a `Terminated` state containing an error
- **THEN** the server emits a fault notification with `faultType="PodCrash"`
- **AND** the notification includes the termination message if available.

#### Scenario: Detect CrashLoopBackOff state
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Pod container enters `Waiting` state with `Reason="CrashLoopBackOff"`
- **THEN** the server emits a fault notification with `faultType="CrashLoop"`
- **AND** subsequent CrashLoopBackOff signals for the same container are deduplicated as a single active incident.

### Requirement: Node and Controller Fault Detection

The server SHALL detect faults in Node and controller resources when `mode="faults"` is active. Node faults MUST be detected when the `Ready` condition transitions from `True` to `False` or `Unknown`. Deployment faults MUST be detected when `ProgressDeadlineExceeded` condition becomes true. Job faults MUST be detected when the `Failed` condition is set.

#### Scenario: Detect node becoming unhealthy
- **GIVEN** an active `mode="faults"` subscription
- **WHEN** a Node's `Ready` condition changes from `True` to `False`
- **THEN** the server emits a fault notification with `faultType="NodeUnhealthy"`
- **AND** the notification includes the condition reason and message.

#### Scenario: Detect deployment progress deadline exceeded
- **GIVEN** an active `mode="faults"` subscription watching a namespace with a Deployment
- **WHEN** the Deployment gains a `ProgressDeadlineExceeded` condition
- **THEN** the server emits a fault notification with `faultType="DeploymentFailure"`
- **AND** the notification includes the deployment name, namespace, and failure reason.

#### Scenario: Detect job failure
- **GIVEN** an active `mode="faults"` subscription
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

For `mode="faults"` subscriptions, the server SHALL emit notifications using `notifications/message` with `logger="kubernetes/faults"` and `level="warning"`. The `data` field MUST include: `subscriptionId`, `cluster`, `faultType` (PodCrash, CrashLoop, NodeUnhealthy, DeploymentFailure, JobFailure), `severity` (info, warning, critical), `resource` object (apiVersion, kind, name, namespace, uid), `context` (termination message or logs if fetched), and RFC3339 `timestamp`. The payload structure is optimized for state-based fault signals.

#### Scenario: Resource fault notification payload
- **WHEN** a Pod crash is detected
- **THEN** the notification contains `logger="kubernetes/faults"`, `level="warning"`
- **AND** `data` includes `faultType="PodCrash"`, `severity="warning"`, resource reference, and context from termination message.

### Requirement: Semantic Fault Deduplication

For `mode="faults"` subscriptions, the server SHALL deduplicate fault signals using a semantic key based on `(faultType, resourceUID, containerName)` rather than Event UID/Count. Recurring fault signals (such as repeated CrashLoopBackOff) SHALL be grouped into a single "active incident" to prevent notification storms. A fault incident SHALL be considered resolved when the underlying condition clears (e.g., Pod transitions from CrashLoopBackOff to Running).

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

### Requirement: Notification Logger Namespacing

The server SHALL use distinct `logger` values to categorize notifications:
- `"kubernetes/events"` for flexible event stream notifications (`mode=events`)
- `"kubernetes/faults"` for resource-based fault notifications (`mode=faults`)
- `"kubernetes/subscription_error"` for subscription error notifications (watch failures, degraded state)

Clients MAY filter incoming `notifications/message` by `logger` to separate event notifications from other logging traffic.

#### Scenario: Filter notifications by logger
- **WHEN** a client receives multiple `notifications/message` payloads
- **THEN** the client can inspect the `logger` field to determine if the notification is an event (`kubernetes/events`), fault (`kubernetes/faults`), subscription error (`kubernetes/subscription_error`), or unrelated logging.
