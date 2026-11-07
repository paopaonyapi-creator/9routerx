// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package updater

import (
	"os"
	"strings"
	"sync"
)

// DistChannel names how this 9routerx build reached the machine it runs on.
// The update banner speaks a different sentence per channel — a binary gets a
// download link, an npm install gets the upgrade command, a source build gets
// neither and points at the release page — so the detection has to be decided
// once and reported verbatim (see docs/updater-contract.md).
type DistChannel string

const (
	// ChannelBinary is a packaged release binary (installer, go build with the
	// release ldflags). The default when nothing marks the build otherwise.
	ChannelBinary DistChannel = "binary"
	// ChannelNpm is an install through the npm distribution. The npm package's
	// bin wrapper exports 9ROUTERX_NPM_INSTALL=1 before exec'ing the same Go
	// binary, so the process can tell how it was launched.
	ChannelNpm DistChannel = "npm"
	// ChannelDev is a build straight from source with no version metadata:
	// `go run`, `go build` without the release ldflags, or a version string
	// stamped "-dev". Updates here are instructions, never auto-applied.
	ChannelDev DistChannel = "dev"
)

// npmInstallMarker is the env var the npm package's bin wrapper sets so the
// process can recognise an npm install. Documented in docs/updater-contract.md;
// Bagian B ships the wrapper that writes it.
const npmInstallMarker = "9ROUTERX_NPM_INSTALL"

// DetectDistChannel classifies this process. The decision is made once and
// cached: the executable path does not change under a running process.
var (
	channelOnce   sync.Once
	detectedChan  DistChannel
	detectedWhy   string
	executableFor = os.Executable
)

// DetectDistChannel returns the distribution channel of the running build plus
// the reason it was classified that way (surfaced by /api/update/debug).
//
// Heuristic, first match wins (documented in docs/updater-contract.md):
//  1. 9ROUTERX_NPM_INSTALL=1 in the environment — set by the npm bin wrapper
//     — means npm.
//  2. A CurrentVersion that is empty or stamped "-dev" means a source build
//     without version metadata, so dev.
//  3. An executable living inside the go toolchain's "go-build" temp tree
//     means `go run` (or `go test`), so dev.
//  4. Anything else is a packaged release binary.
func DetectDistChannel() (DistChannel, string) {
	channelOnce.Do(func() {
		detectedChan, detectedWhy = detectChannel(CurrentVersion, executableFor, npmMarkerSet())
	})
	return detectedChan, detectedWhy
}

// npmMarkerSet reads the npm wrapper marker exactly as the wrapper writes it.
func npmMarkerSet() bool {
	return os.Getenv(npmInstallMarker) == "1"
}

// detectChannel is the pure core of DetectDistChannel, split out so the matrix
// tests each rule without env or filesystem juggling.
func detectChannel(version string, execResolver func() (string, error), npmMarked bool) (DistChannel, string) {
	if npmMarked {
		return ChannelNpm, npmInstallMarker + "=1 (npm bin wrapper)"
	}
	v := strings.TrimSpace(version)
	if v == "" {
		return ChannelDev, "no version metadata in the build"
	}
	if strings.HasSuffix(v, "-dev") {
		return ChannelDev, "version carries the -dev suffix"
	}
	if execResolver != nil {
		if exe, err := execResolver(); err == nil && strings.Contains(exe, "go-build") {
			return ChannelDev, "running from the go toolchain build cache (go run)"
		}
	}
	return ChannelBinary, "packaged release binary (default)"
}
