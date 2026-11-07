// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"

	"9routerx/proxy/internal/handlerutil"
	"9routerx/proxy/internal/providers"
)

// The dispatch guard is the URL-level half of the gateway's SSRF posture. The
// socket-level half lives in ssrf.go: a transport whose ControlContext hook
// receives each RESOLVED address at connect time and refuses private and
// reserved ones — the layer that actually closes DNS rebinding, because it
// judges the address the connection will really use, not the name it started
// from.
//
// The guard here runs earlier, on the URL string itself, at the one point the
// generic OpenAI lane knows where a request is about to go: ForwardOpenAI. The
// operator-supplied upstream overrides — the per-connection baseUrl in
// providerSpecificData and a custom provider node's BaseURL — flow into
// ProviderConfig.BaseURL and reach this check before the request is built, so
// the client gets a 403 that names the rule instead of a 502 that names a
// socket. The web-search and fetch lanes already ran their own URL checks
// (internal/handlers/media targetPrivate, the dashboard probes' AssertPublicURL
// before dispatch); the generic chat lane was the gap.
//
// Layering contract between the two halves:
//   - This guard is deliberately literal-only — scheme, host, embedded
//     credentials, and hosts/addresses internal or metadata by name or literal.
//     It never resolves a hostname: the HTTP request it runs ahead of pays for
//     its own DNS at dial time anyway, and a resolving check here would double
//     every lookup on the hot path (FASE 8 target: p50 < 5ms).
//   - Cloud instance-metadata endpoints are refused here by literal or static
//     name, unconditionally; the dialer refuses their addresses too (link-local,
//     CGNAT, unique-local), so the metadata carve-out survives either layer
//     alone. Residual: a plain hostname that merely RESOLVES to metadata space
//     is left to the dialer, and Azure WireServer (168.63.129.16) is public
//     address space the dialer cannot range-check — that name shape is only
//     caught when written as a literal.
//   - Private and loopback literals pass this guard only when allowLocal is
//     true, and then only reach the wire if the dialer agrees: loopback dials
//     need the WithAllowLoopback marker this package puts on the context, and
//     non-loopback private dials need ALLOW_PRIVATE_NETWORKING (which
//     dispatchAllowLocal also honors, so an operator of that env clears both
//     layers at once). A request that clears only one layer still fails closed.

// allowPrivateProviderURLsGlobal is the runtime switch for the operator
// opt-in. Default false.
var allowPrivateProviderURLsGlobal atomic.Bool

// SetAllowPrivateProviderURLs installs the setting's value. internal/app wires
// it from the settings row at startup, the same one-call pattern
// executor.SetWebExecutorsEnabled follows.
func SetAllowPrivateProviderURLs(v bool) {
	allowPrivateProviderURLsGlobal.Store(v)
}

// AllowPrivateProviderURLs reports whether dispatch to private or loopback
// provider endpoints is permitted. Default is false (OFF). The setting is the
// intended switch; ALLOW_PRIVATE_PROVIDER_URLS=true is the env fallback for
// deployments that configure everything through the environment.
func AllowPrivateProviderURLs() bool {
	if allowPrivateProviderURLsGlobal.Load() {
		return true
	}
	env := strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_PROVIDER_URLS")))
	return env == "true" || env == "1"
}

// allowPrivateURLsHint is appended to an internal-host refusal the operator's
// opt-in would have allowed, so a blocked local-setup operator (ollama on
// 127.0.0.1 behind a fresh install) reads the fix in the error instead of
// having to find the setting. Not appended to metadata refusals: the setting
// deliberately never excuses those.
const allowPrivateURLsHint = "Set allowPrivateProviderURLs (or the ALLOW_PRIVATE_PROVIDER_URLS=true environment variable) to permit loopback or private provider endpoints."

// localDispatchProviders are the provider ids whose endpoint is, by the
// registry's own definition or by the product's documented use, a service on
// the operator's machine or LAN. The registry flag (ProviderConfig.LocalByDesign)
// covers ollama-local; the rest of the set are the self-hosted lanes this
// gateway ships. Lanes that know the provider id should answer this before
// computing allowLocal; ForwardOpenAI, which sees only the config, uses
// cfg.LocalByDesign, and sdwebui/searxng/comfyui/tortoise dispatch through
// their own lanes, not the generic one.
var localDispatchProviders = map[string]bool{
	"ollama-local": true, // registry endpoint http://localhost:11434
	"sdwebui":      true, // registry endpoint http://localhost:7860
	"searxng":      true, // registry endpoint http://localhost:4000
	"comfyui":      true, // registry endpoint http://localhost:8188
	"tortoise":     true, // registry endpoint http://localhost:5000
	"lmstudio":     true, // documented self-hosted OpenAI-compatible server
	"vllm":         true, // documented self-hosted OpenAI-compatible server
}

// IsLocalDispatchProvider reports whether a provider id belongs to the
// self-hosted set above.
func IsLocalDispatchProvider(provider string) bool {
	return localDispatchProviders[provider]
}

