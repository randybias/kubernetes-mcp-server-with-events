## Context

kubernetes-mcp-server emits "Fault Events" via MCP logging protocol to downstream systems (e.g., nightcrier). The current implementation deduplicates internally using a 15-minute TTL, but provides no stable identifier for downstream to recognize re-emissions of the same fault condition.

**Stakeholders:**
- kubernetes-mcp-server: Emits fault events, owns fault detection and deduplication
- nightcrier: Receives fault events, creates incidents, owns triage lifecycle
- MCP specification: Defines the notification format (arbitrary `data` payload)

## Goals / Non-Goals

**Goals:**
- Provide a stable identifier (`faultId`) that downstream can use for deduplication
- Clarify terminology: kubernetes-mcp-server owns "faults", nightcrier owns "incidents"
- Enable reliable at-least-once delivery with downstream-side deduplication
- Minimal changes to kubernetes-mcp-server codebase

**Non-Goals:**
- Acknowledgment/delivery confirmation from downstream
- Managing incident lifecycle (nightcrier's responsibility)
- Changing deduplication TTL or re-emission behavior
- Adding timestamp to Fault ID (would prevent downstream from recognizing recurrences)

## Decisions

### Decision 1: Fault ID is deterministic, not random

**What:** Generate Fault ID as `shortHash(cluster + faultType + resourceUID + containerName)`

**Why:**
- Same fault condition always produces the same Fault ID
- No storage overhead for tracking generated IDs
- Downstream can independently verify the ID if needed
- Re-emissions have the same ID (intentional - downstream handles recurrence policy)

**Alternatives considered:**
- UUID per emission: Would prevent downstream deduplication (rejected)
- UUID per fault condition stored in deduplicator: Storage overhead, complexity (rejected)
- Include timestamp: Would make recurrences look like new faults (rejected)

### Decision 2: Fault ID excludes timestamp

**What:** The Fault ID does not include any time-based component.

**Why:**
- Gives nightcrier full control over incident lifecycle policy
- Nightcrier decides: "Same fault recurred - reopen incident or create new?"
- Aligns with separation of concerns (kubernetes-mcp-server doesn't know if incident was resolved)

**Trade-off:** If a fault genuinely "resolves" and recurs 2 hours later, it has the same Fault ID. This is intentional - nightcrier can track its own state (last resolved timestamp) and decide accordingly.

### Decision 3: Fault ID includes cluster name

**What:** Cluster is part of the Fault ID generation.

**Why:**
- kubernetes-mcp-server supports multi-cluster configurations
- ResourceUIDs are unique within a cluster, but not across clusters
- Same resource name in two clusters would otherwise collide

### Decision 4: Internal terminology change

**What:** Rename `incidentKey` to `faultConditionKey`, `incidentRecord` to `faultEmissionRecord`.

**Why:**
- "Incident" is nightcrier's domain concept
- kubernetes-mcp-server tracks fault conditions and emissions, not incidents
- Clearer separation of concerns in the code

## Data Structure Changes

### Current `ResourceFaultNotification`

```go
type ResourceFaultNotification struct {
    SubscriptionID string             `json:"subscriptionId"`
    Cluster        string             `json:"cluster"`
    FaultType      FaultType          `json:"faultType"`
    Severity       Severity           `json:"severity"`
    Resource       *ResourceReference `json:"resource"`
    Context        string             `json:"context,omitempty"`
    Timestamp      string             `json:"timestamp"`
}
```

### Proposed `ResourceFaultNotification`

```go
type ResourceFaultNotification struct {
    SubscriptionID string             `json:"subscriptionId"`
    Cluster        string             `json:"cluster"`
    FaultID        string             `json:"faultId"`      // NEW: Stable condition identifier
    FaultType      FaultType          `json:"faultType"`
    Severity       Severity           `json:"severity"`
    Resource       *ResourceReference `json:"resource"`
    Context        string             `json:"context,omitempty"`
    Timestamp      string             `json:"timestamp"`
}
```

### Fault ID Format

- Length: 16 hex characters (64 bits of SHA-256)
- Example: `"f7a3b2c1d4e5f6a7"`
- Generation: `hex(sha256(cluster + ":" + faultType + ":" + resourceUID + ":" + containerName)[:8])`

### JSON Payload Example

```json
{
  "subscriptionId": "sub-abc12345",
  "cluster": "prod-us-east",
  "faultId": "f7a3b2c1d4e5f6a7",
  "faultType": "CrashLoop",
  "severity": "critical",
  "resource": {
    "apiVersion": "v1",
    "kind": "Pod",
    "name": "api-server-7f8b9c",
    "namespace": "production",
    "uid": "abc123-def456-ghi789"
  },
  "context": "Error: connection refused to database\n...",
  "timestamp": "2025-12-20T15:30:00Z"
}
```

## Risks / Trade-offs

### Risk: Fault ID collision
**Mitigation:** 64 bits of SHA-256 provides sufficient collision resistance for practical use. With 1 million unique faults, collision probability is ~0.00000003%.

### Risk: Downstream doesn't implement deduplication
**Mitigation:** Documentation clearly states nightcrier should use `faultId` for deduplication. The field name is self-explanatory.

### Risk: Breaking change for existing consumers
**Mitigation:** This is an additive change (new field). Existing consumers can ignore `faultId` if they don't need it.

## Integration with Nightcrier

Nightcrier should implement the following logic:

```
On receiving Fault Event:
1. Extract faultId from payload
2. Check: Have I seen this faultId recently?
   - YES and actively triaging -> Ignore (or log as "still active")
   - YES but incident resolved -> Policy decision (reopen or create new)
   - NO -> Create new incident with nightcrier's own incidentId
3. Store mapping: faultId -> incidentId + state
```

Key points for nightcrier:
- `faultId` is kubernetes-mcp-server's identifier for the fault condition
- `incidentId` is nightcrier's identifier for triage tracking
- These are separate concepts with separate lifecycles
- Re-emissions will have the same `faultId` - this is intentional for reliability

## Open Questions

None - design is straightforward given the semantic model agreed upon.
