// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

var (
	// ErrWebExecutorsDisabled indicates web executor capabilities are turned off.
	ErrWebExecutorsDisabled = errors.New("gemini-web: web executors disabled by policy (ENABLE_WEB_EXECUTORS=false)")
	// ErrBrowserLeaseTimeout indicates queue wait exceeded the deadline.
	ErrBrowserLeaseTimeout = errors.New("gemini-web: browser lease acquisition timed out")
)

// GeminiDOMState holds session tokens and model selection state extracted from the Gemini Web DOM.
type GeminiDOMState struct {
	SNlM0e   string `json:"snlm0e,omitempty"`
	CFB2H    string `json:"cfb2h,omitempty"`
	ModelHex string `json:"modelHex,omitempty"`
	Title    string `json:"title,omitempty"`
}

// GeminiBrowserLease represents an exclusive reservation of a browser instance or session tab.
type GeminiBrowserLease struct {
	ID           string
	ConnectionID string
	AcquiredAt   time.Time
	pool         *GeminiBrowserLeasePool
	released     bool
	mu           sync.Mutex
}

// Release yields the lease back to the pool.
func (l *GeminiBrowserLease) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return
	}
	l.released = true
	if l.pool != nil {
		l.pool.release(l)
	}
}

// GeminiBrowserLeasePool manages a bounded queue of headless browser tabs/sessions.
type GeminiBrowserLeasePool struct {
	mu          sync.Mutex
	capacity    int
	inUse       int
	queue       chan struct{}
	waitTimeout time.Duration
}

// DefaultGeminiLeasePool is the package-level pool.
var DefaultGeminiLeasePool = NewGeminiBrowserLeasePool(2, 30*time.Second)

// NewGeminiBrowserLeasePool creates a pool with maximum concurrent leases.
func NewGeminiBrowserLeasePool(capacity int, waitTimeout time.Duration) *GeminiBrowserLeasePool {
	if capacity <= 0 {
		capacity = 2
	}
	if waitTimeout <= 0 {
		waitTimeout = 30 * time.Second
	}
	p := &GeminiBrowserLeasePool{
		capacity:    capacity,
		waitTimeout: waitTimeout,
		queue:       make(chan struct{}, capacity),
	}
	for i := 0; i < capacity; i++ {
		p.queue <- struct{}{}
	}
	return p
}

// Acquire requests a browser lease. It is gated by WebExecutorsEnabled.
func (p *GeminiBrowserLeasePool) Acquire(ctx context.Context, connectionID string) (*GeminiBrowserLease, error) {
	if !WebExecutorsEnabled() {
		return nil, ErrWebExecutorsDisabled
	}

	timeout := p.waitTimeout
	if dl, ok := ctx.Deadline(); ok {
		remaining := time.Until(dl)
		if remaining < timeout {
			timeout = remaining
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-p.queue:
		p.mu.Lock()
		p.inUse++
		p.mu.Unlock()
		return &GeminiBrowserLease{
			ID:           "lease_" + uuid.NewString(),
			ConnectionID: connectionID,
			AcquiredAt:   time.Now().UTC(),
			pool:         p,
		}, nil
	case <-timer.C:
		return nil, ErrBrowserLeaseTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *GeminiBrowserLeasePool) release(l *GeminiBrowserLease) {
	p.mu.Lock()
	if p.inUse > 0 {
		p.inUse--
	}
	p.mu.Unlock()
	select {
	case p.queue <- struct{}{}:
	default:
	}
}

// CaptureGeminiDOMState connects to a Chrome DevTools Protocol (CDP) WebSocket endpoint
// and evaluates expressions to extract Gemini web application state (SNlM0e, cfb2h, model hex).
func CaptureGeminiDOMState(ctx context.Context, wsURL string) (*GeminiDOMState, error) {
	if !WebExecutorsEnabled() {
		return nil, ErrWebExecutorsDisabled
	}
	if wsURL == "" {
		return nil, errors.New("gemini-web: cdp websocket url is required")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gemini-web: cdp dial failed: %w", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	defer conn.Close()

	// JavaScript expression to extract window.WIZ_global_data and active model element
	jsExpr := `(() => {
		const out = { snlm0e: "", cfb2h: "", modelHex: "", title: document.title || "" };
		try {
			if (window.WIZ_global_data) {
				out.snlm0e = window.WIZ_global_data.SNlM0e || "";
				out.cfb2h = window.WIZ_global_data.cfb2h || "";
			}
			const modelEl = document.querySelector('[data-model-id], [jsname="wPknJb"]');
			if (modelEl) {
				out.modelHex = modelEl.getAttribute('data-model-id') || modelEl.getAttribute('data-value') || "";
			}
		} catch(e) {}
		return JSON.stringify(out);
	})()`

	type cdpEvaluateReq struct {
		ID     int            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}

	reqPayload := cdpEvaluateReq{
		ID:     101,
		Method: "Runtime.evaluate",
		Params: map[string]any{
			"expression":    jsExpr,
			"returnByValue": true,
		},
	}

	reqBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, err
	}

	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, reqBytes); err != nil {
		return nil, fmt.Errorf("gemini-web: cdp write failed: %w", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("gemini-web: cdp read error: %w", err)
		}

		var cdpResp struct {
			ID     int `json:"id"`
			Result struct {
				Result struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"result"`
			} `json:"result"`
			Error any `json:"error"`
		}

		if err := json.Unmarshal(msg, &cdpResp); err != nil {
			continue
		}
		if cdpResp.ID != 101 {
			continue
		}
		if cdpResp.Error != nil {
			return nil, fmt.Errorf("gemini-web: cdp evaluation error: %v", cdpResp.Error)
		}

		var state GeminiDOMState
		if err := json.Unmarshal([]byte(cdpResp.Result.Result.Value), &state); err != nil {
			return nil, fmt.Errorf("gemini-web: unmarshal dom state failed: %w", err)
		}
		return &state, nil
	}
}
