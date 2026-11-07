// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package chat

import (
	"context"
	"strings"
	"testing"
)

func TestApplyTokenSaversAntiSlop(t *testing.T) {
	h, cleanup := setupHandlerForForward(t)
	defer cleanup()

	body := []byte(`{"messages":[{"role":"user","content":"generate landing page"}]}`)

	t.Run("antislop_off_does_nothing", func(t *testing.T) {
		h.TokenSaver.SetAntislopLevel("off")
		out, _, _, _ := h.applyTokenSavers(context.Background(), body, false)
		if strings.Contains(string(out), "antislop") {
			t.Errorf("output should not contain antislop when off")
		}
	})

	t.Run("antislop_lite_injects_prompt", func(t *testing.T) {
		h.TokenSaver.SetAntislopLevel("lite")
		out, _, _, _ := h.applyTokenSavers(context.Background(), body, false)
		if !strings.Contains(string(out), "You use antislop: rules for AI coding agents") {
			t.Errorf("output missing antislop lite prompt")
		}
	})

	t.Run("antislop_claude_native", func(t *testing.T) {
		h.TokenSaver.SetAntislopLevel("lite")
		out, _, _, _ := h.applyTokenSavers(context.Background(), body, true)
		if !strings.Contains(string(out), `"system"`) || !strings.Contains(string(out), "You use antislop") {
			t.Errorf("output missing claude native antislop system field: %s", string(out))
		}
	})
}
