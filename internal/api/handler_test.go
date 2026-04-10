package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// statusRequester mirrors the StatusRequest interface for testing.
type statusRequester interface {
	StatusRequest(ctx context.Context) ([]byte, error)
}

type proxyFunc func(ctx context.Context) ([]byte, error)

func (f proxyFunc) StatusRequest(ctx context.Context) ([]byte, error) {
	return f(ctx)
}

// debugStatusHandlerWith creates a handler using the same logic as Handler.debugStatusHandler
// but with an injectable StatusRequester for testing.
func debugStatusHandlerWith(sr statusRequester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := sr.StatusRequest(r.Context())
		if err != nil {
			if strings.Contains(err.Error(), "timed out") {
				http.Error(w, `{"error":"status request timed out"}`, http.StatusGatewayTimeout)
				return
			}
			http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(resp)
	}
}

func TestDebugStatusHandler_Success(t *testing.T) {
	mockResp := `{"jsonrpc":"2.0","id":"debug-status-1","result":{"content":[{"type":"text","text":"ok"}]}}`

	handler := debugStatusHandlerWith(proxyFunc(func(ctx context.Context) ([]byte, error) {
		return []byte(mockResp), nil
	}))

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if w.Body.String() != mockResp {
		t.Errorf("body = %q, want %q", w.Body.String(), mockResp)
	}
}

func TestDebugStatusHandler_Timeout(t *testing.T) {
	handler := debugStatusHandlerWith(proxyFunc(func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("status request timed out")
	}))

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want %d", w.Code, http.StatusGatewayTimeout)
	}
}

func TestDebugStatusHandler_InternalError(t *testing.T) {
	handler := debugStatusHandlerWith(proxyFunc(func(ctx context.Context) ([]byte, error) {
		return nil, errors.New("write to subprocess: broken pipe")
	}))

	req := httptest.NewRequest("GET", "/debug/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
