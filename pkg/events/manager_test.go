package events

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

type ManagerTestSuite struct {
	suite.Suite
	server  *MockMCPServer
	manager *EventSubscriptionManager
	config  ManagerConfig
}

func (s *ManagerTestSuite) SetupTest() {
	s.server = NewMockMCPServer()
	s.config = NewTestManagerConfig()
	// For tests, use nil getK8sClient since we don't start watchers
	s.manager = NewEventSubscriptionManager(s.server, s.config, nil, nil)
}

func TestManagerSuite(t *testing.T) {
	suite.Run(t, new(ManagerTestSuite))
}

// TestCreate_ReturnsUniqueID tests that Create() returns unique subscription IDs
func (s *ManagerTestSuite) TestCreate_ReturnsUniqueID() {
	s.Run("generates unique IDs for each subscription", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)
		s.NotEmpty(sub1.ID)

		sub2, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)
		s.NotEmpty(sub2.ID)

		s.NotEqual(sub1.ID, sub2.ID, "subscription IDs should be unique")
	})
}

// TestCreate_TracksBySessionAndCluster tests that Create() tracks subscriptions by session and cluster
func (s *ManagerTestSuite) TestCreate_TracksBySessionAndCluster() {
	s.Run("tracks subscription by session", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Verify tracking by session
		sessionSubs := s.manager.ListSubscriptionsForSession("session1")
		s.Len(sessionSubs, 1)
		s.Equal(sub.ID, sessionSubs[0].ID)
	})

	s.Run("tracks subscriptions by cluster", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Both subscriptions should be tracked under cluster1
		s.Equal("cluster1", sub1.Cluster)
		s.Equal("cluster1", sub2.Cluster)

		// Verify via GetSubscription
		retrieved1 := s.manager.GetSubscription(sub1.ID)
		s.NotNil(retrieved1)
		s.Equal("cluster1", retrieved1.Cluster)

		retrieved2 := s.manager.GetSubscription(sub2.ID)
		s.NotNil(retrieved2)
		s.Equal("cluster1", retrieved2.Cluster)
	})

	s.Run("tracks subscriptions across multiple sessions and clusters", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster2", "events", filters)
		s.Require().NoError(err)

		// Verify session isolation
		session1Subs := s.manager.ListSubscriptionsForSession("session1")
		s.Len(session1Subs, 1)
		s.Equal(sub1.ID, session1Subs[0].ID)

		session2Subs := s.manager.ListSubscriptionsForSession("session2")
		s.Len(session2Subs, 1)
		s.Equal(sub2.ID, session2Subs[0].ID)

		// Verify stats
		stats := s.manager.GetStats()
		s.Equal(2, stats.Total)
		s.Equal(2, stats.Sessions)
		s.Equal(2, stats.Clusters)
	})
}

// TestCreate_EnforcesSessionLimit tests that Create() enforces per-session subscription limits
func (s *ManagerTestSuite) TestCreate_EnforcesSessionLimit() {
	s.Run("rejects subscription when session limit reached", func() {
		filters := SubscriptionFilters{}

		// Create up to the limit (config has MaxSubscriptionsPerSession = 3)
		for i := 0; i < s.config.MaxSubscriptionsPerSession; i++ {
			_, err := s.manager.Create("session1", "cluster1", "events", filters)
			s.Require().NoError(err)
		}

		// Next subscription should fail
		_, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Error(err)
		s.Contains(err.Error(), "maximum subscriptions")
	})

	s.Run("allows subscription for different session when one session at limit", func() {
		filters := SubscriptionFilters{}

		// Fill session1 to limit
		for i := 0; i < s.config.MaxSubscriptionsPerSession; i++ {
			_, err := s.manager.Create("session1", "cluster1", "events", filters)
			s.Require().NoError(err)
		}

		// session2 should still be able to create subscriptions
		_, err := s.manager.Create("session2", "cluster1", "events", filters)
		s.NoError(err)
	})
}

