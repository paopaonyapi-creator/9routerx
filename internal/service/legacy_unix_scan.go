// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func discoverUnixLegacy() ([]LegacyEntry, []string) {
	home, err := userHomeDir()
	if err != nil {
		return nil, []string{err.Error()}
	}
	entries, warnings := legacyStartupFiles(home)
	if goos == "darwin" {
		return entries, warnings
	}
	units, unitWarnings := legacySystemdUnits()
	cron, cronWarnings := legacyCronEntries()
	entries = append(entries, units...)
	entries = append(entries, cron...)
	warnings = append(warnings, unitWarnings...)
	warnings = append(warnings, cronWarnings...)
	return entries, warnings
}
func legacyStartupFiles(home string) ([]LegacyEntry, []string) {
	pattern := filepath.Join(home, ".config", "autostart", "*.desktop")
	kind := LegacyFile
	if goos == "darwin" {
		pattern = filepath.Join(home, "Library", "LaunchAgents", "*.plist")
		kind = LegacyLaunchd
	}
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var entries []LegacyEntry
	var warnings []string
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		command, err := legacyStartupCommand(path, string(b), kind)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		entries = append(entries, LegacyEntry{Kind: kind, Name: filepath.Base(path), Path: path, Command: command, Snapshot: string(b)})
	}
	return entries, warnings
}
func legacyStartupCommand(path, text string, kind LegacyKind) (string, error) {
	if kind == LegacyFile {
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "Exec=") {
				return strings.TrimPrefix(line, "Exec="), nil
			}
		}
		return "", nil
	}
	out, err := legacyCommand("plutil", "-convert", "json", "-o", "-", path)
	if err != nil {
		return "", err
	}
	var plist struct {
		Program          string
		ProgramArguments []string
	}
	if err := json.Unmarshal([]byte(out), &plist); err != nil {
		return "", err
	}
	return commandLine(plist.Program, plist.ProgramArguments), nil
}
func legacySystemdUnits() ([]LegacyEntry, []string) {
	if lookPath("systemctl") == "" {
		return nil, nil
	}
	out, err := legacyCommand("systemctl", "--user", "list-unit-files", "--no-legend", "--no-pager")
	if err != nil {
		return nil, []string{err.Error()}
	}
	var entries []LegacyEntry
	var warnings []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(strings.ToLower(fields[0]), "router") || !strings.HasSuffix(fields[0], ".service") {
			continue
		}
		cmd, err := legacyCommand("systemctl", "--user", "show", fields[0], "--property=ExecStart", "--value")
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		// Strip systemctl's structured ExecStart wrappers, not substrings of a path.
		cmd = strings.NewReplacer("path=", "", "argv[]=", "", ";", " ", "{", " ", "}", " ").Replace(cmd)
		entries = append(entries, LegacyEntry{Kind: LegacySystemd, Name: fields[0], Command: cmd, Snapshot: cmd})
	}
	return entries, warnings
}
func legacyCronEntries() ([]LegacyEntry, []string) {
	if lookPath("crontab") == "" {
		return nil, nil
	}
	out, err := legacyCommand("crontab", "-l")
	if err != nil {
		return nil, []string{err.Error()}
	}
	var entries []LegacyEntry
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "@reboot" {
			entries = append(entries, LegacyEntry{Kind: LegacyCron, Name: "@reboot", Command: line, Snapshot: out})
		}
	}
	return entries, nil
}
