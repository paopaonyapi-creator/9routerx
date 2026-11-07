// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"9routerx/proxy/internal/db"
)

// usageWindowDays is the window usage-report aggregates over. A week matches
// the dashboard's default usage range, so an agent and an operator reading the
// same gateway see the same numbers.
const usageWindowDays = 7

// a2aMessage is one conversation turn. Content is text only: every skill here
// is a read, so nothing needs multimodal parts.
type a2aMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// sendParams carries the params shared by message/send and message/stream.
// Metadata is passed through to the skill that wants it (a limit, a filter).
type sendParams struct {
	Skill    string         `json:"skill"`
	Messages []a2aMessage   `json:"messages"`
	Metadata map[string]any `json:"metadata"`
}

// decodeSendParams validates the shared params. An unknown skill is
// errMethodNotFound, matching the reference doc's error table; a missing
// message is errInvalidParams.
func decodeSendParams(raw json.RawMessage) (sendParams, *rpcError) {
	var p sendParams
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return p, &rpcError{Code: errInvalidParams, Message: "invalid params"}
		}
	}
	if p.Skill == "" {
		return p, &rpcError{Code: errInvalidParams, Message: "skill is required"}
	}
	if _, ok := skillHandlers[p.Skill]; !ok {
		return p, &rpcError{Code: errMethodNotFound, Message: "unknown skill: " + p.Skill}
	}
	if len(p.Messages) == 0 {
		return p, &rpcError{Code: errInvalidParams, Message: "messages is required"}
	}
	return p, nil
}

// sendMessage runs one skill to completion and answers synchronously. A skill
// that fails still leaves a queryable task in the failed state, and the
// JSON-RPC error carries the same message the task records.
func (s *Server) sendMessage(ctx context.Context, p sendParams) (map[string]any, *rpcError) {
	task := s.tasks.Create(p.Skill)
	s.tasks.Transition(task.ID, StateWorking)

	payload, err := runSkill(ctx, s.repo, p)
	if err != nil {
		rpcErr := &rpcError{Code: errInternal, Message: err.Error()}
		s.tasks.Fail(task.ID, rpcErr)
		return nil, rpcErr
	}

	artifacts := []Artifact{{Type: "data", Name: p.Skill, Data: payload}}
	metadata := map[string]any{"skill": p.Skill}
	s.tasks.Complete(task.ID, artifacts, metadata)
	done, _ := s.tasks.Get(task.ID)
	return map[string]any{
		"task":      done,
		"artifacts": done.Artifacts,
		"metadata":  done.Metadata,
	}, nil
}

// streamMessage runs one skill over SSE: a status event carrying the task in
// the working state, then one final event carrying the same result
// message/send would have returned, then the stream closes. A client that only
// reads the last frame gets the synchronous answer for free.
func (s *Server) streamMessage(w http.ResponseWriter, r *http.Request, id json.RawMessage, p sendParams) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeRPCError(w, http.StatusInternalServerError, id, errInternal, "streaming unsupported")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(v any) bool {
		body, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := w.Write([]byte("data: " + string(body) + "\n\n")); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	task := s.tasks.Create(p.Skill)
	s.tasks.Transition(task.ID, StateWorking)
	working, _ := s.tasks.Get(task.ID)
	if !send(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "message/stream",
		"params":  map[string]any{"task": working},
	}) {
		return
	}

	select {
	case <-r.Context().Done():
		return
	default:
	}

	payload, err := runSkill(r.Context(), s.repo, p)
	if err != nil {
		rpcErr := &rpcError{Code: errInternal, Message: err.Error()}
		s.tasks.Fail(task.ID, rpcErr)
		_ = send(rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr})
		return
	}

	artifacts := []Artifact{{Type: "data", Name: p.Skill, Data: payload}}
	s.tasks.Complete(task.ID, artifacts, map[string]any{"skill": p.Skill})
	done, _ := s.tasks.Get(task.ID)
	_ = send(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"task":      done,
			"artifacts": done.Artifacts,
			"metadata":  done.Metadata,
		},
	})
}

// taskMethod serves tasks/get, tasks/list and tasks/cancel over the task map.
func (s *Server) taskMethod(method string, raw json.RawMessage) (any, *rpcError) {
	var req struct {
		TaskID string `json:"taskId"`
		Limit  int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, &rpcError{Code: errInvalidParams, Message: "invalid params"}
		}
	}

	switch method {
	case "tasks/get":
		if req.TaskID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "taskId is required"}
		}
		task, ok := s.tasks.Get(req.TaskID)
		if !ok {
			return nil, &rpcError{Code: errInvalidParams, Message: "task not found: " + req.TaskID}
		}
		return map[string]any{"task": task}, nil

	case "tasks/cancel":
		if req.TaskID == "" {
			return nil, &rpcError{Code: errInvalidParams, Message: "taskId is required"}
		}
		task, ok := s.tasks.Cancel(req.TaskID)
		if !ok {
			return nil, &rpcError{Code: errInvalidParams, Message: "task not found: " + req.TaskID}
		}
		return map[string]any{"task": task}, nil

	default: // tasks/list
		return map[string]any{"tasks": s.tasks.List(req.Limit), "total": s.tasks.Count()}, nil
	}
}

// runSkill dispatches one skill against the repo. A skill error is reported as
// a task failure, not a protocol error: the caller asked for a read and the
// read did not happen.
func runSkill(ctx context.Context, repo *db.Repo, p sendParams) (map[string]any, error) {
	if repo == nil {
		return nil, errors.New("repo not available")
	}
	handler, ok := skillHandlers[p.Skill]
	if !ok {
		return nil, errors.New("unknown skill: " + p.Skill)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return handler(ctx, repo, p)
}

// usageWindowStart is the cutoff usage-report aggregates from, in the RFC3339
// UTC form the usage ledger stores.
func usageWindowStart() string {
	return time.Now().UTC().AddDate(0, 0, -usageWindowDays).Format(time.RFC3339)
}
