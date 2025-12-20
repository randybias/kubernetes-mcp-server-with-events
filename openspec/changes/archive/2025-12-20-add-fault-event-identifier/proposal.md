# Change: Add Fault Event Identifier for Downstream Deduplication

## Why

When kubernetes-mcp-server emits fault notifications to downstream systems like nightcrier, there is no stable identifier that downstream can use for deduplication. Currently, the same fault condition may generate multiple notifications (re-emissions after TTL expiry), and downstream systems have no reliable way to recognize these as being about the same underlying fault. This causes potential duplicate triage efforts or notification spam.

## What Changes

- Add a `faultId` field to the `ResourceFaultNotification` payload
- The Fault ID is a stable, deterministic identifier for the fault condition (not the notification/emission itself)
- Generated from: `hash(cluster + faultType + resourceUID + containerName)`
- Same fault condition always produces the same Fault ID across re-emissions
- Rename internal `incidentKey`/`incidentRecord` to `faultConditionKey`/`faultEmissionRecord` for semantic clarity (we don't "own" incidents - nightcrier does)

## Semantic Model

| Concept | Owner | Purpose |
|---------|-------|---------|
| **Fault Signal** | kubernetes-mcp-server (internal) | Raw detection output from detectors |
| **Fault Condition** | kubernetes-mcp-server (internal) | The "thing that's wrong" - used for deduplication |
| **Fault Event** | kubernetes-mcp-server (external) | The notification/message emitted to subscribers |
| **Fault ID** | kubernetes-mcp-server (in Fault Event) | Stable identifier for the fault condition |
| **Incident** | nightcrier | Tracks triage/response for a fault |
| **Incident ID** | nightcrier | nightcrier's internal tracking identifier |

## Design Decisions

- **Fault ID excludes timestamp**: A recurring fault has the same Fault ID. Nightcrier owns the incident lifecycle policy (reopen vs. new incident).
- **Fault ID includes cluster**: Since kubernetes-mcp-server supports multi-cluster, ResourceUIDs alone aren't globally unique.
- **Deterministic generation**: Fault ID is computed from fault characteristics, not randomly generated. Same inputs always produce same output.

## Impact

- Affected specs: `kubernetes-event-streaming` (Resource Fault Notification Format requirement)
- Affected code:
  - `pkg/events/notification.go` - Add `FaultID` field to `ResourceFaultNotification`
  - `pkg/events/fault_dedup.go` - Rename types, add `FaultID()` method
  - `pkg/events/manager.go` - Generate and include Fault ID when building notifications
