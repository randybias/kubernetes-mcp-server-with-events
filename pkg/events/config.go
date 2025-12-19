package events

import "time"

// ManagerConfig holds configuration for the EventSubscriptionManager.
// All fields have sensible defaults specified in the design document.
type ManagerConfig struct {
	// MaxSubscriptionsPerSession limits the number of active subscriptions per session.
	// Default: 10
	MaxSubscriptionsPerSession int

	// MaxSubscriptionsGlobal limits the total number of active subscriptions across all sessions.
	// Default: 100
	MaxSubscriptionsGlobal int

	// MaxLogCapturesPerCluster limits concurrent log capture operations per cluster.
	// Default: 5
	MaxLogCapturesPerCluster int

	// MaxLogCapturesGlobal limits concurrent log capture operations globally.
	// Default: 20
	MaxLogCapturesGlobal int

	// MaxLogBytesPerContainer limits the amount of log data captured per container.
	// Default: 10240 (10KB)
	MaxLogBytesPerContainer int

	// MaxContainersPerNotification limits the number of containers included in fault notifications.
	// Default: 5
	MaxContainersPerNotification int

	// EventDeduplicationWindow specifies the time window for deduplicating event notifications.
	// Default: 5s
	EventDeduplicationWindow time.Duration

	// FaultDeduplicationWindow specifies the time window for deduplicating fault notifications.
	// Default: 60s
	FaultDeduplicationWindow time.Duration

	// SessionMonitorInterval specifies how often to check for stale sessions.
	// Default: 30s (must be long enough to tolerate brief network interruptions)
	SessionMonitorInterval time.Duration

	// WatchReconnectMaxRetries specifies the maximum number of watch reconnection attempts.
	// Default: 5
	WatchReconnectMaxRetries int
}

// DefaultManagerConfig returns a ManagerConfig with sensible defaults
// as specified in the design document.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		MaxSubscriptionsPerSession:   10,
		MaxSubscriptionsGlobal:       100,
		MaxLogCapturesPerCluster:     5,
		MaxLogCapturesGlobal:         20,
		MaxLogBytesPerContainer:      10240, // 10KB
		MaxContainersPerNotification: 5,
		EventDeduplicationWindow:     5 * time.Second,
		FaultDeduplicationWindow:     60 * time.Second,
		SessionMonitorInterval:       30 * time.Second,
		WatchReconnectMaxRetries:     5,
	}
}
