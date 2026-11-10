//go:build !windows

// Package hideconsole stops helper commands started from a windowless process
// (the tray companion) from flashing a console window. Non-Windows platforms
// have no console-window flash to prevent, so Apply is a no-op — the seam
// exists so callers do not need per-platform build tags of their own.
package hideconsole

import "os/exec"

// Apply marks cmd so its child process gets no visible console window. On
// platforms other than Windows there is nothing to do.
func Apply(cmd *exec.Cmd) {
	_ = cmd
}
