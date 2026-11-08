// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package service

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"9routerx/proxy/internal/daemon"
)

func TestLegacyPathMatching(t *testing.T) {
	p := daemon.ProcessInfo{Executable: "C:/node/node.exe", CommandLine: `"C:/node/node.exe" "C:/apps/9router/server.js"`}
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{`"C:/node/node.exe" "C:/apps/9router/server.js"`, true},
		{`"C:\node\node.exe" "C:\apps\9router\server.js"`, true},
		{`C:/node/node.exe C:/other/app.js`, false},
		{`C:/node/node.exe`, false},
		{`C:/apps/9routerx/server.js`, false},
		{`C:/apps/9router/server.js.bak`, false},
		{`C:/node/node.exe C:/apps/9router/server.js && C:/foreign.exe`, false},
		{`C:/node/node.exe C:/apps/9router/server.js &`, true},
		{"C:/node/node.exe C:/apps/9router/server.js\nC:/foreign.exe", false},
		{`C:/apps/9router/server.js --tag=9routerx`, false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := MatchLegacyCommand(tc.command, p); got != tc.want {
				t.Fatalf("match=%v, want %v", got, tc.want)
			}
		})
	}
}
func TestLegacyUnknownPathRefusesAction(t *testing.T) {
	before := legacyCommand
	defer func() { legacyCommand = before }()
	called := false
	legacyCommand = func(string, ...string) (string, error) { called = true; return "", nil }
	err := DisableLegacy(LegacyEntry{Kind: LegacyTask, Name: "foreign", Command: "node.exe"}, daemon.ProcessInfo{Executable: "node.exe"})
	if err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}
func TestWindowsLegacyReadOnlyThenChangedEntryRefused(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows discovery")
	}
	before := legacyCommand
	defer func() { legacyCommand = before }()
	p := daemon.ProcessInfo{PID: 123, CommandLine: `C:/node/node.exe C:/apps/9router/server.js`}
	e := LegacyEntry{Kind: LegacyTask, Name: "old-router", Command: p.CommandLine, Snapshot: "old XML"}
	writes := 0
	legacyCommand = func(name string, args ...string) (string, error) {
		if name != "powershell.exe" {
			return "", fmt.Errorf("unexpected helper %s", name)
		}
		if strings.Contains(strings.Join(args, " "), "/disable") {
			writes++
		}
		// A matching command but changed XML must not pass the second scan.
		e.Snapshot = "changed XML"
		b, _ := json.Marshal(struct {
			Entries  []LegacyEntry
			Warnings []string
		}{Entries: []LegacyEntry{e}})
		return string(b), nil
	}
	if err := DisableLegacy(LegacyEntry{Kind: LegacyTask, Name: e.Name, Command: e.Command, Snapshot: "old XML"}, p); err == nil {
		t.Fatal("stale snapshot accepted")
	}
	if writes != 0 {
		t.Fatal("wrote before validation")
	}
}
func TestRefuseHKLMWithoutProvenScope(t *testing.T) {
	p := daemon.ProcessInfo{CommandLine: `node.exe C:/apps/9router/server.js`}
	e := LegacyEntry{Kind: LegacyRegistry, Name: "old", Path: `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`, Command: p.CommandLine}
	if err := DisableLegacy(e, p); err == nil || !strings.Contains(err.Error(), "proven system scope") {
		t.Fatalf("unproven HKLM write: %v", err)
	}
}

func TestWindowsHKLMScopeAndReadOnlyDiscovery(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows discovery")
	}
	before := legacyCommand
	defer func() { legacyCommand = before }()
	for _, scope := range []bool{false, true} {
		legacyCommand = func(name string, args ...string) (string, error) {
			script := strings.Join(args, " ")
			if !strings.Contains(script, fmt.Sprintf("$systemScope=$%t", scope)) {
				t.Fatal("missing proven scope")
			}
			for _, mutation := range []string{"Remove-Item", "Set-ItemProperty", "Stop-Process", "Unregister-ScheduledTask"} {
				if strings.Contains(script, mutation) {
					t.Fatal("enumeration mutated state")
				}
			}
			return `{"Entries":[],"Warnings":[]}`, nil
		}
		entries, warnings := discoverWindowsLegacy(daemon.ProcessInfo{PID: 123, SystemScope: scope})
		if len(entries) != 0 || len(warnings) != 0 {
			t.Fatalf("%v %v", entries, warnings)
		}
	}
}