// ErrDispatchBlocked is the sentinel every refusal of this guard wraps, so a
// caller can tell an SSRF refusal from a transport failure with errors.Is.
var ErrDispatchBlocked = errors.New("dispatch blocked by SSRF guard")

// AssertDispatchURLAllowed judges a raw upstream URL on the dispatch path.
// allowLocal admits private and loopback hosts (the operator's
// allowPrivateProviderURLs opt-in, a local-by-design provider, or a context
// that already authorized loopback); cloud instance-metadata endpoints are
// refused either way.
//
// The check is literal-only and never resolves a hostname: AssertPublicURLLiterals
// for the private-range literals and blocked names, IsCloudMetadataURLLiterals
// for the metadata endpoints. A hostname's resolved addresses are judged at
// connect time by the socket dialer's ControlContext (newSafeDialerForProxy),
// which is what closes DNS rebinding without a second lookup per request. The
// resolving AssertPublicURL stays exported for the non-hot-path callers
// (dashboard probes, webhooks, connect-url validation).
func AssertDispatchURLAllowed(rawURL string, allowLocal bool) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("%w: empty upstream URL", ErrDispatchBlocked)
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		// A URL that does not parse never reaches this guard's judgment: the
		// request builder fails it with the same parser moments later, and that
		// failure is the 502 the combo layer already relays parseably
		// (combo_error_body_test pins the shape). Refusing it here would swap
		// that pinned verdict for a 403 and add nothing — the request cannot go
		// out either way.
		return nil
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q (http or https only)", ErrDispatchBlocked, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: URL has no host", ErrDispatchBlocked)
	}
	// user:pass@host in a provider URL would put credentials on the wire and
	// in logs, and is never how an upstream endpoint is addressed.
	if parsed.User != nil {
		return fmt.Errorf("%w: URL carries embedded credentials", ErrDispatchBlocked)
	}
	// Metadata first, before allowLocal can excuse anything: 169.254.169.254
	// and friends are refused even for an opted-in operator, and Azure's
	// 168.63.129.16 lives in public address space, so no private-range check
	// would ever catch it. Literal forms only — the resolved-address tail is
	// the dialer's job (see the layering contract above).
	if handlerutil.IsCloudMetadataURLLiterals(trimmed) {
		return fmt.Errorf("%w: cloud instance metadata endpoint", ErrDispatchBlocked)
	}
	if !allowLocal {
		if err := handlerutil.AssertPublicURLLiterals(trimmed); err != nil {
			// A loopback or private literal is exactly what the operator's
			// opt-in exists to allow; name the switch so a local-setup
			// operator reads the fix in the refusal. Metadata never reaches
			// this branch, so the hint cannot promise something the setting
			// would not deliver.
			if strings.Contains(err.Error(), "internal host") {
				err = fmt.Errorf("%v %s", err, allowPrivateURLsHint)
			}
			return fmt.Errorf("%w: %v", ErrDispatchBlocked, err)
		}
	}
	return nil
}

// dispatchAllowLocal resolves the exemption for one ForwardOpenAI call: the
// operator opt-in, the global env switch, a local-by-design provider config,
// or a context that already carries the loopback marker.
func dispatchAllowLocal(ctx context.Context, cfg *providers.ProviderConfig) bool {
	if AllowPrivateProviderURLs() || IsPrivateNetworkingAllowed() {
		return true
	}
	if cfg != nil && cfg.LocalByDesign {
		return true
	}
	return IsLoopbackAllowed(ctx)
}

// dispatchBlockedBody renders the refusal the way the media lane's SSRF refusals
// look, so clients see one shape regardless of which lane refused them. %q
// quotes the message as a JSON string: the messages this guard produces are
// plain ASCII, which is the overlap of Go and JSON string syntax.
func dispatchBlockedBody(err error) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":"address_forbidden"}}`, err.Error()))
}

// applyDispatchGuard is the hook ForwardOpenAI calls before dispatching: it
// answers whether the request may go out and, when private targets are
// permitted, returns the context that lets the dialer honor that decision.
func applyDispatchGuard(ctx context.Context, cfg *providers.ProviderConfig) (context.Context, error) {
	allowLocal := dispatchAllowLocal(ctx, cfg)
	if err := AssertDispatchURLAllowed(cfg.BaseURL, allowLocal); err != nil {
		return ctx, &UpstreamError{
			StatusCode: http.StatusForbidden,
			Body:       dispatchBlockedBody(err),
			Header:     http.Header{},
		}
	}
	if allowLocal && !IsLoopbackAllowed(ctx) {
		// The URL cleared the guard, so the operator's opt-in (or the provider's
		// local-by-design flag) extends to the socket: without the marker the
		// dialer would refuse the loopback dial this same decision just
		// promised. The marker relaxes ONLY loopback — metadata and every other
		// private or reserved address stay refused at the dialer, and a
		// non-loopback private target additionally needs
		// ALLOW_PRIVATE_NETWORKING.
		ctx = WithAllowLoopback(ctx)
	}
	return ctx, nil
}
