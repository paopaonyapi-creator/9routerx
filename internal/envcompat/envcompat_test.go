// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package envcompat

import (
	"os"
	"testing"
)

// TestGetenvNewNameWins pins the R.4 compat contract: the 9ROUTERX_ spelling
// is authoritative, the pre-rename spelling answers only when the new one is
// unset, and neither the presence of the old name nor its precedence order
// among the arguments changes which value wins.
func TestGetenvNewNameWins(t *testing.T) {
	t.Setenv("9ROUTERX_URL", "https://new.example")
	t.Setenv("NINEROUTER_URL", "https://old.example")
	if got := Getenv("9ROUTERX_URL", "NINEROUTER_URL"); got != "https://new.example" {
		t.Errorf("Getenv = %q, want the new name to win", got)
	}

	t.Setenv("9ROUTERX_URL", "")
	if got := Getenv("9ROUTERX_URL", "NINEROUTER_URL"); got != "https://old.example" {
		t.Errorf("Getenv = %q, want the legacy fallback to answer", got)
	}

	t.Setenv("NINEROUTER_URL", "")
	if got := Getenv("9ROUTERX_URL", "NINEROUTER_URL"); got != "" {
		t.Errorf("Getenv = %q, want empty when both unset", got)
	}
}

// TestGetenvEmptyNameSkipped covers the conditional list building: an empty
// slot must be skipped, not returned as a found-but-empty value.
func TestGetenvEmptyNameSkipped(t *testing.T) {
	t.Setenv("NINEROUTER_PEER_TOKEN", "tok")
	if got := Getenv("9ROUTERX_PEER_TOKEN", "", "NINEROUTER_PEER_TOKEN"); got != "tok" {
		t.Errorf("Getenv = %q, want legacy value through the empty slot", got)
	}
}

// TestGetenvNoWarningWhenCurrentSet: the deprecation warning exists to move
// operators off the old name; it must stay silent while the new name answers.
func TestGetenvNoWarningWhenCurrentSet(t *testing.T) {
	loggedMu.Lock()
	logged = map[string]bool{}
	loggedMu.Unlock()

	t.Setenv("9ROUTERX_ROUTE_HEADERS", "1")
	t.Setenv("NINEROUTER_ROUTE_HEADERS", "0")
	_ = Getenv("9ROUTERX_ROUTE_HEADERS", "NINEROUTER_ROUTE_HEADERS")

	loggedMu.Lock()
	_, warned := logged["9ROUTERX_ROUTE_HEADERS\x00NINEROUTER_ROUTE_HEADERS"]
	loggedMu.Unlock()
	if warned {
		t.Error("deprecation warning fired while the current name was set")
	}
}

// TestIsDeprecatedSet pins the doctor-check shape: true only for the exact
// "new unset, old set" combination.
func TestIsDeprecatedSet(t *testing.T) {
	os.Unsetenv("9ROUTERX_HEADROOM_LIVE")
	os.Unsetenv("NINEROUTER_HEADROOM_LIVE")
	t.Cleanup(func() {
		os.Unsetenv("9ROUTERX_HEADROOM_LIVE")
		os.Unsetenv("NINEROUTER_HEADROOM_LIVE")
	})

	if IsDeprecatedSet("9ROUTERX_HEADROOM_LIVE", "NINEROUTER_HEADROOM_LIVE") {
		t.Error("deprecated report with both unset")
	}
	t.Setenv("NINEROUTER_HEADROOM_LIVE", "1")
	if !IsDeprecatedSet("9ROUTERX_HEADROOM_LIVE", "NINEROUTER_HEADROOM_LIVE") {
		t.Error("legacy-only set must report deprecated")
	}
	t.Setenv("9ROUTERX_HEADROOM_LIVE", "1")
	if IsDeprecatedSet("9ROUTERX_HEADROOM_LIVE", "NINEROUTER_HEADROOM_LIVE") {
		t.Error("current name set must not report deprecated")
	}
}
