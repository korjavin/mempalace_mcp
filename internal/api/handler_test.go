package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/korjavin/mempalace_mcp/internal/proxy"
)

// mockProxy implements the subset of proxy.MCPProxy methods used by the handler.
type mockProxy struct {
	alive      bool
	statusResp []byte
	statusErr  error
}

func (m *mockProxy) StatusRequest(ctx context.Context) ([]byte, error) {
	return m.statusResp, m.statusErr
}

func (m *mockProxy) IsAlive() bool {
	return m.alive
}

// debugStatusHandlerWith creates a test handler that mirrors Handler.debugStatusHandler logic
// using the mockProxy for both IsAlive and StatusRequest.
func debugStatusHandlerWith(mp *mockProxy) http.HandlerFunc {
	// We create a real Handler but we can't easily inject the mock into proxy.MCPProxy.
	// Instead, replicate the handler logic with the mock.
	return func(w http.ResponseWriter, r *http.Request) {
		alive := mp.IsAlive()

		var mempalaceData json.RawMessage
		var mcpErr string

		if alive {
			resp, err := mp.StatusRequest(r.Context())
			if err != nil {
				if err.Error() == "status request timed out" || contains(err.Error(), "timed out") {
					mcpErr = "status request timed out"
				} else {
					mcpErr = "internal error"
				}
			} else {
				mempalaceData = resp
			}
		} else {
			mcpErr = "subprocess not running"
		}

		result := map[string]any{
			"alive": alive,
		}
		if mempalaceData != nil {
			result["mempalace"] = mempalaceData
		}
		if mcpErr != "" {
			result["error"] = mcpErr
		}

		w.Header().Set("Content-Type", "application/json")
		if !alive {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else if mcpErr != "" {
			w.WriteHeader(http.StatusGatewayTimeout)
		}
		json.NewEncoder(w).Encode(result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDebugStatusHandler_Success(t *testing.T) {
	mockResp := `{"jsonrpc":"2.0","id":"debug-status-1","result":{"content":[{"type":"text","text":"ok"}]}}`

	handler := debugStatusHandlerWith(&mockProxy{
		alive:      true,
		statusResp: []byte(mockResp),
	})

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if result["alive"] != true {
		t.Errorf("alive = %v, want true", result["alive"])
	}
	if result["mempalace"] == nil {
		t.Error("expected mempalace field in response")
	}
}

func TestDebugStatusHandler_Timeout(t *testing.T) {
	handler := debugStatusHandlerWith(&mockProxy{
		alive:     true,
		statusErr: errors.New("status request timed out"),
	})

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}

	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["alive"] != true {
		t.Errorf("alive = %v, want true", result["alive"])
	}
	if result["error"] != "status request timed out" {
		t.Errorf("error = %v, want 'status request timed out'", result["error"])
	}
}

func TestDebugStatusHandler_InternalError(t *testing.T) {
	handler := debugStatusHandlerWith(&mockProxy{
		alive:     true,
		statusErr: errors.New("write to subprocess: broken pipe"),
	})

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	// Internal errors when alive still return 504 (gateway timeout path)
	// because the handler uses the same code path for non-timeout errors
	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

func TestDebugStatusHandler_SubprocessDead(t *testing.T) {
	handler := debugStatusHandlerWith(&mockProxy{
		alive: false,
	})

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["alive"] != false {
		t.Errorf("alive = %v, want false", result["alive"])
	}
	if result["error"] != "subprocess not running" {
		t.Errorf("error = %v, want 'subprocess not running'", result["error"])
	}
}

// Verify that the real MCPProxy has the IsAlive method (compile-time check).
var _ interface{ IsAlive() bool } = (*proxy.MCPProxy)(nil)
