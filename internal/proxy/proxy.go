package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MCPProxy manages a mempalace subprocess and multiplexes SSE clients.
type MCPProxy struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu       sync.Mutex
	sessions map[string]*sseSession // sessionID -> session
	pending  map[any]string         // jsonrpc id -> sessionID
	directCh map[string]chan []byte // jsonrpc id -> direct response channel (for internal requests)
}

type sseSession struct {
	id     string
	events chan []byte
	done   chan struct{}
}

// New starts the mempalace MCP subprocess and returns a proxy.
func New(ctx context.Context, palacePath string) (*MCPProxy, error) {
	args := []string{"-m", "mempalace.mcp_server"}
	if palacePath != "" {
		args = append(args, "--palace", palacePath)
	}

	cmd := exec.CommandContext(ctx, "python3", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = &logWriter{prefix: "mempalace"}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start mempalace: %w", err)
	}

	p := &MCPProxy{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		sessions: make(map[string]*sseSession),
		pending:  make(map[any]string),
		directCh: make(map[string]chan []byte),
	}

	go p.readLoop()
	slog.Info("mempalace subprocess started", "pid", cmd.Process.Pid)
	return p, nil
}

// readLoop reads JSON-RPC responses from the subprocess and routes them to the right SSE session.
func (p *MCPProxy) readLoop() {
	for {
		line, err := p.stdout.ReadBytes('\n')
		if err != nil {
			slog.Error("mempalace stdout closed", "error", err)
			return
		}

		// Parse to find the id for routing
		var msg struct {
			ID     any    `json:"id,omitempty"`
			Method string `json:"method,omitempty"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			slog.Warn("unparseable response from mempalace", "raw", string(line))
			continue
		}

		p.mu.Lock()
		if msg.ID != nil {
			nid := normalizeID(msg.ID)
			// Check direct response channels first (internal requests like debug/status)
			if ch, ok := p.directCh[nid]; ok {
				delete(p.directCh, nid)
				select {
				case ch <- line:
				default:
				}
			} else if sessionID, ok := p.pending[nid]; ok {
				// Response to a request — route to the session that sent it
				delete(p.pending, nid)
				if sess, exists := p.sessions[sessionID]; exists {
					select {
					case sess.events <- line:
					default:
						slog.Warn("session event buffer full, dropping", "session", sessionID)
					}
				}
			}
		} else if msg.Method != "" {
			// Server-initiated notification — broadcast to all sessions
			for _, sess := range p.sessions {
				select {
				case sess.events <- line:
				default:
				}
			}
		}
		p.mu.Unlock()
	}
}

// HandleSSE serves the SSE endpoint. Clients connect here to receive MCP responses.
func (p *MCPProxy) HandleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sess := &sseSession{
		id:     uuid.New().String(),
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}

	p.mu.Lock()
	p.sessions[sess.id] = sess
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.sessions, sess.id)
		p.mu.Unlock()
		close(sess.done)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send the endpoint event so the client knows where to POST messages
	messageURL := fmt.Sprintf("/message?session_id=%s", sess.id)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", messageURL)
	flusher.Flush()

	slog.Info("SSE session started", "session", sess.id)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Info("SSE session closed", "session", sess.id)
			return
		case data := <-sess.events:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// HandleMessage receives JSON-RPC requests from SSE clients and forwards them to the subprocess.
func (p *MCPProxy) HandleMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	_, exists := p.sessions[sessionID]
	p.mu.Unlock()

	if !exists {
		http.Error(w, "unknown session", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	// Parse to capture the id for routing
	var msg struct {
		ID any `json:"id,omitempty"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if msg.ID != nil {
		p.mu.Lock()
		p.pending[normalizeID(msg.ID)] = sessionID
		p.mu.Unlock()
	}

	// Forward to subprocess
	p.mu.Lock()
	_, err = p.stdin.Write(append(body, '\n'))
	p.mu.Unlock()

	if err != nil {
		slog.Error("write to mempalace failed", "error", err)
		http.Error(w, "proxy error", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// StatusRequest sends a mempalace_status tool call through the subprocess and returns the raw response.
// It uses a dedicated direct channel (not tied to any SSE session) with a 5-second timeout.
func (p *MCPProxy) StatusRequest(ctx context.Context) ([]byte, error) {
	reqID := "debug-status-" + uuid.New().String()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "mempalace_status",
			"arguments": map[string]any{},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	ch := make(chan []byte, 1)

	p.mu.Lock()
	p.directCh[normalizeID(reqID)] = ch
	_, err = p.stdin.Write(append(body, '\n'))
	p.mu.Unlock()

	if err != nil {
		p.mu.Lock()
		delete(p.directCh, normalizeID(reqID))
		p.mu.Unlock()
		return nil, fmt.Errorf("write to subprocess: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	select {
	case resp := <-ch:
		return resp, nil
	case <-timeoutCtx.Done():
		p.mu.Lock()
		delete(p.directCh, normalizeID(reqID))
		p.mu.Unlock()
		return nil, fmt.Errorf("status request timed out")
	}
}

// Stop terminates the subprocess.
func (p *MCPProxy) Stop() error {
	p.stdin.Close()
	return p.cmd.Wait()
}

// normalizeID converts JSON-RPC id to a comparable string key.
func normalizeID(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// logWriter sends subprocess stderr to slog.
type logWriter struct {
	prefix string
}

func (lw *logWriter) Write(p []byte) (int, error) {
	slog.Info("subprocess", "source", lw.prefix, "msg", string(p))
	return len(p), nil
}
