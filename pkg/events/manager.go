package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	pkgkubernetes "github.com/containers/kubernetes-mcp-server/pkg/kubernetes"
)

// MCPServer is a minimal interface for the MCP server that the manager needs.
// This allows for easier testing with mocks.
type MCPServer interface {
	// Sessions returns an iterator over active server sessions.
	Sessions() SessionIterator
}

// SessionIterator is an iterator over server sessions.
// This matches the signature of mcp.Server.Sessions() which returns iter.Seq[*mcp.ServerSession].
type SessionIterator interface {
	// All returns a function that can be used in a for-range loop.
	// The function yields sessions one at a time.
	All(func(ServerSession) bool)
}

// ServerSession is a minimal interface for an MCP server session.
// This allows for easier testing with mocks.
type ServerSession interface {
	// ID returns the session ID. Empty string for stdio sessions.
	ID() string

	// Log sends a log notification to the client.
	Log(ctx context.Context, params *mcp.LoggingMessageParams) error
}

// Subscription represents an active event subscription.
type Subscription struct {
	ID        string
	SessionID string
	Cluster   string
	Mode      string // "events" or "faults"
	Filters   SubscriptionFilters
	Cancel    context.CancelFunc
	CreatedAt time.Time
	Degraded  bool
}

// KubernetesClientGetter is a function that returns a Kubernetes client for a given cluster.
// This allows the manager to access cluster-specific clients without tight coupling.
type KubernetesClientGetter func(cluster string) (*pkgkubernetes.Kubernetes, error)

// EventSubscriptionManager manages event subscriptions and notification delivery.
type EventSubscriptionManager struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription // subscriptionID -> Subscription
	bySession     map[string][]string      // sessionID -> []subscriptionID
	byCluster     map[string][]string      // cluster -> []subscriptionID
	server        MCPServer                // for accessing sessions
	config        ManagerConfig
	getK8sClient  KubernetesClientGetter // function to get Kubernetes client by cluster
	faultProc     *FaultProcessor        // fault processor for enriching fault events
}

// NewEventSubscriptionManager creates a new EventSubscriptionManager.
// The server parameter is used to iterate active sessions for cleanup.
// The getK8sClient function is used to obtain Kubernetes clients for starting watchers.
func NewEventSubscriptionManager(server MCPServer, config ManagerConfig, getK8sClient KubernetesClientGetter) *EventSubscriptionManager {
	return &EventSubscriptionManager{
		subscriptions: make(map[string]*Subscription),
		bySession:     make(map[string][]string),
		byCluster:     make(map[string][]string),
		server:        server,
		config:        config,
		getK8sClient:  getK8sClient,
		faultProc:     NewFaultProcessor(config),
	}
}

// Create creates a new subscription and returns it.
// Returns an error if limits are exceeded or validation fails.
func (m *EventSubscriptionManager) Create(sessionID, cluster, mode string, filters SubscriptionFilters) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate filters
	if err := filters.ValidateForMode(mode); err != nil {
		return nil, fmt.Errorf("invalid filters: %w", err)
	}

	// Validate mode
	if mode != "events" && mode != "faults" {
		return nil, fmt.Errorf("invalid mode: must be 'events' or 'faults'")
	}

	// Check session subscription limit
	sessionSubs := m.bySession[sessionID]
	if len(sessionSubs) >= m.config.MaxSubscriptionsPerSession {
		return nil, fmt.Errorf("session has reached maximum subscriptions (%d)", m.config.MaxSubscriptionsPerSession)
	}

	// Check global subscription limit
	if len(m.subscriptions) >= m.config.MaxSubscriptionsGlobal {
		return nil, fmt.Errorf("server has reached maximum subscriptions (%d)", m.config.MaxSubscriptionsGlobal)
	}

	// Create subscription with unique ID
	sub := &Subscription{
		ID:        generateSubscriptionID(),
		SessionID: sessionID,
		Cluster:   cluster,
		Mode:      mode,
		Filters:   filters,
		CreatedAt: time.Now(),
		Degraded:  false,
	}

	// Track subscription
	m.subscriptions[sub.ID] = sub
	m.bySession[sessionID] = append(m.bySession[sessionID], sub.ID)
	m.byCluster[cluster] = append(m.byCluster[cluster], sub.ID)

	klog.V(1).Infof("Created subscription %s for session %s (cluster=%s, mode=%s)", sub.ID, sessionID, cluster, mode)

	// Start the watcher for this subscription (if getK8sClient is configured)
	if m.getK8sClient != nil {
		if err := m.startWatcher(sub); err != nil {
			// Clean up the subscription if watcher fails to start
			m.cancelSubscriptionLocked(sub)
			return nil, fmt.Errorf("failed to start watcher: %w", err)
		}
	}

	return sub, nil
}

