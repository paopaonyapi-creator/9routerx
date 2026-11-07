//go:build !linux && !windows && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package proc

import "errors"

// No way to read another process's image path on this platform. Executable
// reports an error so callers treat the claim as unverified rather than
// treating an empty path as a match.
func executablePath(pid int) (string, error) {
	return "", errors.New("proc: reading another process's image path is not supported on this platform")
}