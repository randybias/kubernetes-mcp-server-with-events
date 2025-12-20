## Context
- The HTTP front-end already exposes `/sse` and `/message` (`pkg/http/http.go`) backed by `github.com/modelcontextprotocol/go-sdk/mcp.ServeSse`, yet the server only emits built-in notifications such as `notifications/tools/list_changed` from the SDK when toolsets reload.
- The only event-focused tool (`events_list` in `pkg/toolsets/core/events.go`) performs a blocking poll against `Event` resources via `pkg/kubernetes/events.go`; there is no push model or filterable subscription.
- MCP streamable HTTP + SSE transports support long-lived sessions, so we can attach per-session watchers and push notifications with structured payloads without changing the transport layer.
- Warning events rarely provide enough context on their own; operators currently pivot to `kubectl logs` (current and `--previous`) to understand panics/crash loops, which is a tedious set of follow-up tool calls for an MCP client.

## SDK Constraints Analysis

The MCP go-sdk (v1.1.0) has specific constraints that shape our implementation:

1. **No custom notification methods**: The SDK's `defaultSendingMethodHandler` validates outgoing notifications against a fixed `clientMethodInfos` map. Custom methods like `notifications/kubernetes/events` would return `jsonrpc2.ErrNotHandled`.

2. **Unexported connection access**: `ServerSession.getConn()` and `ServerSession.conn` are unexported, preventing direct JSON-RPC notification calls.

3. **Available notification API**: The SDK exposes `ServerSession.Log(ctx, *LoggingMessageParams)` which:
   - Sends `notifications/message` (standard MCP logging notification)
   - Accepts arbitrary JSON data via the `Data any` field
   - Supports a `Logger string` field for categorization
   - **Requires client to set log level first** via `logging/setLevel`

4. **Session access**: `Server.Sessions()` returns an iterator over active sessions, and `ServerSession.ID()` returns empty string for stdio (non-SSE) sessions.

5. **Context-based routing**: For streamable HTTP, notifications sent with `context.Background()` route to the standalone SSE stream, enabling push outside of request contexts.

## Goals / Non-Goals
- Goals:
  - Allow MCP clients connected over SSE (or streamable HTTP) to subscribe to Kubernetes `Event` objects filtered by namespace, label selectors, involved object metadata, type, or reason.
  - Provide tools to create and tear down subscriptions, returning stable `subscriptionId` values for correlation.
  - Deliver two subscription modes:
    1. **Flexible event stream** (`mode=events`) – raw event notifications for Normal or Warning events.
    2. **Fault watcher** (`mode=faults`) – Warning-only stream with automatic pod log collection and panic detection.
  - Work within SDK constraints without forking or modifying the go-sdk.
  - Automatically clean up subscriptions when sessions close.
- Non-Goals:
  - Modifying the MCP go-sdk to add custom notification support.
  - Building a generalized resource watch framework; this covers only Kubernetes `Event` resources.
  - Supporting subscriptions over STDIO (no server push channel).
  - Replacing existing `events_list` polling tools.

## Decisions

### 1. Notification Delivery via `ServerSession.Log()`

**Rationale**: The only publicly exported notification method on `ServerSession` that can carry arbitrary structured data is `Log()`. We use it with a naming convention to distinguish event notifications from regular logs.

**Implementation**:
- Use the `Logger` field as a namespace identifier: `"kubernetes/events"`, `"kubernetes/faults"`, `"kubernetes/subscription_error"`
- Use the `Data` field to carry the full structured event payload
- Use `Level` to indicate severity: `LoggingLevel("info")` for events, `LoggingLevel("warning")` for faults/errors

**Client requirement**: Clients MUST call `logging/setLevel` (with level `debug`, `info`, or higher) before receiving any notifications. This is standard MCP behavior documented in the SDK.

**Notification format** (via `notifications/message`):
```json
{
  "level": "info",
  "logger": "kubernetes/events",
  "data": {
    "subscriptionId": "sub-123",
    "cluster": "dev-cluster",
    "event": {
      "namespace": "kube-system",
      "timestamp": "2025-01-15T12:34:56Z",
      "type": "Warning",
      "reason": "BackOff",
      "message": "Back-off restarting failed container",
      "labels": { "app": "nginx" },
      "involvedObject": {
        "apiVersion": "v1",
        "kind": "Pod",
        "name": "nginx-123",
        "namespace": "default"
      }
    }
  }
}
```

