# Tasks: Rename resource-faults to faults

This change removes the old event-based fault detection and renames the new resource-based implementation from "resource-faults" to "faults".

## Phase 1: Rename resource-faults to faults

Goal: Change all occurrences of "resource-faults" to "faults" without changing functionality.

- [x] 1.1 Update tool schema in `pkg/toolsets/core/events.go`
      - Change mode enum from `["events", "faults", "resource-faults"]` to `["events", "faults"]`
      - Update description to describe resource-based fault detection
- [x] 1.2 Update logger constant in `pkg/events/notification.go`
      - Rename `LoggerResourceFaults = "kubernetes/resource-faults"` to `LoggerFaults = "kubernetes/faults"`
- [x] 1.3 Update manager validation in `pkg/events/manager.go`
      - Change validation from accepting "events", "faults", "resource-faults" to just "events", "faults"
      - Update startWatcher routing to use resource watcher for mode="faults"
- [x] 1.4 Update all test fixtures and assertions
      - Search for `"resource-faults"` in test files
      - Replace with `"faults"`
      - Update logger assertions from `"kubernetes/resource-faults"` to `"kubernetes/faults"`
- [x] 1.5 **VERIFY:** `go test ./pkg/events/` passes with renamed values

## Phase 2: Remove old event-based fault detection

Goal: Delete the old fault watcher implementation that watched v1.Events.

- [x] 2.1 Identify old fault watcher code
      - Find code that watches v1.Event resources for Warning events
      - Find old log enrichment that triggers on event receipt
      - Find old deduplication based on event UID/Count
- [x] 2.2 Remove old fault watcher from `pkg/events/watcher.go`
      - Delete event-based fault watcher struct/methods (if separate from event watcher)
      - Remove log enrichment triggered by Warning events
- [x] 2.3 Remove old fault mode routing from `pkg/events/manager.go`
      - Delete code path that routes old mode="faults" to event watcher
      - Keep only: mode="events" → event watcher, mode="faults" → resource watcher
- [x] 2.4 Clean up unused log enrichment code in `pkg/events/logs.go`
      - Remove event-triggered log capture if distinct from resource-based enrichment
      - Keep: conditional log fetching used by FaultContextEnricher
- [x] 2.5 Remove old fault notification structure (if different)
      - Check if old fault notifications used different struct than ResourceFaultNotification
      - Remove if no longer needed
- [x] 2.6 **VERIFY:** `go build ./cmd/kubernetes-mcp-server` succeeds

## Phase 3: Remove old fault detection tests

Goal: Clean up tests for the removed event-based implementation.

- [x] 3.1 Identify tests for old fault mode
      - Search for tests that create mode="faults" subscriptions and expect event-based behavior
      - Search for tests that verify log enrichment on Warning events
- [x] 3.2 Remove obsolete test files/functions
      - Delete tests that verify old deduplication (event UID/Count based)
      - Delete tests that verify automatic log fetching on Warning events
      - Keep: tests for resource-based fault detection
- [x] 3.3 **VERIFY:** `go test ./pkg/events/` passes

## Phase 4: Update documentation

Goal: Update all user-facing documentation to reflect the change.

- [x] 4.1 Update `pkg/events/README.md`
      - Remove references to event-based fault detection
      - Update examples to use mode="faults" for resource-based detection
      - Document that mode="faults" watches resources, not events
- [x] 4.2 Update `PHASE_12_TESTING.md` in worktree
      - Change all references from mode="resource-faults" to mode="faults"
- [x] 4.3 Update main `README.md` if it documents event subscriptions
      - Search for subscription mode documentation
      - Update to reflect only two modes: events and faults
- [x] 4.4 **VERIFY:** Documentation is accurate and consistent

## Phase 5: Integration verification

Goal: Verify the renamed implementation works end-to-end.

- [x] 5.1 Build server binary: `go build -o kubernetes-mcp-server ./cmd/kubernetes-mcp-server`
- [x] 5.2 Start server with mcp-inspector: `npx @modelcontextprotocol/inspector ./kubernetes-mcp-server`
- [x] 5.3 Verify tool schema shows mode="faults" (not "resource-faults")
- [x] 5.4 Create subscription with mode="faults"
- [x] 5.5 Trigger pod crash and verify notification with logger="kubernetes/faults"
- [x] 5.6 **VERIFY:** End-to-end flow works with renamed mode
