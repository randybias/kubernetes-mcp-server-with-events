# Design: Rename resource-faults to faults

## Context

The `refactor-fault-detection-signals` change introduced a new fault detection system (`mode="resource-faults"`) that is architecturally superior to the old event-based `mode="faults"`. The old system has these problems:

1. **Unreliable signals** - Depends on best-effort Kubernetes Events
2. **Backpressure** - Each Warning event triggers 7-12 API calls for log enrichment
3. **Notification storms** - Duplicate events cause repeated notifications
4. **Limited coverage** - Cannot detect Node or Deployment failures

The new system (resource-faults) solves all these problems by watching resources directly and using edge-triggered detection.

## Goals / Non-Goals

**Goals:**
- Simplify the API by having only one "faults" mode (the better one)
- Remove old, inferior event-based fault detection code
- Rename resource-faults to faults with no functional changes
- Update all user-facing strings and documentation

**Non-Goals:**
- Maintain backwards compatibility (explicitly rejected by user)
- Support migration path or dual mode operation
- Keep old event-based code "just in case"

## Decisions

### Decision: Hard cutover, no compatibility layer

**What:** Completely remove old `mode="faults"` and reuse the name for the new implementation.

**Why:**
- User explicitly requested no backwards compatibility
- Simpler code, simpler docs, less confusion
- The new implementation is objectively better in every way
- Dual-mode operation was only intended for gradual migration, which is no longer needed

**Alternatives considered:**
- Keep both modes indefinitely: Rejected - adds complexity and confuses users
- Add `mode="faults-v2"`: Rejected - awkward naming, still leaves old mode around

### Decision: Simple string replacement for mode and logger

**What:** Change all occurrences of `"resource-faults"` to `"faults"` in:
- Tool mode enum values
- Logger names (kubernetes/resource-faults → kubernetes/faults)
- Documentation strings
- Test fixtures

**Why:** The functionality and data structures are already correct. This is purely a naming change.

### Decision: Remove old fault watcher implementation

**What:** Delete code paths in `pkg/events/` that:
- Watch v1.Event resources for Warning events targeting Pods
- Enrich events with container logs via synchronous API calls
- Use the old DeduplicationCache based on event UID/Count

**Why:** This code becomes dead after the rename. No point maintaining unused code.

**Files affected:**
- `pkg/events/watcher.go` - Contains old event-based watcher
- `pkg/events/manager.go` - Routes mode="faults" to old watcher
- `pkg/events/logs.go` - Event-triggered log enrichment
- Tests that cover old fault mode behavior

## Risks / Trade-offs

### Risk: Breaking change for existing users

**Impact:** Any user currently using `mode="faults"` will get an error or different payload structure.

**Mitigation:** User explicitly requested this. Document the breaking change clearly in release notes.

### Risk: Lost test coverage

**Impact:** Removing old code removes tests that verified event-based behavior.

**Mitigation:** The new resource-based system has comprehensive test coverage (30+ tests per detector). We're not losing functional coverage, just obsolete implementation tests.

## Implementation Plan

1. **Rename phase:**
   - Search/replace "resource-faults" → "faults" in appropriate locations
   - Update logger constants
   - Update tool schema

2. **Removal phase:**
   - Remove old fault watcher code
   - Remove old log enrichment pipeline
   - Remove tests for old implementation

3. **Validation:**
   - Run full test suite
   - Verify manual testing guide works with new `mode="faults"`
   - Update all documentation

## Open Questions

None - design is straightforward.