For faults with logs:
```json
{
  "level": "warning",
  "logger": "kubernetes/faults",
  "data": {
    "subscriptionId": "sub-456",
    "cluster": "prod",
    "event": { "...warning details..." },
    "logs": [
      {
        "container": "web",
        "previous": false,
        "hasPanic": true,
        "sample": "panic: runtime error\nstack trace...\n"
      }
    ]
  }
}
```

For subscription errors:
```json
{
  "level": "error",
  "logger": "kubernetes/subscription_error",
  "data": {
    "subscriptionId": "sub-789",
    "cluster": "dev-cluster",
    "error": "watch connection failed after 5 reconnection attempts",
    "degraded": true
  }
}
```

### 2. EventSubscriptionManager

A new `pkg/events/manager.go` package containing:

```go
type EventSubscriptionManager struct {
    mu            sync.RWMutex
    subscriptions map[string]*Subscription  // subscriptionID -> Subscription
    bySession     map[string][]string       // sessionID -> []subscriptionID
    byCluster     map[string][]string       // cluster -> []subscriptionID
    server        *mcp.Server               // for accessing sessions
    config        ManagerConfig
}

type Subscription struct {
    ID            string
    SessionID     string
    Cluster       string
    Mode          string  // "events" or "faults"
    Filters       SubscriptionFilters
    Cancel        context.CancelFunc
    CreatedAt     time.Time
    Degraded      bool
}

type SubscriptionFilters struct {
    Namespaces        []string
    LabelSelector     string
    InvolvedKind      string
    InvolvedName      string
    InvolvedNamespace string
    Type              string  // "Normal", "Warning", or ""
    Reason            string  // prefix match
}

type ManagerConfig struct {
    MaxSubscriptionsPerSession int  // default: 10
    MaxSubscriptionsGlobal     int  // default: 100
    MaxLogCapturesPerCluster   int  // default: 5
    MaxLogCapturesGlobal       int  // default: 20
    MaxLogBytesPerContainer    int  // default: 10240 (10KB)
    MaxContainersPerNotification int // default: 5
    EventDeduplicationWindow   time.Duration // default: 5s
    FaultDeduplicationWindow   time.Duration // default: 60s
}
```

**Key methods**:
- `Create(sessionID, cluster, mode string, filters SubscriptionFilters) (*Subscription, error)`
- `Cancel(subscriptionID string) error`
- `CancelSession(sessionID string)` - called when session closes
- `CancelCluster(cluster string)` - called when cluster config removed
- `findSession(sessionID string) *mcp.ServerSession` - iterates `server.Sessions()`

### 3. Session Context in Tool Handlers

Extend `api.ToolHandlerParams` minimally:

```go
type ToolHandlerParams struct {
    context.Context
    *internalk8s.Kubernetes
    ToolCallRequest
    ListOutput output.Output
    SessionID  string  // NEW: extracted from mcp.CallToolRequest.Session.ID()
}
```

In `pkg/mcp/gosdk.go`, modify `ServerToolToGoSdkTool`:
```go
goSdkHandler := func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    // ... existing code ...

    sessionID := ""
    if request.Session != nil {
        sessionID = request.Session.ID()
    }

    result, err := tool.Handler(api.ToolHandlerParams{
        Context:         ctx,
        Kubernetes:      k,
        ToolCallRequest: toolCallRequest,
        ListOutput:      s.configuration.ListOutput(),
        SessionID:       sessionID,  // NEW
    })
    // ...
}
```

### 4. Transport Detection

Check `sessionID != ""` to determine if the transport supports notifications:
- SSE and Streamable HTTP sessions have non-empty session IDs
- STDIO sessions have empty session IDs

The `events_subscribe` tool checks this and returns a descriptive error if empty:
```
"Event subscriptions require an HTTP/SSE transport. Start the server with --port and connect via HTTP."
```

### 5. Session Lifecycle Management

**Problem**: The SDK doesn't expose an `OnClose` hook for sessions.

**Solution**: Periodic session validation + context cancellation:

1. When creating a subscription, start its watcher with a cancellable context
2. The manager runs a background goroutine that periodically (every 30s) checks:
   - Iterate `server.Sessions()` to get current session IDs
   - For each tracked session, if no longer present, call `CancelSession(sessionID)`
3. On server shutdown, cancel all subscriptions via context

