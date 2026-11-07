// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package envcompat resolves renamed environment variables with a one-version
// deprecation fallback: the 9ROUTERX_ name wins, the pre-rename name
// (9ROUTERX_* / 9ROUTER_*) still answers when the new one is unset, with a
// warning so an operator knows to migrate. The fallback exists so an upgrade
// never breaks a running deployment's .env or systemd unit; the warning exists
// so the break is announced, not silent. Remove this package when the
// deprecated names have had a full release cycle to disappear.
package envcompat

import (
	"os"
	"strings"
	"sync"
)

// logged dedupes the deprecation warning so a hot path polling the same
// variable warns once per process, not once per request.
var (
	loggedMu sync.Mutex
	logged   = map[string]bool{}
)

// Getenv returns the first non-empty value among the given names, in order.
// A name that is not the first is treated as deprecated: when it answers, a
// one-time warning names both spellings. Empty names are skipped, so callers
// can build the list conditionally.
func Getenv(names ...string) string {
	for i, name := range names {
		if name == "" {
			continue
		}
		value := os.Getenv(name)
		if value == "" {
			continue
		}
		if i > 0 {
			warnOnce(names[0], name)
		}
		return value
	}
	return ""
}

// warnOnce prints the deprecation warning one time per (new, old) pair.
func warnOnce(current, legacy string) {
	loggedMu.Lock()
	defer loggedMu.Unlock()
	key := current + "\x00" + legacy
	if logged[key] {
		return
	}
	logged[key] = true
	// A plain stderr line: this runs before/without the structured logger in
	// some call sites, and the warning must survive either way.
	os.Stderr.WriteString("WARNING: environment variable " + legacy +
		" is deprecated; rename it to " + current + " (the old name still works this release)\n")
}

// IsDeprecatedSet reports whether any legacy name in the list carries a value
// while the current name does not — the shape a startup doctor check wants.
func IsDeprecatedSet(current string, legacy ...string) bool {
	if strings.TrimSpace(os.Getenv(current)) != "" {
		return false
	}
	for _, name := range legacy {
		if name != "" && strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}
