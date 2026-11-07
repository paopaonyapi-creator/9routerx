//go:build !windows && !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package clientconfig

import "os"

// Platforms without a termios implementation here (plan9, js, wasip1, aix,
// solaris) report "no terminal" rather than guessing. Failing closed means the
// user is told to set NINE_ROUTER_PASSWORD instead of typing into a prompt that
// would echo.

func isTerminal(*os.File) bool { return false }

func readPasswordFromConsole(*os.File) ([]byte, error) {
	return nil, ErrNoTTY
}
