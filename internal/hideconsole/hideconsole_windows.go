//go:build windows

// Package hideconsole stops helper commands started from a windowless process
// (the tray companion) from flashing a console window. On Windows a console
// child of a GUI parent allocates its own console, which pops up as a black
// window for the lifetime of the child — once per tray refresh tick, which the
// user sees as CMD windows blinking on screen every second.
package hideconsole

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Windows CREATE_NO_WINDOW process creation flag: the
// child gets a console for its stdio handles but no window is created for it.
// It beats HideWindow alone, which still lets the child flash before hiding.
const createNoWindow = 0x08000000

// Apply marks cmd so its child process gets no visible console window. Safe to
// call on every command: a parent that already owns a console is unaffected
// (the child simply shares it, as before).
func Apply(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
