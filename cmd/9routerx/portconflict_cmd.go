// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"9routerx/proxy/internal/daemon"
)

func firstRunConflict(c *cli.Context, target string) (bool, error) {
	if _, err := os.Stat(daemon.PIDFile()); err == nil {
		return false, nil
	}
	conflict := daemon.DescribePortConflict(target)
	if !conflict.Busy {
		return false, nil
	}
	// The bare picker owns stdin through Bubble Tea; never start a second reader.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !directStartCommand(c) {
		return true, fmt.Errorf("%s", conflict.Error())
	}
	return resolveFirstConflict(c, conflict, bufio.NewReader(os.Stdin), os.Stdout)
}

func directStartCommand(c *cli.Context) bool {
	return c.Command != nil && c.Command.Name == "start"
}
func resolveFirstConflict(c *cli.Context, conflict daemon.PortConflict, in *bufio.Reader, out io.Writer) (bool, error) {
	fmt.Fprintln(out, conflict.String())
	fmt.Fprintf(out, "Choose:\n [1] Take over: stop the old gateway and migrate (service take-over)\n [2] Use another port instead (recommended default: %d) — run side by side\n [3] Abort\nChoice [3]: ", daemon.AlternativePort(conflict.Port))
	answer, err := in.ReadString('\n')
	if err != nil {
		return true, fmt.Errorf("port conflict: choice cancelled: %w", err)
	}
	switch strings.TrimSpace(answer) {
	case "1":
		return true, takeOver(c, migrationPrompts{terminal: true, in: in, out: out})
	case "2":
		port, err := freeAlternative(conflict.URL, daemon.AlternativePort(conflict.Port))
		if err != nil {
			return true, err
		}
		if err := os.Setenv("PORT", strconv.Itoa(port)); err != nil {
			return true, err
		}
		fmt.Fprintf(out, "Set PORT=%d (env) or `9routerx service install` to make it permanent.\nFor this specific port: 9routerx --port=%d service install\n", port, port)
		// Call the underlying start only once; do not re-enter the first-run menu.
		return true, startDetachedOn(c, daemonURL())
	default:
		return true, fmt.Errorf("start aborted; %s", conflict.Error())
	}
}
func freeAlternative(target string, start int) (int, error) {
	u, err := url.Parse(target)
	if err != nil {
		return 0, err
	}
	for port := start; port <= 65535 && port < start+100; port++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(u.Hostname(), strconv.Itoa(port)))
		if err == nil {
			ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free alternative port near %d; set PORT explicitly", start)
}
