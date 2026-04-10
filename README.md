# mempalace_mcp

Web-accessible MCP server that proxies [mempalace](https://github.com/milla-jovovich/mempalace/) over SSE with Google OIDC authentication. Allows external tools like Claude and ChatGPT to access your mempalace remotely.

## Architecture

```
Client (Claude/ChatGPT) --SSE--> Go server (auth + proxy) --stdio--> python -m mempalace.mcp_server
```

- **Go server**: HTTP/SSE transport, Google OIDC auth, session management
- **mempalace subprocess**: Runs as stdio JSON-RPC, manages the palace (ChromaDB + SQLite)
- **Docker**: Single container with Python + Go binary, palace data on a volume

## Setup

1. Copy `.env.example` to `.env` and fill in values
2. Set up Google OAuth2 credentials at https://console.cloud.google.com/apis/credentials
3. Set `ADMIN_EMAILS` to your Google email(s) to restrict access

## Environment Variables

| Variable | Description |
|---|---|
| `PORT` | Server port (default: 8080) |
| `BASE_URL` | Public URL of this service |
| `GOOGLE_CLIENT_ID` | Google OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 client secret |
| `GOOGLE_REDIRECT_URL` | OAuth2 callback URL |
| `ADMIN_EMAILS` | Comma-separated allowed emails |
| `COOKIE_SECRET` | Random 32+ byte string for session signing |
| `PALACE_PATH` | Path to palace data directory (default: /data/palace) |

## Local Development

```bash
cp .env.example .env
# fill in .env
./start.sh
```

## Deploy

Push to master — GitHub Actions builds and pushes to GHCR, then triggers Portainer webhook.

## Endpoints

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/sse` | GET | Required | SSE stream for MCP client connections |
| `/message` | POST | Required | JSON-RPC messages from MCP clients |
| `/health` | GET | No | Basic liveness check |
| `/debug/status` | GET | Required | Full stack health: subprocess alive + mempalace_status tool output |
| `/auth/login` | GET | No | Start Google OIDC flow |
| `/auth/callback` | GET | No | OAuth2 callback |

### `/debug/status`

Returns subprocess health and mempalace internal status:

```json
{
  "alive": true,
  "mempalace": { "...": "raw response from mempalace_status tool" }
}
```

Returns 504 if the subprocess does not respond within 5 seconds.

## Auto-Initialization

On startup, the server checks for `~/.mempalace/config.json`. If missing, it creates the config and palace data directory automatically so the MCP server can accept writes immediately without manual `mempalace init`.

## MCP Client Configuration

Connect your MCP client to `https://your-domain.com/sse` with the session cookie or token query parameter for auth.
