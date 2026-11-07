// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	json "encoding/json/v2"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/proxy"
	"9routerx/proxy/internal/translator"
)

const (
	baiduMuseWebBaseURL = "https://yiyan.baidu.com/eb/chat/conversation/v2"
	baiduMuseUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
)

// ForwardMuseWeb handles chat completions for Baidu Muse Spark via web session (port(serenhope) TASK-5.3).
// Distinct from Meta Model API in muse.go.
func ForwardMuseWeb(w http.ResponseWriter, req *Request) error {
	if !CheckWebExecutorAllowed(w, "muse-web") {
		return &proxy.UpstreamError{
			StatusCode: http.StatusForbidden,
			Body:       []byte(`{"error":{"code":"web_executors_disabled","message":"muse-web is disabled by policy","type":"policy_error"}}`),
		}
	}

	var parsed struct {
		Model    string                   `json:"model"`
		Messages []translator.OpenAIMessage `json:"messages"`
		Stream   bool                     `json:"stream"`
	}
	if err := json.Unmarshal(req.Body, &parsed); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "muse-web: invalid request body")
		return err
	}

	sessionCookie := resolveBaiduMuseToken(req)
	if sessionCookie == "" {
		handlerutil.WriteJSONErrorCode(w, http.StatusUnauthorized, "missing_session",
			"muse-web: missing Baidu session cookie (BDUSS or BAIDUID)")
		return &proxy.UpstreamError{
			StatusCode: http.StatusUnauthorized,
			Body:       []byte(`{"error":{"code":"missing_session","message":"muse-web: missing session cookie","type":"auth_error"}}`),
		}
	}

	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	model := parsed.Model
	if model == "" || model == "auto" {
		model = "muse-spark"
	}
	model = strings.TrimPrefix(model, "muse-web/")
	model = strings.TrimPrefix(model, "mweb/")

	prompt := renderBaiduMusePrompt(parsed.Messages)
	if prompt == "" {
		prompt = "Hello"
	}

	payload := map[string]any{
		"text":        prompt,
		"stream":      true,
		"model":       model,
		"sign_type":   1,
		"timestamp":   time.Now().Unix(),
		"device_type": "pc",
	}

	payloadBytes, err := json.Marshal(payload, json.Deterministic(true))
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "muse-web: marshal error")
		return err
	}

	endpointURL := baiduMuseWebBaseURL
	if req.Config != nil && req.Config.BaseURL != "" {
		endpointURL = req.Config.BaseURL
	}
	if req.ConnData != nil {
		if u, ok := req.ConnData["baseUrl"].(string); ok && strings.TrimSpace(u) != "" {
			endpointURL = strings.TrimSpace(u)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(payloadBytes))
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", baiduMuseUserAgent)
	if strings.Contains(sessionCookie, "=") {
		httpReq.Header.Set("Cookie", sessionCookie)
	} else {
		httpReq.Header.Set("Cookie", "BDUSS="+sessionCookie)
	}

	client := req.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadGateway, "muse-web: upstream error: "+err.Error())
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		handlerutil.WriteJSONError(w, resp.StatusCode, fmt.Sprintf("muse-web: upstream error %d: %s", resp.StatusCode, string(bodyBytes)))
		return &proxy.UpstreamError{StatusCode: resp.StatusCode, Body: bodyBytes}
	}

	return streamBaiduMuseResponse(w, resp.Body, model, req.IsStream)
}

func resolveBaiduMuseToken(req *Request) string {
	if req.APIKey != "" && req.APIKey != "anonymous" {
		return strings.TrimSpace(req.APIKey)
	}
	if req.ConnData != nil {
		for _, key := range []string{"bduss", "BDUSS", "sessionToken", "cookie", "apiKey", "accessToken"} {
			if v, ok := req.ConnData[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func renderBaiduMusePrompt(messages []translator.OpenAIMessage) string {
	var parts []string
	for _, m := range messages {
		if s, ok := m.Content.(string); ok && s != "" {
			parts = append(parts, fmt.Sprintf("[%s]: %s", m.Role, s))
		}
	}
	return strings.Join(parts, "\n\n")
}

func streamBaiduMuseResponse(w http.ResponseWriter, upstream io.Reader, model string, isStream bool) error {
	scanner := bufio.NewScanner(upstream)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	flusher, isFlusher := w.(http.Flusher)
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		if isFlusher {
			flusher.Flush()
		}
	}

	var accumulated strings.Builder
	id := fmt.Sprintf("chatcmpl-mweb-%d", time.Now().UnixMilli())

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "" || data == "[DONE]" {
			break
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		deltaText := ""
		// Extract delta from Baidu format (e.g. data.result or data.content or delta)
		if r, ok := event["result"].(string); ok {
			deltaText = r
		} else if d, ok := event["delta"].(string); ok {
			deltaText = d
		} else if c, ok := event["content"].(string); ok {
			deltaText = c
		}

		if deltaText != "" {
			accumulated.WriteString(deltaText)
			if isStream {
				chunk := map[string]any{
					"id":      id,
					"object":  "chat.completion.chunk",
					"created": time.Now().Unix(),
					"model":   model,
					"choices": []any{
						map[string]any{
							"index":         0,
							"delta":         map[string]string{"content": deltaText},
							"finish_reason": nil,
						},
					},
				}
				chunkBytes, _ := json.Marshal(chunk, json.Deterministic(true))
				_, _ = fmt.Fprintf(w, "data: %s\n\n", string(chunkBytes))
				if isFlusher {
					flusher.Flush()
				}
			}
		}
	}

	if isStream {
		termChunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []any{
				map[string]any{
					"index":         0,
					"delta":         map[string]string{},
					"finish_reason": "stop",
				},
			},
		}
		termBytes, _ := json.Marshal(termChunk, json.Deterministic(true))
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", string(termBytes))
		if isFlusher {
			flusher.Flush()
		}
		return nil
	}

	fullResp := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": accumulated.String(),
				},
				"finish_reason": "stop",
			},
		},
	}
	handlerutil.WriteJSON(w, http.StatusOK, fullResp)
	return nil
}
