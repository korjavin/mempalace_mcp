package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

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

	// Debug endpoints — auth via cookie or query token
	mux.Handle("GET /debug/status", authSvc.RequireAuthOrToken(http.HandlerFunc(h.debugStatusHandler)))

	// MCP SSE endpoints — auth via cookie or query token
	mux.Handle("GET /sse", authSvc.RequireAuthOrToken(http.HandlerFunc(h.proxy.HandleSSE)))
	mux.Handle("POST /message", authSvc.RequireAuthOrToken(http.HandlerFunc(h.proxy.HandleMessage)))

	return logMiddleware(mux)
}

func (h *Handler) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) debugStatusHandler(w http.ResponseWriter, r *http.Request) {
	resp, err := h.proxy.StatusRequest(r.Context())
	if err != nil {
		if strings.Contains(err.Error(), "timed out") {
			http.Error(w, `{"error":"status request timed out"}`, http.StatusGatewayTimeout)
			return
		}
		slog.Error("debug status request failed", "error", err)
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
