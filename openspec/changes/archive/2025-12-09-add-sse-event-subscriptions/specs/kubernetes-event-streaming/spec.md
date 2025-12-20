## ADDED Requirements

### Requirement: Flexible Event Subscription Tools
The Kubernetes MCP server SHALL expose read-only tools that let an MCP client create and cancel Kubernetes event subscriptions while connected over the HTTP/SSE transport. `events_subscribe` MUST accept filter arguments (cluster, namespaces/namespaceSelector, labelSelector, involved object metadata, type, reason) and return a unique `subscriptionId` along with the normalized filters and selected `mode`. `events_unsubscribe` MUST tear down the identified subscription and MAY be invoked multiple times without error.

#### Scenario: Create subscription with filters
- **WHEN** a client connected via SSE calls `events_subscribe` with `cluster="dev"`, `namespace="kube-system"`, and `type="Warning"`
- **THEN** the tool responds with a JSON payload containing a non-empty `subscriptionId`, echoes the normalized filters plus `mode="events"`, and starts watching Kubernetes `Event` resources that match those filters.

#### Scenario: Unsubscribe idempotently
- **WHEN** the same client later calls `events_unsubscribe` with that `subscriptionId`
- **THEN** the server stops emitting notifications for that subscription and returns a success acknowledgement even if the tool is invoked again with the same id.

### Requirement: Client Log Level Prerequisite
Clients MUST call `logging/setLevel` (with level `debug`, `info`, or higher) before receiving any event notifications. This is required because notifications are delivered via the standard MCP `notifications/message` logging mechanism using structured data in the `data` field and a distinguishing `logger` name. If a client has not set a log level, notifications will be silently dropped by the SDK.

#### Scenario: Log level enables notifications
- **WHEN** a client calls `logging/setLevel` with `level="info"` before subscribing
- **THEN** the client receives all `notifications/message` payloads from active subscriptions.

#### Scenario: Missing log level drops notifications
- **WHEN** a client creates a subscription but has not called `logging/setLevel`
- **THEN** no notifications are delivered to that client (they are dropped by the MCP SDK).

### Requirement: Flexible Event Notifications via Logging
For `mode="events"` subscriptions, the server SHALL emit notifications using the standard MCP `notifications/message` method with `logger="kubernetes/events"`. Each notification MUST include `level="info"`, `logger="kubernetes/events"`, and a structured `data` object containing `subscriptionId`, `cluster`, and an `event` object with at least `namespace`, RFC3339 `timestamp`, `type`, `reason`, `message`, labels, and `involvedObject` fields (with `apiVersion`, `kind`, `name`, `namespace`). Notifications MUST be delivered on the SSE stream without requiring the client to make an additional `resources/read` call.

#### Scenario: Deliver raw event payload
- **WHEN** a subscribed namespace receives a `Normal` event about configmap `settings`
- **THEN** the server sends `notifications/message` over SSE with `logger="kubernetes/events"`, `level="info"`, and a `data` object that includes the subscription id, cluster identifier, namespace, `Normal` type, reason, message, label metadata, and the involved object reference.

#### Scenario: Distinguish event notifications from regular logs
- **WHEN** the server emits both diagnostic log messages and event notifications
- **THEN** event notifications use `logger="kubernetes/events"` while diagnostic logs use different logger names, enabling clients to filter by logger.

### Requirement: Fault Subscription Tools
The server SHALL support a `mode="faults"` flow that automatically focuses on Warning events targeting Pods. Fault subscriptions MUST reuse the same lifecycle and filter semantics as flexible subscriptions but SHALL reject `type="Normal"`. The subscription response MUST clearly indicate that the mode is `faults` so clients know to expect enriched notifications.

#### Scenario: Create fault subscription
- **WHEN** a client calls `events_subscribe` with `mode="faults"`, `namespaceSelector=["prod-*"]`, and `labelSelector="app=payments"`
- **THEN** the tool returns a `subscriptionId` tied to `mode="faults"` and begins monitoring Warning events that match the namespace/label filters.

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

### Requirement: Subscription Lifecycle Management
The server SHALL tie each subscription to the originating MCP session. Subscriptions MUST be removed automatically when the client unsubscribes, disconnects, or when the server restarts. While a subscription is active, the server MUST stop the underlying Kubernetes watch if the session closes or errors, and it MUST reject attempts to reuse the same subscription id from another session.

#### Scenario: Cleanup on disconnect
- **WHEN** an SSE client terminates its session without explicitly unsubscribing
- **THEN** the server stops the associated Kubernetes watch, frees the subscription entry, and emits no further notifications for that id.

#### Scenario: Reject cross-session reuse
- **WHEN** a different session tries to call `events_unsubscribe` with a subscription id owned by another session
- **THEN** the tool returns a "not found" style error instead of stopping the original subscription.

