package events

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/suite"
)

type NotificationTestSuite struct {
	suite.Suite
	server  *MockMCPServer
	manager *EventSubscriptionManager
	config  ManagerConfig
}

func (s *NotificationTestSuite) SetupTest() {
	s.server = NewMockMCPServer()
	s.config = NewTestManagerConfig()
	// For tests, use nil getK8sClient since we don't start watchers
	s.manager = NewEventSubscriptionManager(s.server, s.config, nil, nil)
}

func TestNotificationSuite(t *testing.T) {
	suite.Run(t, new(NotificationTestSuite))
}

// TestSendNotification_DeliversToCorrectSession tests that sendNotification() delivers to the correct session
func (s *NotificationTestSuite) TestSendNotification_DeliversToCorrectSession() {
	s.Run("delivers notification to target session", func() {
		// Create mock sessions
		session1 := NewMockServerSession("session1")
		session1.SetLogLevel(mcp.LoggingLevel("info"))
		session2 := NewMockServerSession("session2")
		session2.SetLogLevel(mcp.LoggingLevel("info"))

		s.server.AddSession(session1)
		s.server.AddSession(session2)

		// Send notification to session1
		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		// Verify session1 received the notification
		calls := session1.GetLogCalls()
		s.Len(calls, 1)
		s.Equal(LoggerEvents, calls[0].Logger)
		s.Equal(mcp.LoggingLevel("info"), calls[0].Level)

		// Verify session2 did not receive the notification
		calls2 := session2.GetLogCalls()
		s.Len(calls2, 0)
	})

	s.Run("delivers to correct session among multiple sessions", func() {
		// Create multiple sessions
		session1 := NewMockServerSession("session1")
		session1.SetLogLevel(mcp.LoggingLevel("info"))
		session2 := NewMockServerSession("session2")
		session2.SetLogLevel(mcp.LoggingLevel("info"))
		session3 := NewMockServerSession("session3")
		session3.SetLogLevel(mcp.LoggingLevel("info"))

		s.server.AddSession(session1)
		s.server.AddSession(session2)
		s.server.AddSession(session3)

		// Send notification to session2
		notification := EventNotification{
			SubscriptionID: "sub-456",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session2", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		// Verify only session2 received the notification
		s.Len(session1.GetLogCalls(), 0)
		s.Len(session2.GetLogCalls(), 1)
		s.Len(session3.GetLogCalls(), 0)
	})
}

// TestSendNotification_ReturnsErrorForMissingSession tests that sendNotification() returns error for missing session
func (s *NotificationTestSuite) TestSendNotification_ReturnsErrorForMissingSession() {
	s.Run("returns error when session not found", func() {
		// Create one session
		session1 := NewMockServerSession("session1")
		session1.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session1)

		// Try to send to non-existent session
		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("non-existent-session", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.Error(err)
		s.Contains(err.Error(), "not found")
	})

	s.Run("returns error when no sessions exist", func() {
		// Don't add any sessions

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.Error(err)
		s.Contains(err.Error(), "not found")
	})
}

// TestNotificationDroppedWhenNoLogLevel tests that notifications are dropped when no log level is set
func (s *NotificationTestSuite) TestNotificationDroppedWhenNoLogLevel() {
	s.Run("drops notification when log level not set", func() {
		// Create session without setting log level
		session := NewMockServerSession("session1")
		// Don't call SetLogLevel - mimics SDK behavior
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err) // No error, but notification is dropped

		// Verify no log calls were captured
		calls := session.GetLogCalls()
		s.Len(calls, 0, "notification should be dropped when no log level set")
	})

	s.Run("delivers notification when log level is set", func() {
		// Create session with log level
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		// Verify log call was captured
		calls := session.GetLogCalls()
		s.Len(calls, 1, "notification should be delivered when log level is set")
	})
}

// TestCorrectLoggerNames tests that correct logger names are used for different notification types
func (s *NotificationTestSuite) TestCorrectLoggerNames() {
	s.Run("uses correct logger name for events", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(LoggerEvents, calls[0].Logger)
		s.Equal("kubernetes/events", calls[0].Logger)
	})

	s.Run("uses correct logger name for faults", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("warning"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerFaults, mcp.LoggingLevel("warning"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(LoggerFaults, calls[0].Logger)
		s.Equal("kubernetes/faults", calls[0].Logger)
	})

	s.Run("uses correct logger name for subscription errors", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("error"))
		s.server.AddSession(session)

		notification := SubscriptionErrorNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
			Error:          "connection lost",
			Degraded:       true,
		}

		err := s.manager.sendNotification("session1", LoggerSubscriptionError, mcp.LoggingLevel("error"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(LoggerSubscriptionError, calls[0].Logger)
		s.Equal("kubernetes/subscription_error", calls[0].Logger)
	})
}