```go
func (m *EventSubscriptionManager) StartSessionMonitor(ctx context.Context) {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            m.CancelAll()
            return
        case <-ticker.C:
            m.cleanupStaleSessions()
        }
    }
}

func (m *EventSubscriptionManager) cleanupStaleSessions() {
    activeSessions := make(map[string]bool)
    for session := range m.server.Sessions() {
        activeSessions[session.ID()] = true
    }

    m.mu.Lock()
    defer m.mu.Unlock()
    for sessionID := range m.bySession {
        if !activeSessions[sessionID] {
            m.cancelSessionLocked(sessionID)
        }
    }
}
```

### 6. Notification Delivery

To send a notification to a specific session:

```go
func (m *EventSubscriptionManager) sendNotification(sessionID string, logger string, level mcp.LoggingLevel, data any) error {
    for session := range m.server.Sessions() {
        if session.ID() == sessionID {
            return session.Log(context.Background(), &mcp.LoggingMessageParams{
                Level:  level,
                Logger: logger,
                Data:   data,
            })
        }
    }
    return fmt.Errorf("session %s not found", sessionID)
}
```

Using `context.Background()` ensures notifications route to the standalone SSE stream (not tied to any request).

### 7. Kubernetes Watch Implementation

Use `client-go` informers or direct watch API:

```go
func (m *EventSubscriptionManager) startWatcher(sub *Subscription, k8sClient *kubernetes.Kubernetes) {
    ctx, cancel := context.WithCancel(context.Background())
    sub.Cancel = cancel

    go func() {
        watcher := newEventWatcher(k8sClient, sub.Filters)
        retryCount := 0

        for {
            select {
            case <-ctx.Done():
                return
            case event, ok := <-watcher.ResultChan():
                if !ok {
                    // Watch closed, attempt reconnect
                    if retryCount >= 5 {
                        m.sendSubscriptionError(sub, "watch connection failed after 5 attempts")
                        sub.Degraded = true
                        return
                    }
                    retryCount++
                    time.Sleep(exponentialBackoff(retryCount))
                    watcher = newEventWatcher(k8sClient, sub.Filters)
                    continue
                }
                retryCount = 0
                m.processEvent(sub, event)
            }
        }
    }()
}
```

### 8. Tool API Surface

**`events_subscribe`** tool:
- Input parameters: `cluster`, `namespaces`, `labelSelector`, `involvedKind`, `involvedName`, `involvedNamespace`, `type`, `reason`, `mode`
- Validates session has non-empty ID (transport check)
- Validates subscription limits
- Creates subscription via manager
- Returns: `{ "subscriptionId": "...", "mode": "...", "filters": {...} }`

**`events_unsubscribe`** tool:
- Input parameters: `subscriptionId`
- Validates subscription belongs to current session
- Cancels subscription via manager
- Returns: `{ "cancelled": true }`

**`events_list_subscriptions`** tool (optional):
- Lists active subscriptions for current session
- Returns: `{ "subscriptions": [...] }`

### 9. Log Enrichment Pipeline (`mode=faults`)

For Warning events referencing Pods:
1. Check deduplication cache (60s window, key: `<cluster>/<ns>/<pod>/<reason>/<count>`)
2. If not duplicate, spawn log capture worker (respecting concurrency limits)
3. Fetch logs for each container (current + `--previous` if restarts indicated)
4. Truncate to 10KB per container, max 5 containers
5. Scan for panic/error markers: `panic:`, `fatal`, `segfault`, stack traces
6. Emit enriched notification

### 10. Deduplication

Two separate deduplication caches:
- **Events mode**: 5-second window, key = `<cluster>/<ns>/<name>/<uid>/<resourceVersion>`
- **Faults mode**: 60-second window, key = `<cluster>/<ns>/<pod>/<reason>/<count>`

Use a simple TTL map implementation or `golang.org/x/sync/singleflight` for log captures.

## Migration Plan
1. Add new `pkg/events/` package with manager and notification helpers
2. Extend `api.ToolHandlerParams` with `SessionID`
3. Register manager with MCP server during initialization
4. Add tools to core toolset
5. Document client requirements (must call `logging/setLevel`)
6. Ship behind existing HTTP transport (requires `--port`)

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Client doesn't set log level | Document requirement; tool can check and return warning |
| High-volume event spam | Require namespace filter for unbounded subscriptions; enforce limits |
| API server load from log fetches | Concurrency limits, truncation, deduplication |
| Watcher leaks on session disconnect | Periodic session validation + context cancellation |
| Transport mismatch (stdio) | Clear error message with guidance to use `--port` |

## Configuration Defaults

