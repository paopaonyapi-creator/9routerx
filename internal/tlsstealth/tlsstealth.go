// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
// Package tlsstealth presents browser TLS fingerprints (JA3/JA4) for the
// web-executor lane and pairs the capability with a mandatory alert system.
//
// Why the alerts are not optional: a browser fingerprint is a request
// reshaping feature whose acceptability depends on the operator's standing
// with the upstream provider. docs/security/STEALTH_GUIDE.md frames the
// capability as a compatibility layer between user-owned official accounts
// and this router — not a way to evade provider Terms of Service, fraud
// detection, or credential sharing. An operator who turns the capability on
// must be told, every day it stays on, that this is what it is; an enabled
// stealth flag without a running alert is a compliance hole, so the loop and
// the flag are one unit.
//
// The package holds no wiring of its own: no config read, no route mount, no
// executor edit. Central wiring owns the flag (ROUTERX_TLS_STEALTH), the
// dashboard banner, and the webhook fan-out, and reaches in through the
// setters on Default and the NotifyFunc callback. That callback is also why
// this package imports neither webhooks nor handlers: the seam is a
// function, so neither side can grow a dependency on the other.
package tlsstealth

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogdanfinn/tls-client/profiles"
)

// DefaultProfileName is the profile a freshly enabled controller presents.
//
// chrome_146 on Linux is the profile the stealth guide's web-cookie lane
// carries the most traffic on (Claude, Grok), so it is the least surprising
// default for the same lane here. An operator who wants a different one sets
// it before enabling.
const DefaultProfileName = "chrome_146/linux"

// Profile is one catalog entry: what the operator selects, and the
// ClientHello the pinned tls-client actually sends for it.
type Profile struct {
	// Name is the selector, "<browser>_<version>/<os>". It names the intent,
	// not necessarily the exact handshake (see ClientHello).
	Name string
	// Browser and OS are the plain words the catalog is listed by.
	Browser string
	OS      string
	// ClientHello is the ClientHello identifier the pinned library sends,
	// read from the profile at init. It is kept separate from Name because
	// the two can disagree — the pin has no Firefox 151 handshake — and the
	// dashboard must not claim a fingerprint the stack does not send.
	ClientHello string

	// lib is the key in profiles.MappedTLSClients this entry maps onto.
	lib string
	// clientProfile is the resolved tls-client profile. Unexported: the
	// catalog is read-only after init.
	clientProfile profiles.ClientProfile
	// shuffleExtensions randomises the TLS extension order per connection.
	// Chrome shuffles; a fixed order is itself a fingerprint. Firefox does
	// not, and randomising a Firefox handshake would misrepresent it, so the
	// flag follows the browser, not a global default.
	shuffleExtensions bool
}

// profileCatalog is the closed set the dashboard lists and SetProfile
// validates against. Additive only: removing an entry orphans whatever an
// operator configured it as.
var profileCatalog = []Profile{
	{Name: "chrome_124/macos", Browser: "chrome", OS: "macos", lib: "chrome_124", shuffleExtensions: true},
	{Name: "chrome_146/linux", Browser: "chrome", OS: "linux", lib: "chrome_146", shuffleExtensions: true},
	{Name: "chrome_146/windows", Browser: "chrome", OS: "windows", lib: "chrome_146", shuffleExtensions: true},
	// tls-client v1.16.0 pins Firefox at 148; a 151 ClientHello is not in the
	// pin. The entry keeps the firefox_151/macos name so the operator's
	// selection matches the guide's vocabulary, and Profile.ClientHello
	// reports firefox_148 so what is on the wire stays honest. When the pin
	// grows a 151 handshake, swap the lib key and nothing else changes.
	{Name: "firefox_151/macos", Browser: "firefox", OS: "macos", lib: "firefox_148", shuffleExtensions: false},
}

// profilesByName is the resolved catalog, built once: a pinned dependency
// that drops a profile we map onto is a boot-visible contract change, and
// failing loudly beats dialing with Go's own handshake under a browser's name.
var profilesByName = func() map[string]Profile {
	resolved := make(map[string]Profile, len(profileCatalog))
	for _, p := range profileCatalog {
		lib, ok := profiles.MappedTLSClients[p.lib]
		if !ok {
			panic("tlsstealth: tls-client pin has no profile " + p.lib)
		}
		p.clientProfile = lib
		p.ClientHello = lib.GetClientHelloStr()
		resolved[p.Name] = p
	}
	return resolved
}()

// profileNames lists the catalog names in catalog order, for error messages.
func profileNames() []string {
	names := make([]string, 0, len(profileCatalog))
	for _, p := range profileCatalog {
		names = append(names, p.Name)
	}
	return names
}

