// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
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
	"github.com/google/uuid"
)

const (
	chatGPTWebBaseURL        = "https://chatgpt.com/backend-api/conversation"
	chatGPTWebUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	chatGPTSentinelErrorCode = "sentinel_required"
)

// ForwardChatGPTWeb routes a request to ChatGPT Web conversation backend.
func ForwardChatGPTWeb(w http.ResponseWriter, req *Request) error {
	if !CheckWebExecutorAllowed(w, "chatgpt-web") {
		return &proxy.UpstreamError{
			StatusCode: http.StatusForbidden,
			Body:       []byte(`{"error":{"code":"web_executors_disabled","message":"chatgpt-web is disabled by policy","type":"policy_error"}}`),
		}
	}

	var parsed struct {
		Model    string                   `json:"model"`
		Messages []translator.OpenAIMessage `json:"messages"`
		Stream   bool                     `json:"stream"`
		Tools    []any                    `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(req.Body, &parsed); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "chatgpt-web: invalid request body")
		return err
	}

	token := resolveChatGPTWebToken(req)
	if token == "" {
		handlerutil.WriteJSONErrorCode(w, http.StatusUnauthorized, "missing_session",
			"chatgpt-web: missing session token or accessToken (cookie __Secure-next-auth.session-token)")
		return &proxy.UpstreamError{
			StatusCode: http.StatusUnauthorized,
			Body:       []byte(`{"error":{"code":"missing_session","message":"chatgpt-web: missing session token","type":"auth_error"}}`),
		}
	}

	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	model := parsed.Model
	if model == "" || model == "auto" {
		model = "gpt-4o"
	}
	model = strings.TrimPrefix(model, "chatgpt-web/")
	model = strings.TrimPrefix(model, "cweb/")

	// Build prompt from messages
	prompt := renderChatGPTWebMessages(parsed.Messages)

	// Build ChatGPT conversation payload
	msgID := uuid.NewString()
	parentID := uuid.NewString()

	payload := map[string]any{
		"action": "next",
		"messages": []any{
			map[string]any{
				"id": msgID,
				"author": map[string]any{
					"role": "user",
				},
				"content": map[string]any{
					"content_type": "text",
					"parts":        []string{prompt},
				},
				"metadata": map[string]any{},
			},
		},
		"parent_message_id":             parentID,
		"model":                         model,
		"timezone_offset_min":           -420,
		"history_and_training_disabled": true,
	}

	payloadBytes, err := json.Marshal(payload, json.Deterministic(true))
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "chatgpt-web: marshal error")
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, chatGPTWebBaseURL, bytes.NewReader(payloadBytes))
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("User-Agent", chatGPTWebUserAgent)
	if strings.HasPrefix(token, "ey") {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	} else {
		httpReq.Header.Set("Cookie", "__Secure-next-auth.session-token="+token)
	}

	client := req.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadGateway, "chatgpt-web: upstream request failed: "+err.Error())
		return err
	}
	defer resp.Body.Close()

	// Detect Sentinel / Cloudflare challenges
	if resp.StatusCode == http.StatusForbidden {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if strings.Contains(string(bodyBytes), "sentinel") || strings.Contains(string(bodyBytes), "arkose") {
			handlerutil.WriteJSONErrorCode(w, http.StatusForbidden, chatGPTSentinelErrorCode,
				"chatgpt-web: sentinel verification required")
			return &proxy.UpstreamError{
				StatusCode: http.StatusForbidden,
				Body:       []byte(`{"error":{"code":"sentinel_required","message":"chatgpt-web: sentinel verification required","type":"anti_bot_error"}}`),
			}
		}
		handlerutil.WriteJSONError(w, http.StatusForbidden, "chatgpt-web: 403 forbidden: "+string(bodyBytes))
		return &proxy.UpstreamError{StatusCode: http.StatusForbidden, Body: bodyBytes}
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		handlerutil.WriteJSONError(w, resp.StatusCode, fmt.Sprintf("chatgpt-web: upstream error %d: %s", resp.StatusCode, string(bodyBytes)))
		return &proxy.UpstreamError{StatusCode: resp.StatusCode, Body: bodyBytes}
	}

	return streamChatGPTWebResponse(w, resp.Body, model, req.IsStream)
}

func resolveChatGPTWebToken(req *Request) string {
	if req.APIKey != "" && req.APIKey != "anonymous" {
		return strings.TrimSpace(req.APIKey)
	}
	if req.ConnData != nil {
		if t, ok := req.ConnData["sessionToken"].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
		if t, ok := req.ConnData["accessToken"].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
		if t, ok := req.ConnData["apiKey"].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return ""
}

func renderChatGPTWebMessages(messages []translator.OpenAIMessage) string {
	var parts []string
	for _, m := range messages {
		text := ""
		if s, ok := m.Content.(string); ok {
			text = s
		}
		if text != "" {
			parts = append(parts, fmt.Sprintf("[%s]: %s", m.Role, text))
		}
	}
	return strings.Join(parts, "\n\n")
}

func streamChatGPTWebResponse(w http.ResponseWriter, upstream io.Reader, model string, isStream bool) error {
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
	id := fmt.Sprintf("chatcmpl-cweb-%d", time.Now().UnixMilli())
	lastLen := 0

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

		// Extract text from parts
		if msg, ok := event["message"].(map[string]any); ok {
			if content, ok := msg["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok && len(parts) > 0 {
					if fullText, ok := parts[0].(string); ok {
						if len(fullText) > lastLen {
							delta := fullText[lastLen:]
							lastLen = len(fullText)
							accumulated.WriteString(delta)

							if isStream {
								chunk := map[string]any{
									"id":      id,
									"object":  "chat.completion.chunk",
									"created": time.Now().Unix(),
									"model":   model,
									"choices": []any{
										map[string]any{
											"index":         0,
											"delta":         map[string]string{"content": delta},
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
				}
			}
		}
	}

	if isStream {
		// Terminal chunk
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

	// Non-streaming completion
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
