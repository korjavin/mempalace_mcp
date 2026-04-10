# mempalace Auto-Init and Debug Status

## Overview
- Make mempalace fully functional after Docker container start — no manual init required
- Add `/debug/status` endpoint that calls `mempalace_status` via MCP protocol to verify full stack health
- Detect whether palace is initialized, create minimal config if not, so the MCP server can accept writes immediately
- Ensure the palace data persists across container restarts via Docker volume

## Context (from discovery)
- **MCP server** (`mempalace.mcp_server`) does NOT auto-init — returns `{"error": "No palace found"}` if collection missing
- **But** `tool_add_drawer` creates the ChromaDB collection on demand (`_get_collection(create=True)`)
- **`mempalace init`** is interactive (prompts user to approve rooms) — not suitable for headless Docker
- **Config**: `MempalaceConfig().init()` creates `~/.mempalace/config.json` + directory
- **Palace detection**: Check for `~/.mempalace/config.json` existence and ChromaDB data in `PALACE_PATH`
- **Proxy** (`internal/proxy/proxy.go`): Spawns subprocess, multiplexes SSE sessions, routes by JSON-RPC id
- **Handler** (`internal/api/handler.go`): Routes `/sse`, `/message`, `/health`, `/auth/*`

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**

## Testing Strategy
- **Unit tests**: Required for every task
- Mock the mempalace subprocess for proxy tests (fake stdin/stdout)
- Test `/debug/status` with mock MCP responses
- Test auto-init logic with temp directories

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

### Task 1: Add palace auto-initialization on startup
- [x] Create `internal/palace/init.go` with `EnsureInitialized(palacePath string) error`
- [x] Check if `~/.mempalace/config.json` exists; if not, create it programmatically (JSON with `palace_path`, `collection_name: "mempalace_drawers"`)
- [x] Create `PALACE_PATH` directory if missing
- [x] Call `EnsureInitialized` from `cmd/server/main.go` before starting the MCP proxy subprocess
- [x] Write tests for `EnsureInitialized` — palace dir created, config.json written correctly
- [x] Write tests for idempotency — calling twice doesn't corrupt existing config
- [x] Run tests — must pass before next task

### Task 2: Add `/debug/status` endpoint via MCP protocol
- [x] Add `StatusRequest(ctx context.Context) ([]byte, error)` method to `MCPProxy` that sends a JSON-RPC `tools/call` for `mempalace_status` through the subprocess stdin and waits for the response
- [x] Use a dedicated internal session (not tied to any SSE client) with a reserved JSON-RPC id prefix for debug requests
- [x] Add `GET /debug/status` route in `handler.go` — authenticated via `RequireAuthOrToken`
- [x] Handler calls `proxy.StatusRequest()`, returns the MCP response as JSON
- [x] Handle timeout (5s) — return 504 if subprocess doesn't respond
- [x] Write tests for `StatusRequest` with mock subprocess (write expected JSON-RPC response to stdout pipe)
- [x] Write tests for `/debug/status` handler (success, timeout, auth required)
- [x] Run tests — must pass before next task

### Task 3: Improve proxy subprocess lifecycle
- [x] Add health monitoring: detect if mempalace subprocess exits unexpectedly, log error with exit code
- [x] Add `IsAlive() bool` method to `MCPProxy` that checks if the subprocess is still running
- [x] Include subprocess alive status in `/debug/status` response alongside mempalace data
- [x] Write tests for `IsAlive` (running vs exited process)
- [x] Run tests — must pass before next task

### Task 4: Update Dockerfile for proper palace setup
- [ ] Set `HOME=/app` in Dockerfile so `~/.mempalace/` resolves inside the container
- [ ] Ensure `/data/palace` volume mount point is created
- [ ] Add `PALACE_PATH` and `HOME` to docker-compose.yml environment
- [ ] Verify `.env.example` has all new variables documented
- [ ] Write a simple integration test script (`scripts/test-docker.sh`) that builds the image and verifies `/health` responds
- [ ] Run tests — must pass before next task

### Task 5: Verify acceptance criteria
- [ ] Verify: container starts with empty volume → palace auto-inits → MCP server starts → `/debug/status` returns data
- [ ] Verify: container restarts with existing volume → palace detected → no re-init → everything works
- [ ] Verify: `/debug/status` returns proper error if subprocess is unhealthy
- [ ] Run full test suite (unit tests)
- [ ] Run linter (`go vet ./...`) — all issues must be fixed

### Task 6: [Final] Update documentation
- [ ] Update README.md with `/debug/status` endpoint docs
- [ ] Update agents.md with new `internal/palace/` package description
- [ ] Update .env.example if any new env vars added

## Technical Details

### Auto-init config.json format
```json
{
  "palace_path": "/data/palace",
  "collection_name": "mempalace_drawers"
}
```
Written to `$HOME/.mempalace/config.json` (inside container: `/app/.mempalace/config.json`).

### `/debug/status` MCP request
```json
{"jsonrpc": "2.0", "id": "debug-status-<uuid>", "method": "tools/call", "params": {"name": "mempalace_status", "arguments": {}}}
```

### `/debug/status` response format
```json
{
  "alive": true,
  "mempalace": { /* raw response from mempalace_status tool */ }
}
```

## Post-Completion

**Manual verification:**
- Deploy to staging, verify `/debug/status` works through Traefik
- Connect a real MCP client (Claude) and verify search/add operations work
- Test volume persistence: `docker compose down && docker compose up` — data preserved
