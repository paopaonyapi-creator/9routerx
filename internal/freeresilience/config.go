// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package freeresilience

import (
	"os"
	"strconv"
	"strings"
)

// FallbackConfig is the free-provider fallback chain's configuration.
//
// It follows the env-var configuration pattern the rest of the gateway uses
// (DATA_DIR, RTK_ENABLED, ROUTERX_ENABLE_A2A): plain environment variables
// read at boot, with the same names in .env. No new settings schema — the
// chain ships ON, so the behaviour works with zero configuration.
type FallbackConfig struct {
	// Enabled turns the chain on. Default true: a free-tier refusal that
	// falls through to a healthy free provider is what the user asked the
	// router for when they picked a free model.
	Enabled bool
	// MaxAttempts caps how many free candidates one client request may walk
	// through. Default 3. The original provider's attempt is not counted —
	// it already happened before the chain started.
	MaxAttempts int
	// Order is the operator's preferred candidate order (provider ids or
	// aliases, most preferred first). Candidates not named here trail after
	// the named ones, ordered by health score. Empty means health order
	// throughout.
	Order []string
}

// DefaultFallbackConfig is the zero-configuration behaviour.
func DefaultFallbackConfig() FallbackConfig {
	return FallbackConfig{
		Enabled:     true,
		MaxAttempts: 3,
		Order:       nil,
	}
}

// FallbackConfigFromEnv reads the chain's configuration from the environment.
func FallbackConfigFromEnv() FallbackConfig {
	cfg := DefaultFallbackConfig()
	if v := strings.TrimSpace(os.Getenv("FALLBACK_FREE_ENABLED")); v != "" {
		cfg.Enabled = v != "false" && v != "0" && v != "no"
	}
	if v := strings.TrimSpace(os.Getenv("FALLBACK_FREE_MAX_ATTEMPTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxAttempts = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("FALLBACK_FREE_ORDER")); v != "" {
		for _, name := range strings.Split(v, ",") {
			if name = strings.TrimSpace(name); name != "" {
				cfg.Order = append(cfg.Order, name)
			}
		}
	}
	return cfg
}

// ClampMaxAttempts keeps the attempt budget inside a sane range regardless of
// what the environment said: at least one candidate, at most ten.
func (c FallbackConfig) ClampMaxAttempts() int {
	if c.MaxAttempts < 1 {
		return 1
	}
	if c.MaxAttempts > 10 {
		return 10
	}
	return c.MaxAttempts
}