// Cancel cancels a subscription by ID.
// Returns an error if the subscription doesn't exist.
func (m *EventSubscriptionManager) Cancel(subscriptionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscriptions[subscriptionID]
	if !exists {
		return fmt.Errorf("subscription not found")
	}

	m.cancelSubscriptionLocked(sub)
	return nil
}

// CancelBySessionAndID cancels a subscription by ID, but only if it belongs to the given session.
// This enforces session ownership and prevents cross-session cancellation.
// Returns an error if the subscription doesn't exist or doesn't belong to the session.
func (m *EventSubscriptionManager) CancelBySessionAndID(sessionID, subscriptionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscriptions[subscriptionID]
	if !exists {
		return fmt.Errorf("subscription not found")
	}

	if sub.SessionID != sessionID {
		return fmt.Errorf("subscription not found")
	}

	m.cancelSubscriptionLocked(sub)
	return nil
}

// CancelSession cancels all subscriptions for a session.
func (m *EventSubscriptionManager) CancelSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelSessionLocked(sessionID)
}

// CancelCluster cancels all subscriptions for a given cluster.
// This is called when a cluster configuration is removed.
func (m *EventSubscriptionManager) CancelCluster(cluster string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subIDs := m.byCluster[cluster]
	for _, subID := range subIDs {
		if sub, exists := m.subscriptions[subID]; exists {
			m.cancelSubscriptionLocked(sub)
		}
	}
}

// CancelAll cancels all active subscriptions.
// This is called during server shutdown.
func (m *EventSubscriptionManager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sub := range m.subscriptions {
		m.cancelSubscriptionLocked(sub)
	}
}

// GetSubscription returns a subscription by ID, or nil if not found.
func (m *EventSubscriptionManager) GetSubscription(subscriptionID string) *Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.subscriptions[subscriptionID]
}

// ListSubscriptionsForSession returns all subscriptions for a given session.
func (m *EventSubscriptionManager) ListSubscriptionsForSession(sessionID string) []*Subscription {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subIDs := m.bySession[sessionID]
	subs := make([]*Subscription, 0, len(subIDs))

	for _, subID := range subIDs {
		if sub, exists := m.subscriptions[subID]; exists {
			subs = append(subs, sub)
		}
	}

	return subs
}

// GetStats returns statistics about active subscriptions.
func (m *EventSubscriptionManager) GetStats() SubscriptionStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return SubscriptionStats{
		Total:    len(m.subscriptions),
		Sessions: len(m.bySession),
		Clusters: len(m.byCluster),
		Degraded: m.countDegradedLocked(),
	}
}

// SubscriptionStats holds statistics about subscriptions.
type SubscriptionStats struct {
	Total    int
	Sessions int
	Clusters int
	Degraded int
}

// cancelSessionLocked cancels all subscriptions for a session. Must be called with lock held.
func (m *EventSubscriptionManager) cancelSessionLocked(sessionID string) {
	subIDs := m.bySession[sessionID]
	for _, subID := range subIDs {
		if sub, exists := m.subscriptions[subID]; exists {
			m.cancelSubscriptionLocked(sub)
		}
	}
	klog.V(1).Infof("Cancelled all subscriptions for session %s", sessionID)
}

