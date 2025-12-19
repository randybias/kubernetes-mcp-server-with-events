package events

import (
	"context"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/watch"
)

// MockMCPServer implements MCPServer for testing.
type MockMCPServer struct {
	sessions []*MockServerSession
	mu       sync.RWMutex
}

// NewMockMCPServer creates a new mock MCP server.
func NewMockMCPServer() *MockMCPServer {
	return &MockMCPServer{
		sessions: make([]*MockServerSession, 0),
	}
}

// AddSession adds a mock session to the server.
func (m *MockMCPServer) AddSession(session *MockServerSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = append(m.sessions, session)
}

// RemoveSession removes a mock session from the server.
func (m *MockMCPServer) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sessions {
		if s.ID() == sessionID {
			m.sessions = append(m.sessions[:i], m.sessions[i+1:]...)
			return
		}
	}
}

// Sessions returns an iterator over mock sessions.
func (m *MockMCPServer) Sessions() SessionIterator {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Create a copy of sessions to avoid race conditions
	sessionsCopy := make([]*MockServerSession, len(m.sessions))
	copy(sessionsCopy, m.sessions)

	return &mockSessionIterator{sessions: sessionsCopy}
}

// mockSessionIterator implements SessionIterator for testing.
type mockSessionIterator struct {
	sessions []*MockServerSession
}

// All implements the iterator pattern for sessions.
func (m *mockSessionIterator) All(yield func(ServerSession) bool) {
	for _, session := range m.sessions {
		if !yield(session) {
			return
		}
	}
}

// MockServerSession implements ServerSession for testing.
type MockServerSession struct {
	id       string
	logLevel mcp.LoggingLevel
	logCalls []LogCall
	mu       sync.Mutex
}

// LogCall captures a single Log() call for assertions.
type LogCall struct {
	Ctx    context.Context
	Level  mcp.LoggingLevel
	Logger string
	Data   interface{}
}

// NewMockServerSession creates a new mock server session.
func NewMockServerSession(id string) *MockServerSession {
	return &MockServerSession{
		id:       id,
		logCalls: make([]LogCall, 0),
	}
}

// ID returns the session ID.
func (m *MockServerSession) ID() string {
	return m.id
}

// SetLogLevel sets the log level for this session.
// When empty, Log() calls are dropped (matching SDK behavior).
func (m *MockServerSession) SetLogLevel(level mcp.LoggingLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logLevel = level
}

// Log captures a log call for testing assertions.
// Mimics SDK behavior: drops logs if no level is set.
func (m *MockServerSession) Log(ctx context.Context, params *mcp.LoggingMessageParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Mimic SDK behavior: drop if no log level set
	if m.logLevel == "" {
		return nil
	}

	m.logCalls = append(m.logCalls, LogCall{
		Ctx:    ctx,
		Level:  params.Level,
		Logger: params.Logger,
		Data:   params.Data,
	})

	return nil
}

// GetLogCalls returns all captured log calls.
func (m *MockServerSession) GetLogCalls() []LogCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	calls := make([]LogCall, len(m.logCalls))
	copy(calls, m.logCalls)
	return calls
}

// ClearLogCalls clears all captured log calls.
func (m *MockServerSession) ClearLogCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logCalls = make([]LogCall, 0)
}

// FindLogCall finds the first log call matching the given logger name.
// Returns nil if not found.
func (m *MockServerSession) FindLogCall(logger string) *LogCall {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, call := range m.logCalls {
		if call.Logger == logger {
			return &call
		}
	}
	return nil
}

// MockEventWatcher implements watch.Interface for testing.
type MockEventWatcher struct {
	events     chan watch.Event
	mu         sync.Mutex
	closeAfter int // close channel after N events (0 = never)
	eventCount int
}

// NewMockEventWatcher creates a new mock event watcher.
func NewMockEventWatcher() *MockEventWatcher {
	return &MockEventWatcher{
		events: make(chan watch.Event, 10),
	}
}

// NewMockEventWatcherWithCloseAfter creates a mock watcher that closes after N events.
func NewMockEventWatcherWithCloseAfter(closeAfter int) *MockEventWatcher {
	return &MockEventWatcher{
		events:     make(chan watch.Event, 10),
		closeAfter: closeAfter,
	}
}

// Stop stops the watch.
func (m *MockEventWatcher) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case <-m.events:
		// Already closed
	default:
		close(m.events)
	}
}

// ResultChan returns the event channel.
func (m *MockEventWatcher) ResultChan() <-chan watch.Event {
	return m.events
}

// SendEvent sends an event to the watcher.
// Automatically closes the channel if closeAfter is reached.
func (m *MockEventWatcher) SendEvent(event watch.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()

	select {
	case m.events <- event:
		m.eventCount++
		if m.closeAfter > 0 && m.eventCount >= m.closeAfter {
			close(m.events)
		}
	default:
		// Channel full or closed, ignore
	}
}

// NewTestManagerConfig returns a ManagerConfig with short timeouts for testing.
func NewTestManagerConfig() ManagerConfig {
	return ManagerConfig{
		MaxSubscriptionsPerSession:   3, // Lower for limit testing
		MaxSubscriptionsGlobal:       5,
		MaxLogCapturesPerCluster:     2,
		MaxLogCapturesGlobal:         4,
		MaxLogBytesPerContainer:      1024, // 1KB for testing
		MaxContainersPerNotification: 2,
		EventDeduplicationWindow:     100 * time.Millisecond, // 100ms for fast tests
		FaultDeduplicationWindow:     200 * time.Millisecond, // 200ms for fast tests
		SessionMonitorInterval:       100 * time.Millisecond, // 100ms for fast cleanup tests
		WatchReconnectMaxRetries:     3,                      // Fewer retries for faster tests
	}
}
