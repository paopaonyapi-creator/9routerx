//go:build linux

// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package clientconfig

import "golang.org/x/sys/unix"

// Linux answers termios through the generic TCGETS/TCSETS pair. The BSDs use
// the older TIOC* names for the same operation; the constants cannot be shared
// because their values differ.
const (
	termiosGetReq = unix.TCGETS
	termiosSetReq = unix.TCSETS
)
