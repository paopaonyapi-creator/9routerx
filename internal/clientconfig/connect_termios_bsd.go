//go:build darwin || freebsd || netbsd || openbsd || dragonfly

// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package clientconfig

import "golang.org/x/sys/unix"

// The BSD family (macOS included) spells the termios ioctl TIOCGETA/TIOCSETA
// rather than Linux's TCGETS/TCSETS. Same operation, different numbers.
const (
	termiosGetReq = unix.TIOCGETA
	termiosSetReq = unix.TIOCSETA
)