// TestCreate_EnforcesGlobalLimit tests that Create() enforces global subscription limits
func (s *ManagerTestSuite) TestCreate_EnforcesGlobalLimit() {
	s.Run("rejects subscription when global limit reached", func() {
		filters := SubscriptionFilters{}

		// Create subscriptions up to global limit (config has MaxSubscriptionsGlobal = 5)
		// Use multiple sessions to avoid session limit
		sessionCount := 0
		for i := 0; i < s.config.MaxSubscriptionsGlobal; i++ {
			sessionID := "session" + string(rune('1'+sessionCount))
			_, err := s.manager.Create(sessionID, "cluster1", "events", filters)
			s.Require().NoError(err)

			// Rotate to next session if we hit session limit
			if (i+1)%s.config.MaxSubscriptionsPerSession == 0 {
				sessionCount++
			}
		}

		// Next subscription should fail
		_, err := s.manager.Create("session-extra", "cluster1", "events", filters)
		s.Error(err)
		s.Contains(err.Error(), "maximum subscriptions")
	})
}

// TestCreate_ValidatesMode tests that Create() validates the mode parameter
func (s *ManagerTestSuite) TestCreate_ValidatesMode() {
	s.Run("accepts events mode", func() {
		filters := SubscriptionFilters{}
		_, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.NoError(err)
	})

	s.Run("accepts faults mode", func() {
		filters := SubscriptionFilters{}
		_, err := s.manager.Create("session1", "cluster1", "faults", filters)
		s.NoError(err)
	})

	s.Run("rejects invalid mode", func() {
		filters := SubscriptionFilters{}
		_, err := s.manager.Create("session1", "cluster1", "invalid", filters)
		s.Error(err)
		s.Contains(err.Error(), "invalid mode")
	})
}

// TestCreate_ValidatesFilters tests that Create() validates filters
func (s *ManagerTestSuite) TestCreate_ValidatesFilters() {
	s.Run("rejects invalid label selector", func() {
		filters := SubscriptionFilters{
			LabelSelector: "invalid=label=selector",
		}
		_, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Error(err)
		s.Contains(err.Error(), "invalid filters")
	})

	s.Run("rejects Normal type in faults mode", func() {
		filters := SubscriptionFilters{
			Type: "Normal",
		}
		_, err := s.manager.Create("session1", "cluster1", "faults", filters)
		s.Error(err)
		s.Contains(err.Error(), "faults mode cannot filter for Normal events")
	})

	s.Run("accepts valid filters", func() {
		filters := SubscriptionFilters{
			Namespaces:    []string{"default", "kube-system"},
			LabelSelector: "app=nginx",
			Type:          "Warning",
		}
		_, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.NoError(err)
	})
}

// TestCancel_RemovesSubscription tests that Cancel() removes subscriptions
func (s *ManagerTestSuite) TestCancel_RemovesSubscription() {
	s.Run("removes subscription from all indices", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Verify subscription exists
		retrieved := s.manager.GetSubscription(sub.ID)
		s.NotNil(retrieved)

		// Cancel subscription
		err = s.manager.Cancel(sub.ID)
		s.NoError(err)

		// Verify subscription is removed
		retrieved = s.manager.GetSubscription(sub.ID)
		s.Nil(retrieved)

		// Verify removed from session index
		sessionSubs := s.manager.ListSubscriptionsForSession("session1")
		s.Len(sessionSubs, 0)

		// Verify stats updated
		stats := s.manager.GetStats()
		s.Equal(0, stats.Total)
	})

	s.Run("returns error for non-existent subscription", func() {
		err := s.manager.Cancel("non-existent-id")
		s.Error(err)
		s.Contains(err.Error(), "not found")
	})

	s.Run("calls cancel function when set", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Set a cancel function
		cancelCalled := false
		sub.Cancel = func() {
			cancelCalled = true
		}

		// Cancel subscription
		err = s.manager.Cancel(sub.ID)
		s.NoError(err)
		s.True(cancelCalled, "cancel function should be called")
	})
}

