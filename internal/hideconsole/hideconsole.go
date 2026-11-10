// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package hideconsole

import (
	"fmt"
	"os/exec"
	"strings"
)

// CombinedOutput runs name with args and returns the combined stdout+stderr as
// a string, mirroring exec.Cmd.CombinedOutput, with two differences: the child
// gets no visible console window on Windows (Apply), and the command is
// resolved with LookPath up front so a missing helper fails with a named error
// instead of an anonymous exec.Error at Start time.
//
// The command and its arguments travel as a separate argv list — no shell is
// spawned and nothing is ever concatenated into a command line — so there is
// no injection surface here by construction.
func CombinedOutput(name string, args ...string) (string, error) {
	// LookPath mirrors what exec.Command does before Start; doing it explicitly
	// lets the struct-literal Cmd below carry the resolved path.
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	cmd := &exec.Cmd{Path: path, Args: append([]string{name}, args...)}
	Apply(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