| Setting | Default | CLI Flag |
|---------|---------|----------|
| Max subscriptions per session | 10 | `--max-subscriptions-per-session` |
| Max subscriptions global | 100 | `--max-subscriptions-global` |
| Max log captures per cluster | 5 | `--max-log-captures-per-cluster` |
| Max log captures global | 20 | `--max-log-captures-global` |
| Max log bytes per container | 10KB | `--max-log-bytes-per-container` |
| Max containers per notification | 5 | `--max-containers-per-notification` |
| Event deduplication window | 5s | - |
| Fault deduplication window | 60s | - |
| Watch reconnection backoff | 1s-30s exponential | - |
| Session monitor interval | 30s | - |

## Testing Strategy

Testing requires three layers: unit tests with mocks, integration tests with envtest, and manual verification with live clusters.

### Unit Tests with Mocks

Unit tests validate individual components in isolation. Mock interfaces are used for external dependencies.

**Mock Interfaces**:

```go
// MockMCPServer - mocks mcp.Server for session iteration
type MockMCPServer struct {
    sessions []*MockServerSession
}

func (m *MockMCPServer) Sessions() iter.Seq[*mcp.ServerSession] {
    // Returns iterator over mock sessions
}

// MockServerSession - mocks mcp.ServerSession for notification capture
type MockServerSession struct {
    id        string
    logLevel  mcp.LoggingLevel
    logCalls  []LogCall  // captures all Log() calls for assertions
}

func (m *MockServerSession) ID() string { return m.id }
func (m *MockServerSession) Log(ctx context.Context, params *mcp.LoggingMessageParams) error {
    if m.logLevel == "" { return nil }  // SDK behavior: drops if no level set
    m.logCalls = append(m.logCalls, LogCall{...})
    return nil
}

// MockEventWatcher - mocks client-go watch.Interface
type MockEventWatcher struct {
    events     chan watch.Event
    closeAfter int  // close channel after N events to simulate disconnect
}

// MockPodLogFetcher - mocks pod log API
type MockPodLogFetcher struct {
    logs     map[string]string  // container -> log content
    previous map[string]string  // container -> previous log
    errors   map[string]error   // container -> error (e.g., RBAC denied)
}
```

**Unit Test Coverage**:

| Component | Test File | Key Scenarios |
|-----------|-----------|---------------|
| EventSubscriptionManager | `pkg/events/manager_test.go` | Create/cancel subscriptions, session limits, global limits, session ownership validation, cluster cleanup |
| SubscriptionFilters | `pkg/events/filters_test.go` | Validation (valid/invalid selectors), matching (namespace, labels, involved object, reason prefix) |
| Deduplication Cache | `pkg/events/dedup_test.go` | TTL expiration (5s events, 60s faults), key generation, concurrent access |
| Log Enrichment | `pkg/events/logs_test.go` | Truncation at 10KB boundary, container limit, panic/fatal/segfault detection, RBAC error handling, previous log capture |
| Watch Resilience | `pkg/events/watcher_test.go` | Exponential backoff calculation, reconnection after close, degraded state after 5 failures |
| Notification Delivery | `pkg/events/notification_test.go` | Delivery to correct session, missing session error, log level prerequisite (drops if not set) |

### Integration Tests with envtest

Integration tests use real MCP server infrastructure with `sigs.k8s.io/controller-runtime/pkg/envtest` for Kubernetes API.

**File**: `pkg/events/integration_test.go` or `pkg/http/http_events_test.go`

**Test Infrastructure**:
- `envtest.Environment` provides real Kubernetes API server (no cluster needed)
- `httptest.Server` wraps MCP StreamableHTTPHandler
- Custom MCP test client connects via HTTP, sends JSON-RPC, receives SSE notifications

**Integration Test Scenarios**:

| Test | What It Validates |
|------|-------------------|
| `TestFullSubscriptionLifecycle` | Connect → set log level → subscribe → create K8s event → receive notification → unsubscribe → no more notifications |
| `TestSessionCleanupOnDisconnect` | Subscribe → disconnect without unsubscribe → wait for monitor cycle → verify subscription removed |
| `TestSubscriptionLimits` | Create max subscriptions → next one fails with limit error |
| `TestStdioTransportRejection` | Empty session ID → subscribe fails with `--port` guidance |
| `TestCrossSessionUnsubscribeRejected` | Session A subscribes → Session B tries to unsubscribe → fails with "not found" |
| `TestFaultModeWithLogs` | Subscribe faults mode → create Warning event for Pod → receive notification with logs array |
| `TestDeduplication` | Create same event twice within window → only one notification |
| `TestWatchReconnection` | Simulate watch close → verify reconnection → events resume |
| `TestLogLevelPrerequisite` | Subscribe without setting log level → no notifications delivered |