#### Scenario: Periodic session validation
- **WHEN** a session closes without the server receiving explicit disconnect notification
- **THEN** the server's periodic session monitor (running every 30 seconds) detects the missing session and cleans up its subscriptions.

### Requirement: Watch Resilience
The server SHALL automatically recover from Kubernetes watch connection failures (network partitions, API server restarts, timeouts) using exponential backoff (initial 1s, max 30s). During reconnection attempts, the subscription remains active but no events are delivered. The watch SHALL resume from the last known `resourceVersion` to minimize missed events. If reconnection fails after 5 attempts, the server SHALL emit a `notifications/message` with `logger="kubernetes/subscription_error"`, `level="error"`, and a `data` object containing `subscriptionId`, `cluster`, `error`, and `degraded=true` fields, then mark the subscription as degraded without removing it.

#### Scenario: Auto-reconnect on watch timeout
- **WHEN** a Kubernetes watch connection times out or is closed by the API server
- **THEN** the server automatically re-establishes the watch with exponential backoff and resumes delivering events once reconnected, without requiring client intervention.

#### Scenario: Emit error notification on persistent failure
- **WHEN** reconnection attempts fail 5 consecutive times
- **THEN** the server emits `notifications/message` with `logger="kubernetes/subscription_error"`, `level="error"`, and a `data` object containing the subscription ID, cluster, and error description, allowing the client to decide whether to unsubscribe or wait for recovery.

#### Scenario: Resume from last resourceVersion
- **WHEN** a watch reconnects after a brief disconnection
- **THEN** the server attempts to resume from the last known `resourceVersion` to capture events that occurred during the outage, subject to Kubernetes API server retention limits.

### Requirement: Filtered Event Streams
Subscriptions MUST honor the provided filters using Kubernetes API semantics (namespaces bound to the event client's namespace, field selectors for involved object `kind`, `name`, `namespace`, and optional `reason` prefix matching). When multi-cluster support is enabled, the server SHALL use the derived Kubernetes client for the requested `cluster` and isolate subscriptions per cluster context.

#### Scenario: Namespace + object filter
- **WHEN** a client subscribes with `namespace="payments"` and `involvedName="worker-0"`
- **THEN** only events whose involved object name equals `worker-0` inside the `payments` namespace are forwarded for that subscription.

#### Scenario: Multi-cluster context
- **WHEN** the server is configured with two clusters and the client specifies `cluster="prod"`
- **THEN** the watch uses the prod client, and no events from `dev` appear in that subscription's notifications.

### Requirement: Transport Guardrails
The server MUST ensure that event subscriptions (either mode) are only allowed when running in a transport that can deliver server->client notifications (HTTP streamable or SSE). The server SHALL detect transport capability by checking if the session ID is non-empty (SSE/HTTP sessions have IDs; stdio sessions do not). Attempts to call `events_subscribe` while the server is running in stdio mode SHALL return an error explaining that SSE (via `--port`/HTTP) is required. The server MUST cap subscriptions at 10 per session and 100 globally by default (configurable via `--max-subscriptions-per-session` and `--max-subscriptions-global`). The server MUST cap concurrent log captures at 5 per cluster and 20 globally by default (configurable via `--max-log-captures-per-cluster` and `--max-log-captures-global`). The server SHALL return explicit errors when any of these limits are exceeded.

#### Scenario: Stdio invocation fails
- **WHEN** an assistant connected over stdio calls `events_subscribe`
- **THEN** the tool responds with a message that the SSE/HTTP server must be enabled (`--port ...`) before subscriptions can be created, detected via empty session ID.

#### Scenario: Enforce subscription cap
- **WHEN** a session already owns the maximum allowed subscriptions and tries to add one more
- **THEN** the server returns an error describing the limit instead of leaking an unmanaged watch.

#### Scenario: Enforce log capture concurrency
- **WHEN** the number of simultaneous log enrichments hits the configured maximum
- **THEN** the server responds to additional `events_subscribe` or in-flight events with an error (or a notification-level warning) explaining that log enrichment is throttled, rather than silently dropping log data.

### Requirement: Notification Logger Namespacing
The server SHALL use distinct `logger` values to categorize notifications:
- `"kubernetes/events"` for flexible event stream notifications (`mode=events`)
- `"kubernetes/faults"` for fault notifications with log enrichment (`mode=faults`)
- `"kubernetes/subscription_error"` for subscription error notifications (watch failures, degraded state)

Clients MAY filter incoming `notifications/message` by `logger` to separate event notifications from other logging traffic.

#### Scenario: Filter notifications by logger
- **WHEN** a client receives multiple `notifications/message` payloads
- **THEN** the client can inspect the `logger` field to determine if the notification is an event (`kubernetes/events`), fault (`kubernetes/faults`), subscription error (`kubernetes/subscription_error`), or unrelated logging.
