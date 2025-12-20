# Spec Delta: kubernetes-event-streaming

This spec delta modifies the `kubernetes-event-streaming` specification to clarify logging behavior during watch lifecycle events.

## MODIFIED Requirements

### Requirement: Watch Resilience

The server SHALL automatically recover from Kubernetes watch connection failures (network partitions, API server restarts, timeouts, resource version expiration) using exponential backoff (initial 1s, max 30s). During reconnection attempts, the subscription remains active but no events are delivered. The watch SHALL resume from the last known `resourceVersion` to minimize missed events.

**Routine reconnections** (such as 410 Gone errors due to resource version expiration) SHALL be logged at DEBUG level (klog verbosity >= 2). Individual retry attempts and backoff timing SHALL also be logged at DEBUG level. After 3 consecutive failures, the server SHALL log reconnection attempts at WARNING level.

If reconnection fails after 5 attempts, the server SHALL emit a `notifications/message` with `logger="kubernetes/subscription_error"`, `level="error"`, and a `data` object containing `subscriptionId`, `cluster`, `error`, and `degraded=true` fields, then mark the subscription as degraded without removing it.

#### Scenario: Auto-reconnect on watch timeout
- **WHEN** a Kubernetes watch connection times out or is closed by the API server
- **THEN** the server automatically re-establishes the watch with exponential backoff and resumes delivering events once reconnected, without requiring client intervention
- **AND** logs the reconnection process at DEBUG level for the first 2 attempts.

#### Scenario: Escalate logging after repeated failures
- **WHEN** reconnection attempts fail 3 or more consecutive times
- **THEN** subsequent retry attempts are logged at WARNING level to alert operators of potential issues.

#### Scenario: Emit error notification on persistent failure
- **WHEN** reconnection attempts fail 5 consecutive times
- **THEN** the server emits `notifications/message` with `logger="kubernetes/subscription_error"`, `level="error"`, and a `data` object containing the subscription ID, cluster, and error description, allowing the client to decide whether to unsubscribe or wait for recovery.

#### Scenario: Resume from last resourceVersion
- **WHEN** a watch reconnects after a brief disconnection
- **THEN** the server attempts to resume from the last known `resourceVersion` to capture events that occurred during the outage, subject to Kubernetes API server retention limits
- **AND** logs the resource version being used for resume at DEBUG level.

#### Scenario: Handle resource version expiration gracefully
- **WHEN** the Kubernetes API returns 410 Gone due to resource version being too old
- **THEN** the watch error is logged at DEBUG level (not WARNING)
- **AND** the watcher reconnects automatically without producing WARNING-level logs during the first 2 attempts.

## ADDED Requirements

None - this change only modifies the existing Watch Resilience requirement to clarify logging behavior.

## REMOVED Requirements

None - no requirements are removed, only clarified.