**Test Utilities**:

```go
// MCPTestClient - helper for integration tests
type MCPTestClient struct {
    serverURL     string
    conn          *websocket.Conn  // or HTTP+SSE
    notifications chan Notification
}

func (c *MCPTestClient) Connect() error
func (c *MCPTestClient) Call(method string, params map[string]any) (map[string]any, error)
func (c *MCPTestClient) WaitForNotification(method string, timeout time.Duration) *Notification
func (c *MCPTestClient) Close() error
```

### Manual Testing with Live Server

Manual testing validates real-world scenarios with actual Kubernetes clusters.

**Prerequisites**:
```bash
# Terminal 1: Start Kind cluster
kind create cluster --name events-test

# Terminal 2: Build and run server
make build
./kubernetes-mcp-server --port 8080 -v 2
```

**Using mcp-inspector**:
```bash
# Terminal 3: Connect with inspector
npx @modelcontextprotocol/inspector http://localhost:8080

# In inspector UI:
# 1. Set Log Level → "info"
# 2. Call Tool → "events_subscribe" → {"namespace": "default", "type": "Warning"}
# 3. Observe subscriptionId in response
```

**Trigger Test Events**:
```bash
# Terminal 4: Create events
kubectl run test-pod --image=nginx

# Create Warning event
kubectl create -f - <<EOF
apiVersion: v1
kind: Event
metadata:
  name: test-warning
  namespace: default
type: Warning
reason: TestWarning
message: "Test warning event"
involvedObject:
  apiVersion: v1
  kind: Pod
  name: test-pod
  namespace: default
EOF

# Observe notification in mcp-inspector with logger="kubernetes/events"
```

**Test Crashlooping Pod (faults mode)**:
```bash
# Create crashlooping pod
kubectl run crasher --image=busybox -- /bin/sh -c "echo 'panic: test'; exit 1"

# Subscribe with mode=faults
# Wait for BackOff events → notification with logger="kubernetes/faults" and logs[]
```

**Manual Verification Checklist**:

| Test | Expected Result |
|------|-----------------|
| Connect via HTTP, set log level, subscribe | Returns subscriptionId |
| Create Warning event in subscribed namespace | Notification with logger="kubernetes/events" |
| Create event outside filter | No notification |
| Unsubscribe | No more notifications |
| Subscribe, disconnect, wait 30s | Server logs show cleanup |
| Connect via stdio, try subscribe | Error about `--port` requirement |
| Subscribe mode=faults, trigger crash | Notification with logs[] array |
| Crash with panic message | logs[].hasPanic = true |
| Hit subscription limit | Clear error message |
| Unsubscribe from different session | "not found" error |

### CI Integration

Tests integrate with existing CI pipeline:

```yaml
# In .github/workflows/test.yml
- name: Run unit tests
  run: make test TEST_FLAGS="-run TestEventSubscription"

- name: Run integration tests
  run: make test-integration TEST_FLAGS="-run TestEventSubscriptionIntegration"
  # Uses envtest - downloads K8s binaries automatically, no external cluster needed
```

### Test Configuration

Integration tests use shorter timeouts for faster execution:

```go
// Test-specific config
func TestManagerConfig() ManagerConfig {
    return ManagerConfig{
        MaxSubscriptionsPerSession: 3,    // lower for limit testing
        MaxSubscriptionsGlobal:     5,
        SessionMonitorInterval:     100 * time.Millisecond,  // faster cleanup
        EventDeduplicationWindow:   100 * time.Millisecond,
        FaultDeduplicationWindow:   200 * time.Millisecond,
    }
}
```

## Post-Archive Implementation Notes

### Fault Notification Log Level Fix (December 10, 2025)

**Issue**: Initial implementation incorrectly used `LoggingLevel("info")` for fault notifications instead of `LoggingLevel("warning")` as specified in line 49 of this design document.

**Root Cause**: Copy-paste error in `pkg/events/manager.go:483` - used "info" level when calling `sendNotification()` for fault events.

**Fix**: Changed line 483 to use `LoggingLevel("warning")` to match the design specification. This ensures fault notifications (Warning events with log enrichment) have appropriate severity for logging systems that filter by level.

**Impact**: Corrects spec compliance. Fault notifications now properly use "warning" level, making them more visible in logging systems and consistent with Kubernetes event semantics (Warning events should be treated as warnings, not info messages).
