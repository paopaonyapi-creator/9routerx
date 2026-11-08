// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"9routerx/proxy/internal/daemon"
)

type LegacyKind string

const (
	LegacyTask     LegacyKind = "scheduled task"
	LegacyRegistry LegacyKind = "registry Run"
	LegacyFile     LegacyKind = "startup file"
	LegacyService  LegacyKind = "Windows service"
	LegacyPM2      LegacyKind = "pm2"
	LegacySystemd  LegacyKind = "systemd user"
	LegacyCron     LegacyKind = "cron @reboot"
	LegacyLaunchd  LegacyKind = "launchd"
)

type LegacyEntry struct {
	Kind     LegacyKind
	Name     string
	Command  string
	Path     string
	Snapshot string
}

var legacyCommand = func(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
var commandTokens = regexp.MustCompile(`"[^"]*"|'[^']*'|[^\s"']+`)

func normalizedLegacyPath(s string) string {
	s = strings.Trim(s, "\"'")
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimSuffix(s, "/")
	if goos == "windows" {
		s = strings.ToLower(s)
	}
	return s
}
func legacyPaths(p daemon.ProcessInfo) []string {
	var paths []string
	for _, token := range commandTokens.FindAllString(p.Executable+" "+p.CommandLine, -1) {
		token = normalizedLegacyPath(token)
		lower := strings.ToLower(token)
		absolute := strings.HasPrefix(token, "/") || (len(token) > 2 && token[1] == ':' && token[2] == '/')
		if absolute && strings.Contains(lower, "9router") && !strings.Contains(lower, "9routerx") {
			paths = append(paths, token)
		}
	}
	return paths
}

// MatchLegacyCommand never matches a generic node executable alone or any X entry.
func MatchLegacyCommand(command string, p daemon.ProcessInfo) bool {
	if strings.Contains(strings.ToLower(command), "9routerx") {
		return false
	}
	if strings.ContainsAny(strings.TrimSpace(command), "\r\n") {
		return false
	}
	tokens := commandTokens.FindAllString(command, -1)
	for i, token := range tokens {
		if token == "&" && i == len(tokens)-1 {
			continue
		}
		if !strings.HasPrefix(token, "\"") && !strings.HasPrefix(token, "'") && strings.ContainsAny(token, ";&|") {
			return false
		}
	}
	paths := legacyPaths(p)
	for _, token := range tokens {
		if slices.Contains(paths, normalizedLegacyPath(token)) {
			return true
		}
	}
	return false
}

// DiscoverLegacy is read-only. Failed probes are returned as warnings, never hidden.
func DiscoverLegacy(p daemon.ProcessInfo) ([]LegacyEntry, []string) {
	var entries []LegacyEntry
	var warnings []string
	if goos == "windows" {
		entries, warnings = discoverWindowsLegacy(p)
	} else {
		entries, warnings = discoverUnixLegacy()
	}
	apps, pmWarnings := legacyPM2Entries()
	entries = append(entries, apps...)
	warnings = append(warnings, pmWarnings...)
	var matching []LegacyEntry
	for _, entry := range entries {
		if MatchLegacyCommand(entry.Command, p) {
			matching = append(matching, entry)
		}
	}
	return matching, warnings
}
func legacyPM2Entries() ([]LegacyEntry, []string) {
	if lookPath("pm2") == "" {
		return nil, nil
	}
	out, err := legacyCommand("pm2", "jlist")
	if err != nil {
		return nil, []string{err.Error()}
	}
	var apps []struct {
		ID   *int   `json:"pm_id"`
		Name string `json:"name"`
		Env  struct {
			Path string `json:"pm_exec_path"`
		} `json:"pm2_env"`
	}
	if err := json.Unmarshal([]byte(out), &apps); err != nil {
		return nil, []string{"pm2 jlist: " + err.Error()}
	}
	var entries []LegacyEntry
	for _, app := range apps {
		if app.ID == nil || *app.ID < 0 || strings.Contains(strings.ToLower(app.Name), "9routerx") {
			continue
		}
		command := quoteArg(strings.ReplaceAll(app.Env.Path, "\\", "/"))
		entries = append(entries, LegacyEntry{Kind: LegacyPM2, Name: fmt.Sprint(*app.ID), Command: command, Snapshot: fmt.Sprintf("%d:%s:%s", *app.ID, app.Name, app.Env.Path)})
	}
	return entries, nil
}

// DisableLegacy re-reads before acting: stale scans and modified commands fail closed.
func DisableLegacy(e LegacyEntry, p daemon.ProcessInfo) error {
	if !MatchLegacyCommand(e.Command, p) {
		return fmt.Errorf("refusing unmatched autostart %s", e.Name)
	}
	if e.Kind == LegacyRegistry && strings.HasPrefix(e.Path, "HKLM\\") && !p.SystemScope {
		return fmt.Errorf("refusing HKLM write without proven system scope")
	}
	current, warnings := DiscoverLegacy(p)
	found := false
	for _, v := range current {
		if v.Kind == e.Kind && v.Name == e.Name && v.Path == e.Path && v.Command == e.Command && (v.Snapshot == e.Snapshot || e.Kind == LegacyCron) {
			e.Snapshot = v.Snapshot
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("autostart changed or cannot be verified (%s): %s", e.Name, strings.Join(warnings, "; "))
	}
	return disableLegacyEntry(e)
}

func disableLegacyEntry(e LegacyEntry) error {
	switch e.Kind {
	case LegacyTask:
		_, err := legacyCommand("schtasks.exe", "/change", "/tn", e.Name, "/disable")
		return err
	case LegacyRegistry:
		_, err := legacyCommand("reg.exe", "delete", e.Path, "/v", e.Name, "/f")
		return err
	case LegacyService:
		_, err := legacyCommand("sc.exe", "config", e.Name, "start=", "disabled")
		return err
	case LegacyPM2:
		if _, err := legacyCommand("pm2", "delete", e.Name); err != nil {
			return err
		}
		_, err := legacyCommand("pm2", "save")
		return err
	case LegacySystemd:
		_, err := legacyCommand("systemctl", "--user", "disable", "--now", e.Name)
		return err
	case LegacyLaunchd:
		if _, err := legacyCommand("launchctl", "unload", "-w", e.Path); err != nil {
			return err
		}
		return os.Rename(e.Path, e.Path+".9router-disabled")
	case LegacyFile:
		return os.Rename(e.Path, e.Path+".9router-disabled")
	case LegacyCron:
		return disableLegacyCron(e)
	}
	return fmt.Errorf("unsupported legacy entry %s", e.Kind)
}

func disableLegacyCron(e LegacyEntry) error {
	out, err := legacyCommand("crontab", "-l")
	if err != nil {
		return err
	}
	if out != e.Snapshot {
		return fmt.Errorf("crontab changed; rerun take-over")
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line == e.Command {
			lines[i] = "# disabled by 9routerx take-over: " + line
		}
	}
	return writeCrontab(strings.Join(lines, "\n"))
}
