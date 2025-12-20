# Change: Add SSE-based Kubernetes Event Subscriptions

## Why
Kubernetes MCP Server already exposes an SSE transport (`pkg/http/http.go`, `pkg/mcp/mcp.go`), but it only emits generic tool list change notifications. Operators currently rely on synchronous tools such as `events_list` (`pkg/toolsets/core/events.go`) which poll for recent events and cannot alert assistants when critical warnings happen, nor do they automatically capture the pod logs that explain a crash. We need two complementary capabilities: (1) a flexible, real-time event stream so MCP clients can subscribe to any mix of namespaces, labels, and event types, and (2) an intelligent "faults watcher" that focuses on Warning events, automatically gathers pod logs (current and `--previous`), analyzes them for panics/errors, and delivers actionable fault packages to subscribers without requiring additional tool calls.

## SDK Constraints and Minimalist Approach

The MCP go-sdk (v1.1.0) has specific constraints that shape our implementation:

1. **No custom notification methods**: The SDK validates outgoing notifications against a fixed method registry. Custom methods like `notifications/kubernetes/events` would fail.

2. **Unexported connection access**: We cannot directly send JSON-RPC messages to sessions.

3. **Available API**: The SDK exposes `ServerSession.Log()` which sends standard `notifications/message` with arbitrary structured data.

**Our approach**: Use `ServerSession.Log()` with a naming convention:
- `logger="kubernetes/events"` for event stream notifications
- `logger="kubernetes/faults"` for fault notifications with logs
- `logger="kubernetes/subscription_error"` for error notifications

This requires **zero SDK modifications** and changes only ~3-4 files in the existing codebase:
- `pkg/api/toolsets.go` - add `SessionID` field to `ToolHandlerParams`
- `pkg/mcp/gosdk.go` - extract session ID from request
- `pkg/toolsets/core/` - add subscription tools
- New `pkg/events/` package - self-contained subscription manager

**Client requirement**: Clients must call `logging/setLevel` (standard MCP) before receiving notifications.

## What Changes
- Add a session-scoped `events_subscribe` tool that registers filters (cluster/namespace/labels/involved object/reason/type) and streams matching Kubernetes `Event` objects to the calling session via `notifications/message` with `logger="kubernetes/events"`.
- Add a `mode=faults` option dedicated to Warning events referencing Pods, backed by a log intelligence layer that collects container logs, detects panics/errors, and delivers enriched notifications via `logger="kubernetes/faults"`.
- Use `notifications/message` (standard MCP logging) with structured `data` payloads containing subscription id, cluster, event details, and (for faults) log bundles.
- Provide lifecycle management (unsubscribe + automatic cleanup via periodic session monitoring) plus guardrails when SSE is unavailable (check `session.ID() == ""`).
- Add new `pkg/events/` package containing `EventSubscriptionManager` that is self-contained and minimally coupled to existing code.

## Minimal Code Impact

| File | Change |
|------|--------|
| `pkg/api/toolsets.go` | Add `SessionID string` field to `ToolHandlerParams` |
| `pkg/mcp/gosdk.go` | Extract `request.Session.ID()` and pass to handler |
| `pkg/mcp/mcp.go` | Pass `*mcp.Server` reference to event manager |
| `pkg/toolsets/core/` | Add `events_subscribe`, `events_unsubscribe` tools |
| `pkg/events/` (new) | Self-contained subscription manager, watcher, notifications |
| `cmd/kubernetes-mcp-server/` | Add CLI flags, wire manager at startup |

## Impact
- Affected specs: `kubernetes-event-streaming` (new capability covering flexible event stream and fault watcher tools, logging-based notifications, and lifecycle constraints).
- Affected code: Minimal changes to existing files; most logic in new `pkg/events/` package.
- No SDK modifications required.
- Backward compatible: clients not using subscriptions are unaffected.

## Key Design Decisions

1. **Notification via `Log()`**: Uses existing SDK API, no custom notification methods needed.
2. **Logger namespacing**: Distinguishes event types via `logger` field (`kubernetes/events`, `kubernetes/faults`, `kubernetes/subscription_error`).
3. **Session ID detection**: Empty session ID indicates stdio transport (no SSE support).
4. **Periodic session monitoring**: Every 30s, check `Server.Sessions()` to clean up stale subscriptions.
5. **Context-based routing**: Use `context.Background()` when sending notifications to route to standalone SSE stream.
