package events

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

// GenerateFaultID creates a deterministic, stable identifier for a fault condition.
// The Fault ID is generated from a hash of the fault's defining characteristics:
// - cluster: The cluster where the fault occurred
// - faultType: The type of fault detected
// - resourceUID: The unique identifier of the affected resource
// - containerName: The container name (for pod-level faults, empty otherwise)
//
// The same fault condition always produces the same Fault ID, enabling downstream
// systems to recognize re-emissions of the same fault for deduplication purposes.
//
// Returns a 16-character hex string (64 bits of SHA-256).
func GenerateFaultID(cluster string, faultType FaultType, resourceUID types.UID, containerName string) string {
	// Build the input string: cluster:faultType:resourceUID:containerName
	input := fmt.Sprintf("%s:%s:%s:%s", cluster, faultType, resourceUID, containerName)

	// Compute SHA-256 hash
	hash := sha256.Sum256([]byte(input))

	// Take first 8 bytes (64 bits) and convert to hex
	return hex.EncodeToString(hash[:8])
}
