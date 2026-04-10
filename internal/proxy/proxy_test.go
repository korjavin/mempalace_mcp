package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// newTestProxy creates an MCPProxy with fake stdin/stdout for testing.
// Returns the proxy and a writer that simulates subprocess stdout.
func newTestProxy(t *testing.T) (*MCPProxy, io.Writer, io.ReadCloser) {
	t.Helper()

	// Pipe for subprocess stdout: test writes to stdinW, proxy reads from stdinR.
	stdoutR, stdoutW := io.Pipe()

	// Pipe for subprocess stdin: proxy writes to stdinW, test reads from stdinR.
	stdinR, stdinW := io.Pipe()

	p := &MCPProxy{
		stdin:    stdinW,
		stdout:   bufio.NewReader(stdoutR),
		sessions: make(map[string]*sseSession),
		pending:  make(map[any]string),
		directCh: make(map[string]chan []byte),
		exitCh:   make(chan struct{}),
	}

	go p.readLoop()

	t.Cleanup(func() {
		stdinW.Close()
		stdoutW.Close()
	})

	return p, stdoutW, stdinR
}

func TestStatusRequest_Success(t *testing.T) {
	p, stdoutW, stdinR := newTestProxy(t)

	// Read the request from stdin in a goroutine and send back a response.
	go func() {
		buf := make([]byte, 4096)
		n, err := stdinR.Read(buf)
		if err != nil {
			t.Errorf("read from stdin: %v", err)
			return
		}

		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.Unmarshal(buf[:n], &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
			return
		}

		if req.Method != "tools/call" {
			t.Errorf("method = %q, want tools/call", req.Method)
		}
		if req.Params.Name != "mempalace_status" {
			t.Errorf("tool name = %q, want mempalace_status", req.Params.Name)
		}
		if !strings.HasPrefix(req.ID, "debug-status-") {
			t.Errorf("id = %q, want prefix debug-status-", req.ID)
		}

		// Send back a mock response.
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Palace is healthy"},
				},
			},
		}
		respBytes, _ := json.Marshal(resp)
		stdoutW.Write(append(respBytes, '\n'))
	}()

	ctx := context.Background()
	resp, err := p.StatusRequest(ctx)
	if err != nil {
		t.Fatalf("StatusRequest: %v", err)
	}

	var result struct {
		ID     string `json:"id"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(result.Result.Content) == 0 {
		t.Fatal("expected content in response")
	}
	if result.Result.Content[0].Text != "Palace is healthy" {
		t.Errorf("text = %q, want 'Palace is healthy'", result.Result.Content[0].Text)
	}
}

func TestStatusRequest_Timeout(t *testing.T) {
	p, _, stdinR := newTestProxy(t)

	// Drain stdin so the Write in StatusRequest doesn't block on the synchronous pipe.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := stdinR.Read(buf); err != nil {
				return
			}
		}
	}()

	// Use a very short timeout context so we don't wait long.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// No response will be sent, so StatusRequest should time out.
	_, err := p.StatusRequest(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want 'timed out'", err.Error())
	}

	// Verify the direct channel was cleaned up.
	p.mu.Lock()
	remaining := len(p.directCh)
	p.mu.Unlock()
	if remaining != 0 {
		t.Errorf("directCh not cleaned up: %d entries remain", remaining)
	}
}

func TestStatusRequest_DoesNotInterfereWithSessions(t *testing.T) {
	p, stdoutW, stdinR := newTestProxy(t)

	// Create an SSE session.
	sess := &sseSession{
		id:     "test-session",
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}
	p.mu.Lock()
	p.sessions[sess.id] = sess
	p.mu.Unlock()

	// Simulate a session request being pending.
	p.mu.Lock()
	p.pending[normalizeID(float64(42))] = sess.id
	p.mu.Unlock()

	go func() {
		// Read the debug status request from stdin.
		buf := make([]byte, 4096)
		n, _ := stdinR.Read(buf)

		var req struct {
			ID string `json:"id"`
		}
		json.Unmarshal(buf[:n], &req)

		// Send back the debug response.
		debugResp, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"status": "ok"},
		})
		stdoutW.Write(append(debugResp, '\n'))

		// Also send a response for the session request (id=42).
		sessionResp, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      42,
			"result":  map[string]any{"session": "data"},
		})
		stdoutW.Write(append(sessionResp, '\n'))
	}()

	ctx := context.Background()
	resp, err := p.StatusRequest(ctx)
	if err != nil {
		t.Fatalf("StatusRequest: %v", err)
	}

	// Debug response should contain status ok.
	if !strings.Contains(string(resp), `"status":"ok"`) && !strings.Contains(string(resp), `"status": "ok"`) {
		t.Errorf("unexpected debug response: %s", resp)
	}

	// Session should also receive its response.
	select {
	case data := <-sess.events:
		if !strings.Contains(string(data), `"session"`) {
			t.Errorf("unexpected session response: %s", data)
		}
	case <-time.After(2 * time.Second):
		t.Error("session did not receive its response")
	}
}

func TestIsAlive_Running(t *testing.T) {
	p, _, _ := newTestProxy(t)

	// A test proxy without a real cmd hasn't exited, so IsAlive should be true.
	if !p.IsAlive() {
		t.Error("IsAlive() = false, want true for running proxy")
	}
}

func TestIsAlive_Exited(t *testing.T) {
	p, _, _ := newTestProxy(t)

	// Simulate subprocess exit.
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()

	if p.IsAlive() {
		t.Error("IsAlive() = true, want false for exited proxy")
	}
}

func TestIsAlive_RealProcess(t *testing.T) {
	// Use a real short-lived subprocess to test the full lifecycle.
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "echo", "hello")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	p := &MCPProxy{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   bufio.NewReader(stdout),
		sessions: make(map[string]*sseSession),
		pending:  make(map[any]string),
		directCh: make(map[string]chan []byte),
		exitCh:   make(chan struct{}),
	}

	go p.monitorProcess()

	// Wait for the process to exit (echo exits immediately).
	select {
	case <-p.exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for process exit")
	}

	if p.IsAlive() {
		t.Error("IsAlive() = true after process exited")
	}
}

func TestMonitorProcess_NilCmd(t *testing.T) {
	p := &MCPProxy{
		exitCh: make(chan struct{}),
	}
	// monitorProcess should return immediately without panicking when cmd is nil.
	p.monitorProcess()
}