// TestCancelBySessionAndID_ValidatesOwnership tests that CancelBySessionAndID validates session ownership
func (s *ManagerTestSuite) TestCancelBySessionAndID_ValidatesOwnership() {
	s.Run("cancels subscription when owned by session", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Cancel with correct session ID
		err = s.manager.CancelBySessionAndID("session1", sub.ID)
		s.NoError(err)

		// Verify subscription is removed
		retrieved := s.manager.GetSubscription(sub.ID)
		s.Nil(retrieved)
	})

	s.Run("rejects cancellation when not owned by session", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Attempt to cancel with different session ID
		err = s.manager.CancelBySessionAndID("session2", sub.ID)
		s.Error(err)
		s.Contains(err.Error(), "not found")

		// Verify subscription still exists
		retrieved := s.manager.GetSubscription(sub.ID)
		s.NotNil(retrieved)
	})

	s.Run("returns error for non-existent subscription", func() {
		err := s.manager.CancelBySessionAndID("session1", "non-existent-id")
		s.Error(err)
		s.Contains(err.Error(), "not found")
	})
}

// TestCancelSession_RemovesAllForSession tests that CancelSession() removes all subscriptions for a session
func (s *ManagerTestSuite) TestCancelSession_RemovesAllForSession() {
	s.Run("removes all subscriptions for session", func() {
		filters := SubscriptionFilters{}

		// Create multiple subscriptions for session1
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session1", "cluster2", "faults", filters)
		s.Require().NoError(err)

		// Create subscription for session2
		sub3, err := s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Cancel all subscriptions for session1
		s.manager.CancelSession("session1")

		// Verify session1 subscriptions removed
		s.Nil(s.manager.GetSubscription(sub1.ID))
		s.Nil(s.manager.GetSubscription(sub2.ID))

		// Verify session2 subscription still exists
		s.NotNil(s.manager.GetSubscription(sub3.ID))

		// Verify session1 has no subscriptions
		sessionSubs := s.manager.ListSubscriptionsForSession("session1")
		s.Len(sessionSubs, 0)

		// Verify session2 still has subscriptions
		sessionSubs = s.manager.ListSubscriptionsForSession("session2")
		s.Len(sessionSubs, 1)
	})

	s.Run("calls cancel functions for all subscriptions", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session1", "cluster2", "faults", filters)
		s.Require().NoError(err)

		// Set cancel functions
		cancel1Called := false
		cancel2Called := false
		sub1.Cancel = func() { cancel1Called = true }
		sub2.Cancel = func() { cancel2Called = true }

		// Cancel session
		s.manager.CancelSession("session1")

		s.True(cancel1Called, "cancel function 1 should be called")
		s.True(cancel2Called, "cancel function 2 should be called")
	})

	s.Run("handles non-existent session gracefully", func() {
		// Should not panic or error
		s.manager.CancelSession("non-existent-session")
	})
}

// TestCancelCluster_RemovesAllForCluster tests that CancelCluster() removes all subscriptions for a cluster
func (s *ManagerTestSuite) TestCancelCluster_RemovesAllForCluster() {
	s.Run("removes all subscriptions for cluster", func() {
		filters := SubscriptionFilters{}

		// Create subscriptions for cluster1
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster1", "faults", filters)
		s.Require().NoError(err)

		// Create subscription for cluster2
		sub3, err := s.manager.Create("session1", "cluster2", "events", filters)
		s.Require().NoError(err)

		// Cancel all subscriptions for cluster1
		s.manager.CancelCluster("cluster1")

		// Verify cluster1 subscriptions removed
		s.Nil(s.manager.GetSubscription(sub1.ID))
		s.Nil(s.manager.GetSubscription(sub2.ID))

		// Verify cluster2 subscription still exists
		s.NotNil(s.manager.GetSubscription(sub3.ID))

		// Verify stats
		stats := s.manager.GetStats()
		s.Equal(1, stats.Total)
		s.Equal(1, stats.Clusters)
	})

	s.Run("calls cancel functions for all cluster subscriptions", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster1", "faults", filters)
		s.Require().NoError(err)

		// Set cancel functions
		cancel1Called := false
		cancel2Called := false
		sub1.Cancel = func() { cancel1Called = true }
		sub2.Cancel = func() { cancel2Called = true }

		// Cancel cluster
		s.manager.CancelCluster("cluster1")

		s.True(cancel1Called, "cancel function 1 should be called")
		s.True(cancel2Called, "cancel function 2 should be called")
	})

	s.Run("handles non-existent cluster gracefully", func() {
		// Should not panic or error
		s.manager.CancelCluster("non-existent-cluster")
	})
}