// cancelSubscriptionLocked cancels a subscription. Must be called with lock held.
func (m *EventSubscriptionManager) cancelSubscriptionLocked(sub *Subscription) {
	// Call cancel function if set
	if sub.Cancel != nil {
		sub.Cancel()
	}

	// Remove from indices
	delete(m.subscriptions, sub.ID)

	// Remove from session index
	sessionSubs := m.bySession[sub.SessionID]
	for i, id := range sessionSubs {
		if id == sub.ID {
			m.bySession[sub.SessionID] = append(sessionSubs[:i], sessionSubs[i+1:]...)
			break
		}
	}
	if len(m.bySession[sub.SessionID]) == 0 {
		delete(m.bySession, sub.SessionID)
	}

	// Remove from cluster index
	clusterSubs := m.byCluster[sub.Cluster]
	for i, id := range clusterSubs {
		if id == sub.ID {
			m.byCluster[sub.Cluster] = append(clusterSubs[:i], clusterSubs[i+1:]...)
			break
		}
	}
	if len(m.byCluster[sub.Cluster]) == 0 {
		delete(m.byCluster, sub.Cluster)
	}

	klog.V(1).Infof("Cancelled subscription %s", sub.ID)
}

// countDegradedLocked counts degraded subscriptions. Must be called with lock held.
func (m *EventSubscriptionManager) countDegradedLocked() int {
	count := 0
	for _, sub := range m.subscriptions {
		if sub.Degraded {
			count++
		}
	}
	return count
}

// StartSessionMonitor starts a background goroutine that periodically checks for stale sessions.
func (m *EventSubscriptionManager) StartSessionMonitor(ctx context.Context) {
	ticker := time.NewTicker(m.config.SessionMonitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.V(1).Info("Session monitor shutting down, cancelling all subscriptions")
			m.CancelAll()
			return
		case <-ticker.C:
			m.cleanupStaleSessions()
		}
	}
}

// cleanupStaleSessions removes subscriptions for sessions that no longer exist.
func (m *EventSubscriptionManager) cleanupStaleSessions() {
	// Build set of active session IDs
	activeSessions := make(map[string]bool)
	activeSessionList := []string{}
	if m.server != nil {
		m.server.Sessions().All(func(session ServerSession) bool {
			if id := session.ID(); id != "" {
				activeSessions[id] = true
				activeSessionList = append(activeSessionList, id)
			}
			return true // continue iteration
		})
	}

	klog.V(1).Infof("Cleanup cycle: MCP server reports %d active sessions: %v", len(activeSessions), activeSessionList)

	// Find and cancel stale sessions
	m.mu.Lock()
	defer m.mu.Unlock()

	subscriptionSessionList := []string{}
	for sessionID := range m.bySession {
		subscriptionSessionList = append(subscriptionSessionList, sessionID)
	}
	klog.V(1).Infof("Cleanup cycle: checking %d subscription sessions: %v", len(m.bySession), subscriptionSessionList)

	for sessionID := range m.bySession {
		if !activeSessions[sessionID] {
			klog.Infof("🧹 CLEANUP: Removing stale session %s with %d subscriptions", sessionID, len(m.bySession[sessionID]))
			m.cancelSessionLocked(sessionID)
		}
	}
}

// sendNotification sends a notification to a specific session.
// Uses a timeout context to detect dead connections.
func (m *EventSubscriptionManager) sendNotification(sessionID string, logger string, level mcp.LoggingLevel, data any) error {
	// Find the target session
	var targetSession ServerSession
	m.server.Sessions().All(func(session ServerSession) bool {
		if session.ID() == sessionID {
			targetSession = session
			return false // stop iteration
		}
		return true // continue iteration
	})

	if targetSession == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	// Use a short timeout to detect dead connections
	// If the SSE connection is dead, this should fail quickly
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := targetSession.Log(ctx, &mcp.LoggingMessageParams{
		Level:  level,
		Logger: logger,
		Data:   data,
	})
	if err != nil {
		klog.Warningf("Failed to send notification to session %s: %v", sessionID, err)
		return err
	}
	klog.V(1).Infof("Sent notification to session %s (logger=%s, level=%s)", sessionID, logger, level)
	return nil
}

// generateSubscriptionID generates a unique subscription ID.
func generateSubscriptionID() string {
	return fmt.Sprintf("sub-%s", uuid.New().String()[:8])
}

