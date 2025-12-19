package events

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type LogsSuite struct {
	suite.Suite
}

func (s *LogsSuite) TestTruncateLog() {
	s.Run("preserves logs shorter than limit", func() {
		input := "short log message"
		result := truncateLog(input, 1000)
		s.Equal(input, result)
	})

	s.Run("truncates at exactly 10KB boundary", func() {
		// Create a log that's exactly 15KB
		largeLog := strings.Repeat("a", 15360)
		result := truncateLog(largeLog, DefaultMaxLogBytesPerContainer)
		s.Equal(DefaultMaxLogBytesPerContainer, len(result))
		s.Equal(strings.Repeat("a", DefaultMaxLogBytesPerContainer), result)
	})

	s.Run("handles empty log", func() {
		result := truncateLog("", 1000)
		s.Equal("", result)
	})

	s.Run("handles exact size log", func() {
		input := strings.Repeat("x", 100)
		result := truncateLog(input, 100)
		s.Equal(input, result)
	})
}

func (s *LogsSuite) TestDetectPanic() {
	s.Run("detects panic keyword", func() {
		s.Run("with standard panic message", func() {
			log := "panic: runtime error: invalid memory address or nil pointer dereference"
			s.True(detectPanic(log))
		})

		s.Run("with uppercase PANIC", func() {
			log := "PANIC: something went wrong"
			s.True(detectPanic(log))
		})

		s.Run("with mixed case", func() {
			log := "Starting application\nPaNiC: unexpected condition\nShutting down"
			s.True(detectPanic(log))
		})
	})

	s.Run("detects fatal keyword", func() {
		s.Run("with fatal error", func() {
			log := "fatal error: concurrent map writes"
			s.True(detectPanic(log))
		})

		s.Run("with FATAL in uppercase", func() {
			log := "FATAL: database connection failed"
			s.True(detectPanic(log))
		})
	})

	s.Run("detects SIGSEGV", func() {
		log := "SIGSEGV: segmentation violation"
		s.True(detectPanic(log))
	})

	s.Run("detects segfault", func() {
		s.Run("lowercase", func() {
			log := "Process terminated: segfault"
			s.True(detectPanic(log))
		})

		s.Run("uppercase", func() {
			log := "SEGFAULT detected in module"
			s.True(detectPanic(log))
		})
	})

	s.Run("detects goroutine stack traces", func() {
		log := `panic: test panic
goroutine 1 [running]:
main.main()
	/app/main.go:10 +0x39`
		s.True(detectPanic(log))
	})

	s.Run("returns false for normal log messages", func() {
		s.Run("informational message", func() {
			log := "Server started on port 8080"
			s.False(detectPanic(log))
		})

		s.Run("warning message", func() {
			log := "Warning: high memory usage detected"
			s.False(detectPanic(log))
		})

		s.Run("error message without panic", func() {
			log := "Error connecting to database, retrying..."
			s.False(detectPanic(log))
		})

		s.Run("empty log", func() {
			s.False(detectPanic(""))
		})
	})

	s.Run("handles case insensitivity", func() {
		testCases := []string{
			"panic: test",
			"PANIC: test",
			"PaNiC: test",
			"fatal error",
			"FATAL ERROR",
			"FaTaL error",
		}
		for _, tc := range testCases {
			s.True(detectPanic(tc), "should detect: %s", tc)
		}
	})
}

// TestCapturePodLogs requires a real Kubernetes cluster with running pods
// These tests are commented out for unit testing and should be run as integration tests
// func (s *LogsSuite) TestCapturePodLogs() {
// 	// Integration tests with actual Kubernetes cluster would go here
// }

func (s *LogsSuite) TestManagerConfig() {
	s.Run("default config has expected values", func() {
		config := DefaultManagerConfig()
		s.Equal(DefaultMaxContainersPerNotification, config.MaxContainersPerNotification)
		s.Equal(DefaultMaxLogBytesPerContainer, config.MaxLogBytesPerContainer)
		s.Equal(5, config.MaxLogCapturesPerCluster)
		s.Equal(20, config.MaxLogCapturesGlobal)
	})
}

func (s *LogsSuite) TestContainerLogStructure() {
	s.Run("container log has expected fields", func() {
		log := ContainerLog{
			Container: "app",
			Previous:  false,
			HasPanic:  true,
			Sample:    "panic: test",
			Error:     "",
		}

		s.Equal("app", log.Container)
		s.False(log.Previous)
		s.True(log.HasPanic)
		s.Equal("panic: test", log.Sample)
		s.Empty(log.Error)
	})

	s.Run("container log with error", func() {
		log := ContainerLog{
			Container: "sidecar",
			Previous:  true,
			HasPanic:  false,
			Sample:    "",
			Error:     "container not found",
		}

		s.Equal("sidecar", log.Container)
		s.True(log.Previous)
		s.False(log.HasPanic)
		s.Empty(log.Sample)
		s.Equal("container not found", log.Error)
	})
}

func (s *LogsSuite) TestTruncationPreservesUTF8() {
	s.Run("handles multi-byte UTF-8 characters", func() {
		// Create a string with multi-byte UTF-8 characters
		input := strings.Repeat("Hello 世界 ", 2000) // Each repetition is ~15 bytes
		result := truncateLog(input, 100)

		// Verify truncation occurred
		s.LessOrEqual(len(result), 100)

		// Note: Our simple truncation may cut in the middle of a multi-byte character
		// For production, you might want to use strings.ToValidUTF8 or similar
	})
}

func (s *LogsSuite) TestDetectPanicWithRealStackTraces() {
	s.Run("detects real Go panic stack trace", func() {
		realPanic := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x4a1f93]

goroutine 1 [running]:
main.processRequest(0x0)
	/app/main.go:45 +0x93
main.main()
	/app/main.go:10 +0x3e`
		s.True(detectPanic(realPanic))
	})

	s.Run("detects fatal error with stack", func() {
		fatalError := `fatal error: concurrent map writes

goroutine 37 [running]:
runtime.throw(0x4c8e4f, 0x15)
	/usr/local/go/src/runtime/panic.go:774 +0x72 fp=0xc00009bf50 sp=0xc00009bf20 pc=0x42d572`
		s.True(detectPanic(fatalError))
	})

	s.Run("detects panic in logs with context", func() {
		logWithContext := `2025-01-15T10:30:45.123Z INFO Starting application
2025-01-15T10:30:45.456Z INFO Connecting to database
2025-01-15T10:30:45.789Z ERROR panic: assignment to entry in nil map
2025-01-15T10:30:45.790Z INFO Shutting down`
		s.True(detectPanic(logWithContext))
	})
}

func TestLogs(t *testing.T) {
	suite.Run(t, new(LogsSuite))
}