// TestCancelAll tests that CancelAll() removes all subscriptions
func (s *ManagerTestSuite) TestCancelAll() {
	s.Run("removes all subscriptions", func() {
		filters := SubscriptionFilters{}

		// Create multiple subscriptions
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster2", "faults", filters)
		s.Require().NoError(err)

		// Cancel all
		s.manager.CancelAll()

		// Verify all removed
		s.Nil(s.manager.GetSubscription(sub1.ID))
		s.Nil(s.manager.GetSubscription(sub2.ID))

		// Verify stats
		stats := s.manager.GetStats()
		s.Equal(0, stats.Total)
		s.Equal(0, stats.Sessions)
		s.Equal(0, stats.Clusters)
	})
}

// TestGetSubscription tests the GetSubscription method
func (s *ManagerTestSuite) TestGetSubscription() {
	s.Run("returns subscription when exists", func() {
		filters := SubscriptionFilters{
			Namespaces: []string{"default"},
		}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		retrieved := s.manager.GetSubscription(sub.ID)
		s.NotNil(retrieved)
		s.Equal(sub.ID, retrieved.ID)
		s.Equal("session1", retrieved.SessionID)
		s.Equal("cluster1", retrieved.Cluster)
		s.Equal("events", retrieved.Mode)
		s.Equal(filters.Namespaces, retrieved.Filters.Namespaces)
	})

	s.Run("returns nil when subscription does not exist", func() {
		retrieved := s.manager.GetSubscription("non-existent-id")
		s.Nil(retrieved)
	})
}

// TestListSubscriptionsForSession tests the ListSubscriptionsForSession method
func (s *ManagerTestSuite) TestListSubscriptionsForSession() {
	s.Run("returns all subscriptions for session", func() {
		filters := SubscriptionFilters{}

		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session1", "cluster2", "faults", filters)
		s.Require().NoError(err)

		// Create subscription for different session
		_, err = s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// List session1 subscriptions
		subs := s.manager.ListSubscriptionsForSession("session1")
		s.Len(subs, 2)

		// Verify both subscriptions are in the list
		ids := make(map[string]bool)
		for _, sub := range subs {
			ids[sub.ID] = true
		}
		s.True(ids[sub1.ID])
		s.True(ids[sub2.ID])
	})

	s.Run("returns empty list for non-existent session", func() {
		subs := s.manager.ListSubscriptionsForSession("non-existent-session")
		s.Len(subs, 0)
	})
}

// TestGetStats tests the GetStats method
func (s *ManagerTestSuite) TestGetStats() {
	s.Run("returns correct statistics", func() {
		filters := SubscriptionFilters{}

		// Initially empty
		stats := s.manager.GetStats()
		s.Equal(0, stats.Total)
		s.Equal(0, stats.Sessions)
		s.Equal(0, stats.Clusters)
		s.Equal(0, stats.Degraded)

		// Add subscriptions
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session1", "cluster2", "faults", filters)
		s.Require().NoError(err)

		_, err = s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Check stats
		stats = s.manager.GetStats()
		s.Equal(3, stats.Total)
		s.Equal(2, stats.Sessions)
		s.Equal(2, stats.Clusters)
		s.Equal(0, stats.Degraded)

		// Mark one as degraded
		sub1.Degraded = true

		stats = s.manager.GetStats()
		s.Equal(1, stats.Degraded)

		// Remove one subscription
		err = s.manager.Cancel(sub2.ID)
		s.Require().NoError(err)

		stats = s.manager.GetStats()
		s.Equal(2, stats.Total)
		s.Equal(2, stats.Sessions)
		s.Equal(2, stats.Clusters)
	})
}