// getCurrentResourceVersion gets the current resource version for events in the specified namespace.
//
// This method is used to start watches from "now" instead of receiving all historical events
// when a new subscription is created. By obtaining the current resource version before starting
// the watch, we ensure that only events occurring after subscription creation are delivered.
//
// The method performs a List operation with Limit=1 to efficiently obtain just the resource version
// without retrieving actual event data. This minimizes API server load and network bandwidth.
//
// Parameters:
//   - clientset: The Kubernetes clientset to use for the List operation
//   - namespace: The namespace to list events from. If empty (""), lists cluster-wide events.
//
// Returns:
//   - The current resource version as a string (may be empty if no events exist)
//   - An error if the List operation fails
//
// The operation has a 5-second timeout to prevent hanging on unavailable API servers.
func (m *EventSubscriptionManager) getCurrentResourceVersion(clientset kubernetes.Interface, namespace string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := metav1.ListOptions{
		Limit: 1, // We only need the resource version, not the actual events
	}

	var list *v1.EventList
	var err error

	if namespace == "" {
		// Cluster-wide watch
		list, err = clientset.CoreV1().Events(metav1.NamespaceAll).List(ctx, opts)
	} else {
		// Namespace-scoped watch
		list, err = clientset.CoreV1().Events(namespace).List(ctx, opts)
	}

	if err != nil {
		return "", fmt.Errorf("failed to list events: %w", err)
	}

	return list.ResourceVersion, nil
}

// startWatcher starts an EventWatcher for the given subscription.
// This method must be called with the lock held.
func (m *EventSubscriptionManager) startWatcher(sub *Subscription) error {
	// Get Kubernetes client for the cluster
	if m.getK8sClient == nil {
		return fmt.Errorf("kubernetes client getter not configured")
	}

	k8s, err := m.getK8sClient(sub.Cluster)
	if err != nil {
		return fmt.Errorf("failed to get kubernetes client: %w", err)
	}

	// The Kubernetes client embeds kubernetes.Interface directly
	clientset := k8s

	// Create deduplication cache based on mode
	var dedupCache *DeduplicationCache
	if sub.Mode == "events" {
		dedupCache = NewDeduplicationCache(m.config.EventDeduplicationWindow)
	} else {
		// Faults use their own deduplication logic in FaultProcessor
		dedupCache = nil
	}

	// Create context for the watcher
	ctx, cancel := context.WithCancel(context.Background())
	sub.Cancel = cancel

	// Determine namespace for watcher
	namespace := ""
	if len(sub.Filters.Namespaces) == 1 {
		// Single namespace: use namespace-scoped watch
		namespace = sub.Filters.Namespaces[0]
	}
	// Multiple namespaces or empty: use cluster-wide watch with client-side filtering

	// Get current resource version to start from "now" and skip historical events
	initialResourceVersion, err := m.getCurrentResourceVersion(clientset, namespace)
	if err != nil {
		return fmt.Errorf("failed to get current resource version: %w", err)
	}
	klog.V(1).Infof("Starting subscription %s from resource version %s (filtering historical events)", sub.ID, initialResourceVersion)

	// Create the event watcher
	watcher := NewEventWatcher(EventWatcherConfig{
		Clientset:              clientset,
		Namespace:              namespace,
		Filters:                &sub.Filters,
		MaxRetries:             m.config.WatchReconnectMaxRetries,
		InitialResourceVersion: initialResourceVersion,
		OnError: func(err error) {
			klog.Warningf("Watch error for subscription %s: %v", sub.ID, err)
		},
		OnDegraded: func() {
			m.markSubscriptionDegraded(sub.ID)
		},
		DedupCache:   dedupCache,
		ProcessEvent: m.makeProcessEventFunc(ctx, sub, k8s),
	})

	// Start the watcher in the background
	watcher.Start(ctx)

	klog.V(1).Infof("Started watcher for subscription %s (cluster=%s, mode=%s, namespace=%s)", sub.ID, sub.Cluster, sub.Mode, namespace)

	return nil
}

