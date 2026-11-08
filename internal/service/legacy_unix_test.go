// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"9routerx/proxy/internal/daemon"
)

func TestLinuxLegacyMigrationPreservesForeignEntries(t *testing.T) {
	stubHost(t, hostStub{goos: "linux", bins: map[string]string{"systemctl": "systemctl", "crontab": "crontab", "pm2": "pm2"}})
	home := setHome(t)
	desktop := filepath.Join(home, ".config", "autostart", "old.desktop")
	foreign := filepath.Join(home, ".config", "autostart", "foreign.desktop")
	writeFile(t, desktop, "[Desktop Entry]\nExec=/usr/bin/node /opt/9router/server.js\n")
	writeFile(t, foreign, "[Desktop Entry]\nExec=/usr/bin/node /opt/foreign/server.js\n")
	cron := stubCrontab(t, "@reboot /usr/bin/node /opt/9router/server.js --one\n@reboot /usr/bin/node /opt/9router/server.js --two\n@reboot /usr/bin/node /opt/foreign/server.js\n0 * * * * /usr/bin/backup\n")
	original := legacyCommand
	t.Cleanup(func() { legacyCommand = original })
	var mutations []string
	deleted := false
	saved := false
	polls := 0
	legacyCommand = func(name string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		switch name {
		case "systemctl":
			if slices.Contains(args, "list-unit-files") {
				return "9router.service enabled\nforeign-router.service enabled\n9routerx.service enabled\n", nil
			}
			if slices.Contains(args, "show") {
				script := "/opt/foreign/server.js"
				if slices.Contains(args, "9router.service") {
					script = "/opt/9router/server.js"
				}
				if slices.Contains(args, "9routerx.service") {
					script = "/opt/9routerx/server.js"
				}
				return "{ path=/usr/bin/node ; argv[]=/usr/bin/node " + script + " ; }", nil
			}
			mutations = append(mutations, name+" "+cmd)
			return "", nil
		case "crontab":
			return *cron, nil
		case "pm2":
			if len(args) == 1 && args[0] == "jlist" {
				polls++
				if deleted {
					return "[]", nil
				}
				return fmt.Sprintf(`[{"pm_id":7,"name":"old-router","pm2_env":{"pm_exec_path":"/opt/9router/server.js"},"monit":{"cpu":%d}},{"pm_id":8,"name":"foreign","pm2_env":{"pm_exec_path":"/opt/foreign/server.js"}}]`, polls), nil
			}
			if cmd == "delete 7" {
				deleted = true
				mutations = append(mutations, "pm2 delete 7")
				return "", nil
			}
			if cmd == "save" {
				saved = true
				return "", nil
			}
		}
		return "", fmt.Errorf("unexpected command %s %s", name, cmd)
	}
	p := daemon.ProcessInfo{Executable: "/usr/bin/node", CommandLine: "/usr/bin/node /opt/9router/server.js"}
	entries, warnings := DiscoverLegacy(p)
	if len(warnings) > 0 || len(entries) != 5 {
		t.Fatalf("entries=%+v warnings=%v", entries, warnings)
	}
	if len(mutations) > 0 || deleted {
		t.Fatal("discovery wrote state")
	}
	for _, entry := range entries {
		if err := DisableLegacy(entry, p); err != nil {
			t.Fatalf("%s: %v", entry.Kind, err)
		}
	}
	if fileExists(desktop) || !fileExists(desktop+".9router-disabled") || !fileExists(foreign) {
		t.Fatal("startup file isolation failed")
	}
	if !strings.Contains(*cron, "@reboot /usr/bin/node /opt/foreign/server.js\n0 * * * * /usr/bin/backup\n") {
		t.Fatal("unrelated cron content changed", *cron)
	}
	if strings.Count(*cron, "# disabled by 9routerx take-over:") != 2 {
		t.Fatal("both matching cron entries must be disabled", *cron)
	}
	if !deleted || !saved || !slices.Contains(mutations, "systemctl --user disable --now 9router.service") {
		t.Fatal(mutations, deleted, saved)
	}
}
func TestMacLegacyLaunchAgentMigration(t *testing.T) {
	stubHost(t, hostStub{goos: "darwin"})
	home := setHome(t)
	path := filepath.Join(home, "Library", "LaunchAgents", "old.plist")
	writeFile(t, path, "binary plist fixture")
	original := legacyCommand
	t.Cleanup(func() { legacyCommand = original })
	unloaded := false
	legacyCommand = func(name string, args ...string) (string, error) {
		if name == "plutil" {
			return `{"ProgramArguments":["/usr/bin/node","/opt/9router/server.js"]}`, nil
		}
		if name == "launchctl" && slices.Equal(args, []string{"unload", "-w", path}) {
			unloaded = true
			return "", nil
		}
		return "", fmt.Errorf("unexpected %s %v", name, args)
	}
	p := daemon.ProcessInfo{CommandLine: "/usr/bin/node /opt/9router/server.js"}
	entries, warnings := DiscoverLegacy(p)
	if len(warnings) > 0 || len(entries) != 1 || unloaded {
		t.Fatalf("%v %v", entries, warnings)
	}
	if err := DisableLegacy(entries[0], p); err != nil {
		t.Fatal(err)
	}
	if !unloaded {
		t.Fatal("agent not unloaded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("plist still enabled")
	}
	if !fileExists(path + ".9router-disabled") {
		t.Fatal("backup missing")
	}
}
func TestCronConfigurationFlagsPrecedeStart(t *testing.T) {
	args := []string{"--port=20129", "--data-dir=/tmp/router-data", "--service-process"}
	for _, rendered := range []string{renderCronLine("/bin/9routerx", args), renderAutostartDesktop("/bin/9routerx", args)} {
		if !strings.Contains(rendered, "--service-process start") {
			t.Fatal("global flags would be rejected after start:", rendered)
		}
	}
}