// TestNotificationData tests that notification data is correctly passed
func (s *NotificationTestSuite) TestNotificationData() {
	s.Run("passes event notification data correctly", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
			Event: &EventDetails{
				Namespace: "default",
				Type:      "Warning",
				Reason:    "BackOff",
				Message:   "Back-off restarting failed container",
			},
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)

		// Verify data is passed correctly
		data, ok := calls[0].Data.(EventNotification)
		s.True(ok, "data should be EventNotification type")
		s.Equal("sub-123", data.SubscriptionID)
		s.Equal("test-cluster", data.Cluster)
		s.Equal("default", data.Event.Namespace)
		s.Equal("Warning", data.Event.Type)
		s.Equal("BackOff", data.Event.Reason)
	})

	s.Run("passes error notification data correctly", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("error"))
		s.server.AddSession(session)

		notification := SubscriptionErrorNotification{
			SubscriptionID: "sub-456",
			Cluster:        "prod-cluster",
			Error:          "watch connection failed",
			Degraded:       true,
		}

		err := s.manager.sendNotification("session1", LoggerSubscriptionError, mcp.LoggingLevel("error"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)

		// Verify data is passed correctly
		data, ok := calls[0].Data.(SubscriptionErrorNotification)
		s.True(ok, "data should be SubscriptionErrorNotification type")
		s.Equal("sub-456", data.SubscriptionID)
		s.Equal("prod-cluster", data.Cluster)
		s.Equal("watch connection failed", data.Error)
		s.True(data.Degraded)
	})
}

// TestNotificationLogLevel tests that notifications use correct log levels
func (s *NotificationTestSuite) TestNotificationLogLevel() {
	s.Run("uses Info level for normal events", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(mcp.LoggingLevel("info"), calls[0].Level)
	})

	s.Run("uses Error level for error notifications", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("error"))
		s.server.AddSession(session)

		notification := SubscriptionErrorNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
			Error:          "connection lost",
		}

		err := s.manager.sendNotification("session1", LoggerSubscriptionError, mcp.LoggingLevel("error"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(mcp.LoggingLevel("error"), calls[0].Level)
	})

	s.Run("uses Warning level for fault events", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("warning"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerFaults, mcp.LoggingLevel("warning"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Equal(mcp.LoggingLevel("warning"), calls[0].Level)
	})
}

// TestNotificationContext tests that notifications use correct context
func (s *NotificationTestSuite) TestNotificationContext() {
	s.Run("uses background context for SSE routing", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		notification := EventNotification{
			SubscriptionID: "sub-123",
			Cluster:        "test-cluster",
		}

		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)

		// Verify context is background context
		s.Equal(context.Background(), calls[0].Ctx)
	})
}

// TestMultipleNotifications tests sending multiple notifications
func (s *NotificationTestSuite) TestMultipleNotifications() {
	s.Run("delivers multiple notifications in order", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		// Send multiple notifications
		for i := 1; i <= 5; i++ {
			notification := EventNotification{
				SubscriptionID: "sub-123",
				Cluster:        "test-cluster",
			}

			err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
			s.NoError(err)
		}

		// Verify all notifications were delivered
		calls := session.GetLogCalls()
		s.Len(calls, 5)

		// Verify all have correct logger
		for _, call := range calls {
			s.Equal(LoggerEvents, call.Logger)
		}
	})

	s.Run("delivers to multiple sessions independently", func() {
		session1 := NewMockServerSession("session1")
		session1.SetLogLevel(mcp.LoggingLevel("info"))
		session2 := NewMockServerSession("session2")
		session2.SetLogLevel(mcp.LoggingLevel("info"))

		s.server.AddSession(session1)
		s.server.AddSession(session2)

		// Send different numbers of notifications to each session
		for i := 0; i < 3; i++ {
			notification := EventNotification{
				SubscriptionID: "sub-1",
				Cluster:        "cluster1",
			}
			err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
			s.NoError(err)
		}

		for i := 0; i < 5; i++ {
			notification := EventNotification{
				SubscriptionID: "sub-2",
				Cluster:        "cluster2",
			}
			err := s.manager.sendNotification("session2", LoggerFaults, mcp.LoggingLevel("warning"), notification)
			s.NoError(err)
		}

		// Verify correct counts
		s.Len(session1.GetLogCalls(), 3)
		s.Len(session2.GetLogCalls(), 5)

		// Verify correct loggers
		for _, call := range session1.GetLogCalls() {
			s.Equal(LoggerEvents, call.Logger)
		}
		for _, call := range session2.GetLogCalls() {
			s.Equal(LoggerFaults, call.Logger)
		}
	})
}

// TestLoggerConstants tests the logger name constants
func (s *NotificationTestSuite) TestLoggerConstants() {
	s.Run("logger constants have expected values", func() {
		s.Equal("kubernetes/events", LoggerEvents)
		s.Equal("kubernetes/faults", LoggerFaults)
		s.Equal("kubernetes/subscription_error", LoggerSubscriptionError)
	})
}

// TestNotificationWithNilData tests handling of nil data
func (s *NotificationTestSuite) TestNotificationWithNilData() {
	s.Run("handles nil data gracefully", func() {
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		// Send notification with nil data
		err := s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), nil)
		s.NoError(err)

		calls := session.GetLogCalls()
		s.Require().Len(calls, 1)
		s.Nil(calls[0].Data)
	})
}

// TestSessionRemoval tests behavior when session is removed
func (s *NotificationTestSuite) TestSessionRemoval() {
	s.Run("returns error when session removed after subscription created", func() {
		// Create and add session
		session := NewMockServerSession("session1")
		session.SetLogLevel(mcp.LoggingLevel("info"))
		s.server.AddSession(session)

		// Create subscription
		filters := SubscriptionFilters{}
		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Remove session from server
		s.server.RemoveSession("session1")

		// Try to send notification
		notification := EventNotification{
			SubscriptionID: sub.ID,
			Cluster:        "cluster1",
		}

		err = s.manager.sendNotification("session1", LoggerEvents, mcp.LoggingLevel("info"), notification)
		s.Error(err)
		s.Contains(err.Error(), "not found")
	})
}
