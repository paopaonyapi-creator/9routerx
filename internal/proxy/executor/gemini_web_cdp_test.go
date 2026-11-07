// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type mockCDPServer struct {
	srv      *httptest.Server
	upgrader websocket.Upgrader
	snlm0e   string
	cfb2h    string
	modelHex string
}

func newMockCDPServer(t *testing.T, snlm0e, cfb2h, modelHex string) *mockCDPServer {
	t.Helper()
	s := &mockCDPServer{
		snlm0e:   snlm0e,
		cfb2h:    cfb2h,
		modelHex: modelHex,
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := s.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			if req.Method == "Runtime.evaluate" {
				valJSON, _ := json.Marshal(map[string]string{
					"snlm0e":   s.snlm0e,
					"cfb2h":    s.cfb2h,
					"modelHex": s.modelHex,
					"title":    "Gemini",
				})
				reply, _ := json.Marshal(map[string]any{
					"id": req.ID,
					"result": map[string]any{
						"result": map[string]any{
							"type":  "string",
							"value": string(valJSON),
						},
					},
				})
				_ = conn.WriteMessage(websocket.TextMessage, reply)
			}
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *mockCDPServer) wsURL() string {
	return "ws" + strings.TrimPrefix(s.srv.URL, "http")
}

func TestGeminiDOMCapture(t *testing.T) {
	SetWebExecutorsEnabled(true)
	defer SetWebExecutorsEnabled(false)

	mock := newMockCDPServer(t, "test-snlm0e-token", "test-cfb2h-token", "8c46e95b1a07cecc")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := CaptureGeminiDOMState(ctx, mock.wsURL())
	if err != nil {
		t.Fatalf("CaptureGeminiDOMState failed: %v", err)
	}
	if state.SNlM0e != "test-snlm0e-token" {
		t.Errorf("SNlM0e = %q, want test-snlm0e-token", state.SNlM0e)
	}
	if state.CFB2H != "test-cfb2h-token" {
		t.Errorf("CFB2H = %q, want test-cfb2h-token", state.CFB2H)
	}
	if state.ModelHex != "8c46e95b1a07cecc" {
		t.Errorf("ModelHex = %q, want 8c46e95b1a07cecc", state.ModelHex)
	}
	if state.Title != "Gemini" {
		t.Errorf("Title = %q, want Gemini", state.Title)
	}
}

func TestGeminiBrowserLeasePool(t *testing.T) {
	t.Run("gated_by_web_executors_disabled", func(t *testing.T) {
		SetWebExecutorsEnabled(false)
		pool := NewGeminiBrowserLeasePool(1, time.Second)
		_, err := pool.Acquire(context.Background(), "c1")
		if !errors.Is(err, ErrWebExecutorsDisabled) {
			t.Fatalf("err = %v, want ErrWebExecutorsDisabled", err)
		}
	})

	t.Run("acquire_and_release", func(t *testing.T) {
		SetWebExecutorsEnabled(true)
		defer SetWebExecutorsEnabled(false)

		pool := NewGeminiBrowserLeasePool(2, time.Second)

		ctx := context.Background()
		l1, err := pool.Acquire(ctx, "c1")
		if err != nil {
			t.Fatalf("acquire l1: %v", err)
		}
		if l1.ID == "" || l1.ConnectionID != "c1" {
			t.Errorf("invalid lease: %+v", l1)
		}

		l2, err := pool.Acquire(ctx, "c2")
		if err != nil {
			t.Fatalf("acquire l2: %v", err)
		}

		// Pool capacity is 2; 3rd acquire must timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		_, err = pool.Acquire(timeoutCtx, "c3")
		if err == nil {
			t.Fatal("expected timeout when pool saturated")
		}

		// Release l1 -> allows c3 to acquire
		l1.Release()

		l3, err := pool.Acquire(ctx, "c3")
		if err != nil {
			t.Fatalf("acquire l3 after release: %v", err)
		}
		l2.Release()
		l3.Release()
	})
}
