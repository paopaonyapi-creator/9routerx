// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"9routerx/proxy/internal/config"
	"9routerx/proxy/internal/daemon"
	"9routerx/proxy/internal/service"
)

type migrationPrompts struct {
	yes      bool
	terminal bool
	in       *bufio.Reader
	out      io.Writer
}

func (p migrationPrompts) confirm(question string) error {
	fmt.Fprintln(p.out, question)
	if p.yes {
		fmt.Fprintln(p.out, "  --yes: confirmed")
		return nil
	}
	if !p.terminal {
		return fmt.Errorf("confirmation requires a terminal; inspect the report and rerun with --yes to approve")
	}
	fmt.Fprint(p.out, "Continue? [y/N] ")
	text, err := p.in.ReadString('\n')
	if err != nil {
		return fmt.Errorf("confirmation cancelled: %w", err)
	}
	if answer := strings.ToLower(strings.TrimSpace(text)); answer != "y" && answer != "yes" {
		return fmt.Errorf("cancelled; no further actions taken")
	}
	return nil
}
func serviceTakeOver(c *cli.Context) error {
	p := migrationPrompts{yes: c.Bool("yes"), terminal: term.IsTerminal(int(os.Stdin.Fd())), in: bufio.NewReader(os.Stdin), out: os.Stdout}
	return takeOver(c, p)
}
func takeOver(c *cli.Context, p migrationPrompts) error {
	if err := config.ValidateEnvFile(".env"); err != nil {
		return err
	}
	cfg := config.LoadConfig()
	if err := cfg.Validate(); err != nil {
		return err
	}
	target := daemonURL()
	old := daemon.DescribePortConflict(target)
	stopped := "none (port was free)"
	if old.Busy {
		if err := p.stopGateway(old); err != nil {
			return err
		}
		stopped = old.String()
		fmt.Fprintln(p.out, "Stopped:", stopped)
	}
	disabled, err := p.disableLegacy(old)
	if err != nil {
		return err
	}
	if busy := daemon.DescribePortConflict(target); busy.Busy {
		return fmt.Errorf("gateway respawned; disable its parent and rerun: %s", busy.String())
	}
	info, err := p.installGateway(c, cfg, target)
	if err != nil {
		return err
	}
	fmt.Fprintln(p.out, "Health: 200; GET / fingerprint: 9RouterX")
	printMigrationData(p.out, cfg.DatabasePath)
	if len(disabled) == 0 {
		disabled = []string{"none"}
	}
	fmt.Fprintf(p.out, "\nTake-over complete\n  Stopped: %s\n  Disabled autostart: %s\n  Service backend: %s\n  Port: %d\n  DATA_DIR: %s\n  Database: %s\n  Dashboard: %s\n  Login: use your existing dashboard password (or INITIAL_PASSWORD for a new database).\n", stopped, strings.Join(disabled, ", "), info.Backend, cfg.Port, config.ResolveDataDir(), cfg.DatabasePath, target)
	return nil
}
func (p migrationPrompts) stopGateway(old daemon.PortConflict) error {
	fmt.Fprintln(p.out, old.String())
	for _, warning := range old.Diagnostics {
		fmt.Fprintln(p.out, "Inspection warning:", warning)
	}
	if err := p.confirm("Stop the process occupying the configured gateway port?"); err != nil {
		return err
	}
	if old.Kind == daemon.GatewayLegacy {
		fmt.Fprintln(p.out, "Skipping upstream's broad HTTP shutdown helper; requesting a stop of the verified listener PID only.")
	}
	if err := daemon.RequestPortStop(old); err != nil {
		fmt.Fprintln(p.out, "Graceful stop:", err)
	}
	if err := daemon.WaitPortFree(old.URL); err == nil {
		return nil
	}
	if err := p.confirm("WARNING: graceful stop failed. Force-kill this exact process? Open requests may be lost."); err != nil {
		return err
	}
	if err := daemon.ForcePortStop(old); err != nil {
		return err
	}
	return daemon.WaitPortFree(old.URL)
}
func (p migrationPrompts) disableLegacy(old daemon.PortConflict) ([]string, error) {
	var disabled []string
	if !old.Busy || old.Kind != daemon.GatewayLegacy {
		return disabled, nil
	}
	entries, warnings := service.DiscoverLegacy(old.Process)
	for _, warning := range warnings {
		fmt.Fprintln(p.out, "Autostart inspection warning:", warning)
	}
	for _, entry := range entries {
		fmt.Fprintf(p.out, "Legacy autostart: %s %s\n  Command: %s\n", entry.Kind, entry.Name, entry.Command)
	}
	if len(entries) == 0 {
		fmt.Fprintf(p.out, "No matching legacy autostart found. Command line: %s; parent PID: %d. The parent may respawn it. Disable that parent and rerun take-over, or keep legacy autostart and run X side by side with PORT=%d.\n", old.Process.CommandLine, old.Process.ParentPID, daemon.AlternativePort(old.Port))
		if err := p.confirm("Continue on this port despite the unresolved legacy autostart? (Cancel to disable the parent or choose another port.)"); err != nil {
			return disabled, err
		}
	}
	for _, entry := range entries {
		warning := fmt.Sprintf("Disable matching %s %s?", entry.Kind, entry.Name)
		if entry.Kind == service.LegacyRegistry {
			warning = "WARNING: this removes a registry Run value. " + warning
		}
		if err := p.confirm(warning); err != nil {
			return disabled, err
		}
		if err := service.DisableLegacy(entry, old.Process); err != nil {
			return disabled, err
		}
		action := string(entry.Kind) + " " + entry.Name
		disabled = append(disabled, action)
		fmt.Fprintln(p.out, "Disabled:", action)
	}
	return disabled, nil
}
func (p migrationPrompts) installGateway(c *cli.Context, cfg *config.Config, target string) (service.Info, error) {
	var info service.Info
	if err := p.confirm(fmt.Sprintf("Install/start 9RouterX using %s on port %d, DATA_DIR=%s?", service.Detect(), cfg.Port, config.ResolveDataDir())); err != nil {
		return info, err
	}
	// Persist effective configuration in the same backend's argv, not the launching shell.
	args, err := serviceArgs(c)
	if err != nil {
		return info, err
	}
	info, err = service.Install(args)
	if err != nil {
		return info, err
	}
	fmt.Fprintf(p.out, "Autostart installed via %s: %s\n", info.Backend, info.Path)
	if !info.Active {
		_, err = service.Start()
		if errors.Is(err, service.ErrNoSupervisor) {
			_, err = daemon.Start(target, forwardArgs(c))
		}
		if err != nil {
			return info, err
		}
	}
	if err := daemon.WaitHealthy(target); err != nil {
		return info, err
	}
	return info, nil
}
func printMigrationData(out io.Writer, path string) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintln(out, "Database warning:", err)
		return
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath, RawQuery: "mode=ro"}
	conn, err := sql.Open("sqlite", uri.String())
	if err != nil {
		fmt.Fprintln(out, "Database warning:", err)
		return
	}
	defer conn.Close()
	for _, table := range []string{"providerConnections", "apiKeys"} {
		var count int
		err := conn.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&count)
		if err != nil {
			fmt.Fprintf(out, "Database warning (%s): %v; location checked: %s\n", table, err, path)
			continue
		}
		fmt.Fprintf(out, "  %s: %d\n", table, count)
		if count == 0 {
			fmt.Fprintf(out, "WARNING: DATA_DIR may differ; location checked: %s\n", path)
		}
	}
}