// makeProcessEventFunc creates a callback function for processing events based on subscription mode
func (m *EventSubscriptionManager) makeProcessEventFunc(ctx context.Context, sub *Subscription, k8s *pkgkubernetes.Kubernetes) func(*v1.Event) {
	if sub.Mode == "faults" {
		// Fault mode: enrich with logs
		return func(event *v1.Event) {
			faultEvent, err := m.faultProc.ProcessEvent(ctx, k8s, sub.Cluster, sub.ID, event)
			if err != nil {
				klog.Warningf("Failed to process fault event for subscription %s: %v", sub.ID, err)
				return
			}
			if faultEvent == nil {
				// Event was filtered out (not a fault or duplicate)
				return
			}

			// Send fault notification
			err = m.sendNotification(sub.SessionID, LoggerFaults, mcp.LoggingLevel("warning"), faultEvent)
			if err != nil {
				// Any error sending notification means the session is dead - cancel immediately
				klog.V(1).Infof("Session %s unreachable (error: %v), cancelling subscription %s", sub.SessionID, err, sub.ID)
				go func() {
					if cancelErr := m.CancelBySessionAndID(sub.SessionID, sub.ID); cancelErr != nil {
						klog.V(2).Infof("Failed to auto-cancel subscription %s: %v", sub.ID, cancelErr)
					}
				}()
			}
		}
	}

	// Events mode: send event notification directly
	return func(event *v1.Event) {
		notification := &EventNotification{
			SubscriptionID: sub.ID,
			Cluster:        sub.Cluster,
			Event:          SerializeEvent(event),
		}

		err := m.sendNotification(sub.SessionID, LoggerEvents, mcp.LoggingLevel("info"), notification)
		if err != nil {
			// Any error sending notification means the session is dead - cancel immediately
			klog.V(1).Infof("Session %s unreachable (error: %v), cancelling subscription %s", sub.SessionID, err, sub.ID)
			go func() {
				if cancelErr := m.CancelBySessionAndID(sub.SessionID, sub.ID); cancelErr != nil {
					klog.V(2).Infof("Failed to auto-cancel subscription %s: %v", sub.ID, cancelErr)
				}
			}()
		}
	}
}

// markSubscriptionDegraded marks a subscription as degraded
func (m *EventSubscriptionManager) markSubscriptionDegraded(subscriptionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, exists := m.subscriptions[subscriptionID]
	if !exists {
		return
	}

	if !sub.Degraded {
		sub.Degraded = true
		klog.Warningf("Subscription %s marked as degraded", subscriptionID)

		// Send degraded notification to the session
		notification := &SubscriptionErrorNotification{
			SubscriptionID: subscriptionID,
			Cluster:        sub.Cluster,
			Error:          "Watch connection failed after maximum retry attempts",
			Degraded:       true,
		}

		err := m.sendNotification(sub.SessionID, LoggerSubscriptionError, mcp.LoggingLevel("warning"), notification)
		if err != nil {
			// Any error sending notification means the session is dead - cancel immediately
			klog.V(1).Infof("Session %s unreachable (error: %v), cancelling subscription %s", sub.SessionID, err, subscriptionID)
			go func() {
				if cancelErr := m.CancelBySessionAndID(sub.SessionID, subscriptionID); cancelErr != nil {
					klog.V(2).Infof("Failed to auto-cancel subscription %s: %v", subscriptionID, cancelErr)
				}
			}()
		}
	}
}

// ManagerAdapter adapts EventSubscriptionManager to the api.EventSubscriptionManager interface.
// This avoids circular dependencies between pkg/api and pkg/events.
type ManagerAdapter struct {
	*EventSubscriptionManager
}

// Create adapts the Create method to use interface{} types.
func (a *ManagerAdapter) Create(sessionID, cluster, mode string, filters interface{}) (interface{}, error) {
	subscriptionFilters, ok := filters.(SubscriptionFilters)
	if !ok {
		return nil, fmt.Errorf("invalid filters type: expected SubscriptionFilters")
	}
	return a.EventSubscriptionManager.Create(sessionID, cluster, mode, subscriptionFilters)
}

// ListSubscriptionsForSession adapts the ListSubscriptionsForSession method to return interface{}.
func (a *ManagerAdapter) ListSubscriptionsForSession(sessionID string) interface{} {
	return a.EventSubscriptionManager.ListSubscriptionsForSession(sessionID)
}