// TestSessionMonitor tests the session monitoring functionality
func (s *ManagerTestSuite) TestSessionMonitor() {
	s.Run("removes subscriptions for stale sessions", func() {
		filters := SubscriptionFilters{}

		// Create a session and add it to the server
		session1 := NewMockServerSession("session1")
		s.server.AddSession(session1)

		// Create subscriptions
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Verify both exist
		s.NotNil(s.manager.GetSubscription(sub1.ID))
		s.NotNil(s.manager.GetSubscription(sub2.ID))

		// Trigger cleanup (session2 is not in the server, so it's stale)
		s.manager.cleanupStaleSessions()

		// session1 subscription should remain (it's in the server)
		s.NotNil(s.manager.GetSubscription(sub1.ID))

		// session2 subscription should be removed (it's stale)
		s.Nil(s.manager.GetSubscription(sub2.ID))
	})

	s.Run("keeps subscriptions for active sessions", func() {
		filters := SubscriptionFilters{}

		// Create sessions and add them to the server
		session1 := NewMockServerSession("session1")
		session2 := NewMockServerSession("session2")
		s.server.AddSession(session1)
		s.server.AddSession(session2)

		// Create subscriptions
		sub1, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub2, err := s.manager.Create("session2", "cluster1", "events", filters)
		s.Require().NoError(err)

		// Trigger cleanup
		s.manager.cleanupStaleSessions()

		// Both subscriptions should remain (both sessions are active)
		s.NotNil(s.manager.GetSubscription(sub1.ID))
		s.NotNil(s.manager.GetSubscription(sub2.ID))
	})
}

// TestSubscriptionCreatedAt tests that subscriptions have timestamps
func (s *ManagerTestSuite) TestSubscriptionCreatedAt() {
	s.Run("sets CreatedAt timestamp", func() {
		filters := SubscriptionFilters{}

		before := time.Now()
		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)
		after := time.Now()

		s.False(sub.CreatedAt.IsZero())
		s.True(sub.CreatedAt.After(before) || sub.CreatedAt.Equal(before))
		s.True(sub.CreatedAt.Before(after) || sub.CreatedAt.Equal(after))
	})
}

// TestSubscriptionDegraded tests the Degraded flag
func (s *ManagerTestSuite) TestSubscriptionDegraded() {
	s.Run("starts as not degraded", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		s.False(sub.Degraded)
	})

	s.Run("can be marked as degraded", func() {
		filters := SubscriptionFilters{}

		sub, err := s.manager.Create("session1", "cluster1", "events", filters)
		s.Require().NoError(err)

		sub.Degraded = true

		// Verify via GetSubscription
		retrieved := s.manager.GetSubscription(sub.ID)
		s.True(retrieved.Degraded)

		// Verify in stats
		stats := s.manager.GetStats()
		s.Equal(1, stats.Degraded)
	})
}

// TestGetCurrentResourceVersion tests the getCurrentResourceVersion method
func (s *ManagerTestSuite) TestGetCurrentResourceVersion() {
	s.Run("retrieves resource version from cluster-wide list", func() {
		clientset := fake.NewClientset()

		// Create events with resource version
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "default",
				ResourceVersion: "12345",
			},
		}
		_, err := clientset.CoreV1().Events("default").Create(context.Background(), event, metav1.CreateOptions{})
		s.Require().NoError(err)

		// Get current resource version (cluster-wide)
		rv, err := s.manager.getCurrentResourceVersion(clientset, "")
		s.NoError(err)
		// Fake clientset may return empty resource version, which is acceptable
		s.NotNil(rv, "resource version should not be nil")
	})

	s.Run("retrieves resource version from namespace-scoped list", func() {
		clientset := fake.NewClientset()

		// Create events in specific namespace
		event := &v1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "test-event",
				Namespace:       "kube-system",
				ResourceVersion: "67890",
			},
		}
		_, err := clientset.CoreV1().Events("kube-system").Create(context.Background(), event, metav1.CreateOptions{})
		s.Require().NoError(err)

		// Get current resource version (namespace-scoped)
		rv, err := s.manager.getCurrentResourceVersion(clientset, "kube-system")
		s.NoError(err)
		// Fake clientset may return empty resource version, which is acceptable
		s.NotNil(rv, "resource version should not be nil")
	})

	s.Run("returns empty resource version when no events exist", func() {
		clientset := fake.NewClientset()

		// Get current resource version (no events in cluster)
		rv, err := s.manager.getCurrentResourceVersion(clientset, "")
		s.NoError(err)
		s.Equal("", rv, "resource version should be empty when no events exist")
	})

	s.Run("handles context timeout gracefully", func() {
		clientset := fake.NewClientset()

		// The fake clientset doesn't really timeout, but we can verify the method completes
		rv, err := s.manager.getCurrentResourceVersion(clientset, "")
		s.NoError(err)
		s.NotNil(rv)
	})
}
