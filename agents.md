# Project Guide for AI Assistants

## What This Is

An authenticated MCP-over-SSE proxy for mempalace. Go handles HTTP/auth, mempalace (Python) handles memory operations.

## Structure

- `cmd/server/main.go` — Entry point. Runs palace auto-init, starts auth, proxy, HTTP server.
- `internal/auth/google.go` — Google OIDC login, session cookies, middleware.
- `internal/palace/init.go` — Auto-initialization: creates `~/.mempalace/config.json` and palace data directory if missing, so the MCP server works without manual `mempalace init`.
- `internal/proxy/proxy.go` — Spawns `python -m mempalace.mcp_server`, proxies JSON-RPC between SSE clients and subprocess stdin/stdout. Includes `StatusRequest()` for debug queries and `IsAlive()` for health monitoring.
- `internal/api/handler.go` — HTTP routing: `/health`, `/auth/*`, `/sse`, `/message`, `/debug/status`.

## How It Works

1. Client connects to `GET /sse` (must be authenticated) — gets an SSE stream and a message endpoint URL
2. Client sends JSON-RPC via `POST /message?session_id=...`
3. Go proxy writes the request to mempalace's stdin
4. Mempalace responds on stdout, proxy routes it back to the correct SSE session using JSON-RPC `id` matching

## Adding Features

- Auth changes: `internal/auth/google.go`
- New HTTP routes: `internal/api/handler.go`
- Proxy behavior: `internal/proxy/proxy.go`
- Mempalace itself is a pip dependency — not in this repo
