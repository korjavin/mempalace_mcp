package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/korjavin/mempalace_mcp/internal/auth"
	"github.com/korjavin/mempalace_mcp/internal/proxy"
)

type Handler struct {
	auth  *auth.Service
	proxy *proxy.MCPProxy
}

func NewHandler(authSvc *auth.Service, mcpProxy *proxy.MCPProxy) http.Handler {
	h := &Handler{
		auth:  authSvc,
		proxy: mcpProxy,
	}

	mux := http.NewServeMux()

	// Health check — no auth
	mux.HandleFunc("GET /health", h.healthHandler)

	// Auth endpoints
	mux.HandleFunc("GET /auth/login", authSvc.LoginHandler)
	mux.HandleFunc("GET /auth/callback", authSvc.CallbackHandler)
	mux.HandleFunc("POST /auth/logout", authSvc.LogoutHandler)

	// MCP SSE endpoints — auth via cookie or query token
	mux.Handle("GET /sse", authSvc.RequireAuthOrToken(http.HandlerFunc(h.proxy.HandleSSE)))
	mux.Handle("POST /message", authSvc.RequireAuthOrToken(http.HandlerFunc(h.proxy.HandleMessage)))

	return logMiddleware(mux)
}

func (h *Handler) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
