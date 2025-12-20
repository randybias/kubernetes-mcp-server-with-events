package events

import (
	"strings"
)

const (
	// DefaultMaxLogBytesPerContainer is the default maximum bytes to capture per container
	DefaultMaxLogBytesPerContainer = 10240 // 10KB

	// DefaultMaxContainersPerNotification is the default maximum containers to capture logs from
	DefaultMaxContainersPerNotification = 5
)

// ContainerLog represents logs captured from a single container
type ContainerLog struct {
	Container string `json:"container"`
	Previous  bool   `json:"previous"`
	HasPanic  bool   `json:"hasPanic"`
	Sample    string `json:"sample"`
	Error     string `json:"error,omitempty"`
}

// truncateLog truncates log content to the specified byte limit
func truncateLog(log string, maxBytes int) string {
	if len(log) <= maxBytes {
		return log
	}
	return log[:maxBytes]
}

// detectPanic scans log content for panic indicators
// Returns true if any panic/fatal/crash patterns are detected
func detectPanic(log string) bool {
	// Keywords to detect panics and fatal errors (lowercase for case-insensitive matching)
	keywords := []string{
		"panic:",
		"fatal",
		"sigsegv",
		"segfault",
		"goroutine",
	}

	logLower := strings.ToLower(log)
	for _, keyword := range keywords {
		if strings.Contains(logLower, keyword) {
			return true
		}
	}

	return false
}
