// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package middleware

import (
	"net/http"

	"9routerx/proxy/internal/updater"
)

// ProductIdentity stamps the API identity headers on every response
// (9RouterX rebrand FASE 0.3). Go's net/http sends no Server header by
// default, so without this middleware a probe cannot tell the gateway from
// anything else on the port; X-Powered-By carries the running version the
// same way the tray and the dashboard footer do.
//
// The headers are informational only: no client behavior keys off them, so a
// proxy in front may strip them without breaking anything.

const ProductName = "9RouterX"

func ProductIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Powered-By", ProductName+"/"+updater.CurrentVersion)
		next.ServeHTTP(w, r)
	})
}
