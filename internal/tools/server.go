// Package tools gives each agent session its own MCP endpoint (/mcp/<token>) whose WhatsApp tools
// are bound to that session's chat.
package tools

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerName is the MCP server name sessions see; tools appear as mcp__whatsapp__<tool>.
const ServerName = "whatsapp"

// Host serves the MCP endpoints of all live sessions.
type Host struct {
	mu      sync.RWMutex
	servers map[string]*mcp.Server
	handler http.Handler
}

func NewHost(log *slog.Logger) *Host {
	h := &Host{servers: map[string]*mcp.Server{}}
	h.handler = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return h.servers[strings.TrimPrefix(r.URL.Path, "/mcp/")]
	}, &mcp.StreamableHTTPOptions{SessionTimeout: 6 * time.Hour, Logger: log})
	return h
}

func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.handler.ServeHTTP(w, r) }

// Register creates the endpoint for a session and returns its URL path ("/mcp/<token>").
func (h *Host) Register(b *Binding) string {
	token := randomToken()
	srv := newServer(b)
	h.mu.Lock()
	h.servers[token] = srv
	h.mu.Unlock()
	return "/mcp/" + token
}

// Unregister removes a session endpoint.
func (h *Host) Unregister(path string) {
	h.mu.Lock()
	delete(h.servers, strings.TrimPrefix(path, "/mcp/"))
	h.mu.Unlock()
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
