//go:build integration

// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package integration

import (
	json "encoding/json/v2"
	"testing"
)

// sseErrorEnvelope is the OpenAI error frame a client reads off the stream.
type sseErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// sseErrorFrame returns the first SSE data payload that carries an error key.
func sseErrorFrame(t *testing.T, body []byte) (sseErrorEnvelope, bool) {
	t.Helper()
	for _, payload := range sseDataLines(t, body) {
		var frame sseErrorEnvelope
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			continue
		}
		if frame.Error.Code != "" {
			return frame, true
		}
	}
	return sseErrorEnvelope{}, false
}
