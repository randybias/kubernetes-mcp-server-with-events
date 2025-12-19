package events

import (
	"context"
	"iter"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPServerAdapter adapts a real mcp.Server to the MCPServer interface.
type MCPServerAdapter struct {
	server *mcp.Server
}

// NewMCPServerAdapter creates a new adapter for the given mcp.Server.
func NewMCPServerAdapter(server *mcp.Server) *MCPServerAdapter {
	return &MCPServerAdapter{server: server}
}

// Sessions returns an iterator over active server sessions.
func (a *MCPServerAdapter) Sessions() SessionIterator {
	return &sessionIteratorAdapter{seq: a.server.Sessions()}
}

// sessionIteratorAdapter adapts iter.Seq[*mcp.ServerSession] to SessionIterator.
type sessionIteratorAdapter struct {
	seq iter.Seq[*mcp.ServerSession]
}

// All calls the yield function for each session in the iterator.
func (s *sessionIteratorAdapter) All(yield func(ServerSession) bool) {
	for session := range s.seq {
		if !yield(&serverSessionAdapter{session: session}) {
			return
		}
	}
}

// serverSessionAdapter adapts mcp.ServerSession to ServerSession interface.
type serverSessionAdapter struct {
	session *mcp.ServerSession
}

// ID returns the session ID.
func (s *serverSessionAdapter) ID() string {
	return s.session.ID()
}

// Log sends a log message to the session.
func (s *serverSessionAdapter) Log(ctx context.Context, params *mcp.LoggingMessageParams) error {
	return s.session.Log(ctx, params)
}
