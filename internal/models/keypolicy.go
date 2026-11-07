// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package models

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// keypolicy.go holds the parsed shapes of the serenhope per-key governance
// columns (port(serenhope)): the IP whitelist, the endpoint permissions and
// the endpoint families a permission can name. Both surfaces that read the
// columns — the dashboard handler that validates an edit and the middleware
// that enforces it per request — come here, so one parser cannot drift from
// the other.

// KeyEndpointFamily names one family of engine endpoints a key's permissions
// can allow. The set is closed: a family exists only when a route group
// answers to it.
type KeyEndpointFamily string

const (
	// KeyEndpointChat covers the completion surfaces: /chat/completions,
	// /messages (+count_tokens), /responses (+compact) and /api/chat.
	KeyEndpointChat KeyEndpointFamily = "chat"
	// KeyEndpointModels covers the model catalogs (/models, /v1/models and
	// their aliases).
	KeyEndpointModels KeyEndpointFamily = "models"
	// KeyEndpointEmbeddings covers /embeddings and /rerank.
	KeyEndpointEmbeddings KeyEndpointFamily = "embeddings"
	// KeyEndpointMedia covers images, audio and videos.
	KeyEndpointMedia KeyEndpointFamily = "media"
	// KeyEndpointWeb covers the web tools: /search, /scrape, /web/fetch and
	// /systemone.
	KeyEndpointWeb KeyEndpointFamily = "web"
	// KeyEndpointFiles covers /v1/files and its aliases.
	KeyEndpointFiles KeyEndpointFamily = "files"
	// KeyEndpointBatches covers /v1/batches and its aliases.
	KeyEndpointBatches KeyEndpointFamily = "batches"
)

// keyEndpointFamilies is the canonical set, used to validate a permission
// object at the boundary instead of discovering a typo mid-request.
var keyEndpointFamilies = map[KeyEndpointFamily]bool{
	KeyEndpointChat:       true,
	KeyEndpointModels:     true,
	KeyEndpointEmbeddings: true,
	KeyEndpointMedia:      true,
	KeyEndpointWeb:        true,
	KeyEndpointFiles:      true,
	KeyEndpointBatches:    true,
}

// IsValidKeyEndpointFamily reports whether name is one of the known families.
func IsValidKeyEndpointFamily(name string) bool {
	return keyEndpointFamilies[KeyEndpointFamily(name)]
}

// KeyEndpointFamilies lists every known family, for dashboards that render
// the choices.
func KeyEndpointFamilies() []KeyEndpointFamily {
	return []KeyEndpointFamily{
		KeyEndpointChat, KeyEndpointModels, KeyEndpointEmbeddings,
		KeyEndpointMedia, KeyEndpointWeb, KeyEndpointFiles, KeyEndpointBatches,
	}
}

// EndpointFamilyForPath maps an engine request path onto the family its
// permissions are written against. The second return is false for paths that
// belong to no family — infra endpoints (headroom, usage streams, OAuth) and
// anything the API-key middleware chain does not guard. An unmapped path is
// never permission-gated: the empty permission object means "the chat
// families", not "everything", but the families are defined by the routes
// they name, so a path outside every family has no rule to violate.
func EndpointFamilyForPath(path string) (KeyEndpointFamily, bool) {
	p := path
	// Versioned and /api-prefixed aliases mount the same handlers, so the
	// family follows the handler, not the prefix.
	for _, prefix := range []string{"/v1/", "/api/v1/", "/api/"} {
		if strings.HasPrefix(p, prefix) {
			p = strings.TrimPrefix(p, prefix)
			// /v1/models style paths lose their version but keep the rest.
			break
		}
	}
	p = strings.TrimPrefix(p, "/")
	switch {
	case p == "chat/completions" || p == "chat" ||
		p == "messages" || strings.HasPrefix(p, "messages/") ||
		p == "responses" || strings.HasPrefix(p, "responses/"):
		return KeyEndpointChat, true
	case p == "models" || strings.HasPrefix(p, "models/") ||
		strings.HasPrefix(p, "models:"):
		return KeyEndpointModels, true
	case p == "embeddings" || p == "rerank":
		return KeyEndpointEmbeddings, true
	case strings.HasPrefix(p, "images/") ||
		strings.HasPrefix(p, "audio/") ||
		strings.HasPrefix(p, "videos/"):
		return KeyEndpointMedia, true
	case p == "search" || p == "scrape" || p == "web/fetch" || p == "systemone":
		return KeyEndpointWeb, true
	case p == "files" || strings.HasPrefix(p, "files/"):
		return KeyEndpointFiles, true
	case p == "batches" || strings.HasPrefix(p, "batches/"):
		return KeyEndpointBatches, true
	}
	return "", false
}

