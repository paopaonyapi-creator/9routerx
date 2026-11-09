// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package dashboard

import (
	"net/http"
	"strings"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/log"
)

func protectKeyReveal(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func logKeyReveal(r *http.Request, kind, id string) {
	masked := strings.Repeat("*", handlerutil.RuneCount(id))
	if handlerutil.RuneCount(id) > 8 {
		masked = handlerutil.RunePrefix(id, 4) + "…" + handlerutil.RuneSuffix(id, 4)
	}
	log.InfoCtx(r.Context(), "apikeys", "apikeys reveal", "resource_kind", kind, "key_id", masked, "remote_addr", r.RemoteAddr)
}