// Profiles returns the resolved catalog for the dashboard's profile picker,
// in catalog order. A copy: the caller can sort or filter it without
// reordering the package's view.
func Profiles() []Profile {
	out := make([]Profile, 0, len(profileCatalog))
	for _, p := range profileCatalog {
		if resolved, ok := profilesByName[p.Name]; ok {
			out = append(out, resolved)
			continue
		}
		out = append(out, p)
	}
	return out
}

// Status is the dashboard banner's payload. The banner is permanent while
// Enabled is true: the operator must never have to wonder whether requests
// are being reshaped, which is why LegalNotice travels with the status
// rather than living only in the docs.
type Status struct {
	Enabled          bool   `json:"enabled"`
	ActiveProfile    string `json:"activeProfile"`
	AlertLoopRunning bool   `json:"alertLoopRunning"`
	LegalNotice      string `json:"legalNotice"`
}

// Stealth is the controller: the flag, the active profile, and the alert
// loop's running state. The zero value is not usable — construct with New.
type Stealth struct {
	mu sync.RWMutex
	// enabled is the flag central wiring sets from ROUTERX_TLS_STEALTH.
	// Default off: reshaping requests is an operator decision, never a
	// side effect of installing the router.
	enabled bool
	profile string
	// alertLoopRunning mirrors whether StartAlertLoop's goroutine is alive.
	// A separate atomic because the loop reads its own liveness from the
	// goroutine's deferred store, not from under mu.
	alertLoopRunning atomic.Bool
	clock            func() time.Time
}

// New returns a controller: flag off, default profile, wall clock.
func New() *Stealth {
	return &Stealth{profile: DefaultProfileName, clock: time.Now}
}

// Default is the process-wide controller central wiring configures. The
// package-level helpers below are shorthands for it.
var Default = New()

// SetEnabled turns the flag on or off on Default. Empty-profile defense: an
// on-switch with no profile would present Go's own handshake under a browser
// banner, so a controller that somehow lost its profile falls back to the
// default rather than enabling into a lie.
func SetEnabled(on bool) { Default.SetEnabled(on) }

// Enabled reports the flag on Default.
func Enabled() bool { return Default.Enabled() }

// SetProfile selects the active profile on Default.
func SetProfile(name string) error { return Default.SetProfile(name) }

// ActiveProfile returns the profile in use on Default, or "" while off.
func ActiveProfile() string { return Default.ActiveProfile() }

// SetEnabled turns the flag on or off. Wiring calls this from the
// ROUTERX_TLS_STEALTH handler; nothing inside this package reads the
// environment, so tests and embedding processes drive it directly.
func (s *Stealth) SetEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if on && strings.TrimSpace(s.profile) == "" {
		s.profile = DefaultProfileName
	}
	s.enabled = on
}

// Enabled reports whether the flag is on. The flag gates the web-executor
// wiring and the alert loop, not Transport: impersonation is opt-in per
// call there.
func (s *Stealth) Enabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled
}

// SetProfile selects the profile the web-executor lane presents. Unknown
// names are refused rather than silently falling back: an operator who
// typed firefox_151/windows meant exactly that, and a silent swap to
// something else is a fingerprint that contradicts the dashboard.
func (s *Stealth) SetProfile(name string) error {
	p, ok := profilesByName[name]
	if !ok {
		return fmt.Errorf("tlsstealth: unknown profile %q (want one of %s)",
			name, strings.Join(profileNames(), ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profile = p.Name
	return nil
}

// ActiveProfile returns the profile in use, or "" while the flag is off.
// "Active" means in use: a disabled controller presents nothing, and a
// banner built from this field must not advertise a fingerprint that is not
// being sent.
func (s *Stealth) ActiveProfile() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.enabled {
		return ""
	}
	return s.profile
}

// Status returns the banner payload. AlertLoopRunning is reported even while
// disabled, so wiring can notice a loop it started before the flag was
// turned off (the loop keeps running and simply stays silent until re-enabled).
func (s *Stealth) Status() Status {
	return Status{
		Enabled:          s.Enabled(),
		ActiveProfile:    s.ActiveProfile(),
		AlertLoopRunning: s.alertLoopRunning.Load(),
		LegalNotice:      LegalNotice,
	}
}

// WebExecutorClient returns the client the web executors (deepseek-web,
// gemini-web, chatgpt-web) should dial with while the flag is on: the active
// profile's transport. While off it returns (nil, nil) — the caller keeps its
// existing client, which is the whole point of the seam: wiring can call this
// unconditionally and the executors never learn stealth exists.
func (s *Stealth) WebExecutorClient() (*http.Client, error) {
	if !s.Enabled() {
		return nil, nil
	}
	s.mu.RLock()
	profile := s.profile
	s.mu.RUnlock()
	return Transport(profile)
}