// KeyPermissions is the parsed shape of the permissions column: the endpoint
// families the key may call and whether it may hold admin-shaped grants.
// Upstream (src/lib/auth/permissionPaths.js) gates dashboard sessions with a
// fixed set of booleans; on this gateway the admin surface never accepts an
// engine key, so the object narrows to what an engine key can actually do —
// the endpoint families — and Admin survives only as a parsed-but-inert field
// so a restore from upstream keeps loading.
type KeyPermissions struct {
	Endpoints []string `json:"endpoints,omitempty"`
	Admin     bool     `json:"admin,omitempty"`
}

// ParseKeyPermissions parses the stored permissions column. An empty or
// absent value parses to the zero object, which means every chat-family
// endpoint is allowed — the default keys created before the column existed
// must keep meaning. Unknown family names are refused, not ignored: a typo
// that silently granted nothing would look like a working key until the
// client hit the one endpoint the operator meant to allow.
func ParseKeyPermissions(raw string) (KeyPermissions, error) {
	var perms KeyPermissions
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return perms, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &perms); err != nil {
		return perms, fmt.Errorf("parse key permissions: %w", err)
	}
	for _, name := range perms.Endpoints {
		if !IsValidKeyEndpointFamily(name) {
			return perms, fmt.Errorf("parse key permissions: unknown endpoint family %q", name)
		}
	}
	return perms, nil
}

// AllowsEndpoint reports whether the parsed permissions let the key call the
// given family. Empty permissions allow every family: the column stores a
// restriction list, not a grant list, so absence of a restriction is access —
// the same direction the upstream default (every dashboard permission false
// except viewUsage) does not take, but the one an engine key must: a key that
// existed before the column shipped cannot wake up locked out of chat.
func (p KeyPermissions) AllowsEndpoint(family KeyEndpointFamily) bool {
	if len(p.Endpoints) == 0 {
		return true
	}
	for _, name := range p.Endpoints {
		if KeyEndpointFamily(name) == family {
			return true
		}
	}
	return false
}

// ParseIPWhitelist parses the stored ipWhitelist column into prefixes. An
// empty value parses to nil, which the caller reads as "every address is
// allowed". Entries are IP literals or CIDR ranges, accepted as a JSON array
// of strings or — the shape a restore from upstream carries, since upstream
// wrote the column as a plain string — a comma-separated list. Exact
// addresses become single-host prefixes, so the check downstream is one
// prefix match either way.
func ParseIPWhitelist(raw string) ([]netip.Prefix, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	var entries []string
	if strings.HasPrefix(trimmed, "[") {
		var items []string
		if err := json.Unmarshal([]byte(trimmed), &items); err != nil {
			return nil, fmt.Errorf("parse ip whitelist: %w", err)
		}
		entries = items
	} else {
		for _, item := range strings.Split(trimmed, ",") {
			entries = append(entries, item)
		}
	}
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, item := range entries {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			prefix, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("parse ip whitelist entry %q: %w", item, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		addr, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("parse ip whitelist entry %q: %w", item, err)
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
	}
	return prefixes, nil
}

// IPAllowed reports whether addr falls inside the whitelist. A nil or empty
// whitelist allows every address — an empty column never locks a key out.
func IPAllowed(addr netip.Addr, whitelist []netip.Prefix) bool {
	if len(whitelist) == 0 {
		return true
	}
	if addr.Is4() || addr.Is4In6() {
		addr = netip.AddrFrom4(addr.As4())
	}
	for _, prefix := range whitelist {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
