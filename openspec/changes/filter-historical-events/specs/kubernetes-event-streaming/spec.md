# kubernetes-event-streaming Specification Delta

## MODIFIED Requirements

### Requirement: Flexible Event Subscription Tools

The Kubernetes MCP server SHALL expose read-only tools that let an MCP client create and cancel Kubernetes event subscriptions while connected over the HTTP/SSE transport. `events_subscribe` MUST accept filter arguments (cluster, namespaces/namespaceSelector, labelSelector, involved object metadata, type, reason) and return a unique `subscriptionId` along with the normalized filters and selected `mode`. **Subscriptions SHALL only notify on events that occur after the subscription is created, filtering out historical events that existed before subscription time.** `events_unsubscribe` MUST tear down the identified subscription and MAY be invoked multiple times without error.

#### Scenario: Create subscription with filters
- **WHEN** a client connected via SSE calls `events_subscribe` with `cluster="dev"`, `namespace="kube-system"`, and `type="Warning"`
- **THEN** the tool responds with a JSON payload containing a non-empty `subscriptionId`, echoes the normalized filters plus `mode="events"`, and starts watching Kubernetes `Event` resources that match those filters **from the current point in time forward**.

#### Scenario: Historical events are filtered out
- **GIVEN** a Kubernetes cluster with existing Warning events that occurred 5 minutes ago
- **WHEN** a client creates a new subscription at time T
- **THEN** the client receives NO notifications for events that occurred before time T, even if they match the subscription filters.

#### Scenario: New events are delivered
- **GIVEN** an active subscription created at time T
- **WHEN** a new Kubernetes event matching the subscription filters is generated at time T+1
- **THEN** the client receives a notification for that event.

#### Scenario: Ongoing faults are not missed
- **GIVEN** a pod that has been crash-looping for 10 minutes (with many historical BackOff events)
- **WHEN** a client creates a fault subscription at time T
- **THEN** the client receives NO notifications for the historical BackOff events
- **AND** when Kubernetes generates a new BackOff event at time T+30s (ongoing issue), the client DOES receive a notification.

#### Scenario: Unsubscribe idempotently
- **WHEN** the same client later calls `events_unsubscribe` with that `subscriptionId`
- **THEN** the server stops emitting notifications for that subscription and returns a success acknowledgement even if the tool is invoked again with the same id.

### Requirement: Fault Subscription Tools

The server SHALL support a `mode="faults"` flow that automatically focuses on Warning events targeting Pods. Fault subscriptions MUST reuse the same lifecycle and filter semantics as flexible subscriptions but SHALL reject `type="Normal"`. **Fault subscriptions SHALL only notify on Warning events that occur after the subscription is created, not historical warnings.** The subscription response MUST clearly indicate that the mode is `faults` so clients know to expect enriched notifications.

#### Scenario: Create fault subscription
- **WHEN** a client calls `events_subscribe` with `mode="faults"`, `namespaceSelector=["prod-*"]`, and `labelSelector="app=payments"`
- **THEN** the tool returns a `subscriptionId` tied to `mode="faults"` and begins monitoring Warning events that match the namespace/label filters **from the current point in time forward**.

#### Scenario: Historical fault events are filtered out
- **GIVEN** pods with historical FailedMount warnings from 2 hours ago
- **WHEN** a client creates a fault subscription
- **THEN** the client receives NO notifications for the 2-hour-old FailedMount events.

#### Scenario: New fault events are delivered with logs
- **GIVEN** an active fault subscription
- **WHEN** a pod generates a new FailedMount warning after subscription creation
- **THEN** the client receives a fault notification with container logs for that specific warning event.

## ADDED Requirements

### Requirement: Historical Event Filtering

Subscriptions SHALL start watching from the current Kubernetes resource version to filter out historical events. When `events_subscribe` is called, the server MUST obtain the current resource version from the Kubernetes API before starting the watch. The watch SHALL use this resource version as its starting point, ensuring only events generated after subscription creation are delivered. If obtaining the current resource version fails, the subscription creation MUST fail with a clear error message indicating the reason.

#### Scenario: Resource version initialization
- **WHEN** a client calls `events_subscribe`
- **THEN** the server performs a List operation with `Limit=1` to obtain the current resource version
- **AND** uses that resource version when starting the Kubernetes watch.

#### Scenario: Subscription fails when resource version unavailable
- **GIVEN** the Kubernetes API is temporarily unavailable or RBAC denies List permission
- **WHEN** a client calls `events_subscribe` and the List operation to get resource version fails
- **THEN** the subscription creation fails with an error
- **AND** the error message clearly indicates that the resource version could not be obtained
- **AND** the client can retry when the API becomes available.

#### Scenario: Resource version preserved on reconnection
- **GIVEN** an active subscription watching from resource version "12345"
- **WHEN** the watch connection is lost and reconnects
- **THEN** the watch resumes from the last known resource version (NOT the initial resource version)
- **AND** no duplicate events are sent.

#### Scenario: Cluster-wide vs namespace-scoped resource versions
- **WHEN** creating a cluster-wide subscription (no namespace filter)
- **THEN** the server obtains the resource version using `List(namespace=metav1.NamespaceAll)`
- **WHEN** creating a namespace-scoped subscription
- **THEN** the server obtains the resource version using `List(namespace=<specific-namespace>)`.
