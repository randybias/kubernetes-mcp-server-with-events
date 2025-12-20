package events

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/types"
)

type FaultIDTestSuite struct {
	suite.Suite
}

func TestFaultIDSuite(t *testing.T) {
	suite.Run(t, new(FaultIDTestSuite))
}

// Test that the same inputs produce the same FaultID (determinism)
func (s *FaultIDTestSuite) TestGenerateFaultID_Determinism() {
	s.Run("same inputs produce identical FaultID", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("abc123-def456")
		containerName := "app-container"

		id1 := GenerateFaultID(cluster, faultType, resourceUID, containerName)
		id2 := GenerateFaultID(cluster, faultType, resourceUID, containerName)

		s.Equal(id1, id2, "identical inputs should produce identical FaultID")
		s.Len(id1, 16, "FaultID should be 16 hex characters")
	})
}

// Test that different inputs produce different FaultIDs (uniqueness)
func (s *FaultIDTestSuite) TestGenerateFaultID_Uniqueness() {
	s.Run("different clusters produce different FaultIDs", func() {
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("abc123-def456")
		containerName := "app-container"

		id1 := GenerateFaultID("cluster-1", faultType, resourceUID, containerName)
		id2 := GenerateFaultID("cluster-2", faultType, resourceUID, containerName)

		s.NotEqual(id1, id2, "different clusters should produce different FaultIDs")
	})

	s.Run("different fault types produce different FaultIDs", func() {
		cluster := "prod-cluster"
		resourceUID := types.UID("abc123-def456")
		containerName := "app-container"

		id1 := GenerateFaultID(cluster, FaultTypeCrashLoop, resourceUID, containerName)
		id2 := GenerateFaultID(cluster, FaultTypePodCrash, resourceUID, containerName)

		s.NotEqual(id1, id2, "different fault types should produce different FaultIDs")
	})

	s.Run("different resource UIDs produce different FaultIDs", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeCrashLoop
		containerName := "app-container"

		id1 := GenerateFaultID(cluster, faultType, types.UID("resource-1"), containerName)
		id2 := GenerateFaultID(cluster, faultType, types.UID("resource-2"), containerName)

		s.NotEqual(id1, id2, "different resource UIDs should produce different FaultIDs")
	})

	s.Run("different container names produce different FaultIDs", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("abc123-def456")

		id1 := GenerateFaultID(cluster, faultType, resourceUID, "container-1")
		id2 := GenerateFaultID(cluster, faultType, resourceUID, "container-2")

		s.NotEqual(id1, id2, "different container names should produce different FaultIDs")
	})
}

// Test format and length constraints
func (s *FaultIDTestSuite) TestGenerateFaultID_Format() {
	s.Run("FaultID has correct format", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("abc123-def456")
		containerName := "app-container"

		id := GenerateFaultID(cluster, faultType, resourceUID, containerName)

		s.Len(id, 16, "FaultID should be exactly 16 characters")
		s.Regexp("^[0-9a-f]{16}$", id, "FaultID should be lowercase hex characters")
	})
}

// Test edge cases
func (s *FaultIDTestSuite) TestGenerateFaultID_EdgeCases() {
	s.Run("empty container name produces valid FaultID", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeNodeUnhealthy
		resourceUID := types.UID("node-123")
		containerName := ""

		id := GenerateFaultID(cluster, faultType, resourceUID, containerName)

		s.Len(id, 16, "FaultID should be 16 hex characters even with empty container name")
		s.Regexp("^[0-9a-f]{16}$", id, "FaultID should be valid hex")
	})

	s.Run("empty container name vs non-empty produce different FaultIDs", func() {
		cluster := "prod-cluster"
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("abc123")

		id1 := GenerateFaultID(cluster, faultType, resourceUID, "")
		id2 := GenerateFaultID(cluster, faultType, resourceUID, "container")

		s.NotEqual(id1, id2, "empty container name should produce different FaultID than non-empty")
	})
}

// Test collision resistance
func (s *FaultIDTestSuite) TestGenerateFaultID_CollisionResistance() {
	s.Run("many unique inputs produce many unique FaultIDs", func() {
		seen := make(map[string]bool)
		collisions := 0

		// Generate 10000 unique fault IDs
		for i := 0; i < 10000; i++ {
			cluster := "cluster"
			faultType := FaultTypeCrashLoop
			resourceUID := types.UID(string(rune(i)))
			containerName := "container"

			id := GenerateFaultID(cluster, faultType, resourceUID, containerName)

			if seen[id] {
				collisions++
			}
			seen[id] = true
		}

		s.Equal(0, collisions, "no collisions should occur in 10000 unique inputs")
		s.Len(seen, 10000, "all FaultIDs should be unique")
	})
}

// Test multi-cluster scenarios
func (s *FaultIDTestSuite) TestGenerateFaultID_MultiCluster() {
	s.Run("same resource in different clusters produces different FaultIDs", func() {
		faultType := FaultTypeCrashLoop
		resourceUID := types.UID("same-resource-uid")
		containerName := "app"

		idCluster1 := GenerateFaultID("cluster-us-east", faultType, resourceUID, containerName)
		idCluster2 := GenerateFaultID("cluster-us-west", faultType, resourceUID, containerName)

		s.NotEqual(idCluster1, idCluster2,
			"same resource UID in different clusters should produce different FaultIDs")
	})
}
