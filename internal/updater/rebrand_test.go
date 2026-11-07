// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"strings"
	"testing"
)

// TestRebrandUpdateChannelPointsAt9RouterX pins the R.1 rebrand: the default
// update channel must answer from the 9RouterX repository, never from the
// upstream fork's. A build that still points at luqman-v1/9routerx would
// offer every user the ORIGINAL author's binary on `9routerx update` —
// silently overwriting this codebase with someone else's release.
func TestRebrandUpdateChannelPointsAt9RouterX(t *testing.T) {
	if !strings.Contains(DefaultUpdateURL, "yanstd888/9routerx") {
		t.Errorf("DefaultUpdateURL = %q, must point at yanstd888/9routerx", DefaultUpdateURL)
	}
	if strings.Contains(DefaultUpdateURL, "luqman-v1") || strings.Contains(DefaultUpdateURL, "9router"+"-go") {
		t.Errorf("DefaultUpdateURL still references the old repo: %q", DefaultUpdateURL)
	}
	if DefaultGitHubRepo != "yanstd888/9routerx" {
		t.Errorf("DefaultGitHubRepo = %q, want yanstd888/9routerx", DefaultGitHubRepo)
	}
}
