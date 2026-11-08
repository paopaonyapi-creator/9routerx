// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v2"
)

func TestParseChoice(t *testing.T) {
	cases := map[string]string{
		"":        "1", // Enter takes the default
		"\n":      "1",
		"1\n":     "1",
		"3":       "3",
		"6\n":     "6",
		" 5 \n":   "5",
		"0\n":     "0",
		"q\n":     "0",
		"bogus\n": "0",
	}
	for in, want := range cases {
		if got := parseChoice(in); got != want {
			t.Errorf("parseChoice(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReadChoiceEOFIsNotABlankLine: the caller tells "no operator at all" from
// "the operator pressed Ctrl+Z" by whether anything arrived, so an empty read
// has to be distinguishable from a blank line.
func TestReadChoiceEOFIsNotABlankLine(t *testing.T) {
	if line, ok := readChoice(bufio.NewReader(strings.NewReader(""))); ok {
		t.Errorf("readChoice on an empty stream = (%q, true), want ok=false", line)
	}
	if line, ok := readChoice(bufio.NewReader(strings.NewReader("\n"))); !ok || line != "\n" {
		t.Errorf("readChoice(blank line) = (%q, %v), want (\\n, true)", line, ok)
	}
	// A line with no trailing newline still carries the operator's input.
	if line, ok := readChoice(bufio.NewReader(strings.NewReader("3"))); !ok || line != "3" {
		t.Errorf("readChoice(\"3\" at EOF) = (%q, %v), want (3, true)", line, ok)
	}
}

func TestTrayBinaryName(t *testing.T) {
	name := trayBinaryName()
	if !strings.HasSuffix(name, "-tray") {
		t.Errorf("expected tray binary name to end with -tray, got %q", name)
	}
}

// recordedActions is a menuActions whose members log their calls. Safe for the
// Bubble Tea path too, where the action runs off the UI thread.
type recordedActions struct {
	mu     sync.Mutex
	called []string
	errs   map[string]error
}

func (r *recordedActions) record(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.called = append(r.called, name)
	if r.errs != nil {
		return r.errs[name]
	}
	return nil
}

func (r *recordedActions) has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.called {
		if c == name {
			return true
		}
	}
	return false
}

func (r *recordedActions) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.called {
		if c == name {
			n++
		}
	}
	return n
}

func (r *recordedActions) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.called))
	copy(out, r.called)
	return out
}

func (r *recordedActions) actions() menuActions {
	return menuActions{
		openDashboard: func(*cli.Context) error { return r.record("openDashboard") },
		startDetached: func(*cli.Context) error { return r.record("startDetached") },
		runServer:     func(*cli.Context) error { return r.record("runServer") },
		stopDetached:  func(*cli.Context) error { return r.record("stopDetached") },
		status:        func(*cli.Context) error { return r.record("status") },
		startTray:     func(*cli.Context) error { return r.record("startTray") },
	}
}

func testContext() *cli.Context {
	return cli.NewContext(nil, flag.NewFlagSet("menu-test", flag.ContinueOnError), nil)
}

// TestMenuItemsMapToActions: the TUI and the plain fallback share one item
// list, and that list must wire each entry to the exact function the old
// switch called — pointer identity, not just any function.
func TestMenuItemsMapToActions(t *testing.T) {
	actions := realMenuActions()
	items := buildMenuItems(actions)

	wantKeys := []string{"1", "2", "3", "4", "5", "6"}
	wantFuncs := []func(*cli.Context) error{
		actions.openDashboard, actions.startDetached, actions.runServer,
		actions.stopDetached, actions.status, actions.startTray,
	}
	wantBackToMenu := map[int]bool{3: true, 4: true} // old items 4 and 5 loop back
	wantTakeOver := map[int]bool{2: true}            // old item 3 owns the terminal

	if len(items) != len(wantKeys) {
		t.Fatalf("buildMenuItems produced %d items, want %d", len(items), len(wantKeys))
	}
	for i, item := range items {
		if item.key != wantKeys[i] {
			t.Errorf("item %d key = %q, want %q", i, item.key, wantKeys[i])
		}
		if item.label == "" {
			t.Errorf("item %d has an empty label", i)
		}
		if item.action == nil {
			t.Errorf("item %d (%s) has no action", i, item.key)
			continue
		}
		if reflect.ValueOf(item.action).Pointer() != reflect.ValueOf(wantFuncs[i]).Pointer() {
			t.Errorf("item %d (%s) is wired to the wrong action", i, item.key)
		}
		if item.backToMenu != wantBackToMenu[i] {
			t.Errorf("item %d (%s) backToMenu = %v, want %v", i, item.key, item.backToMenu, wantBackToMenu[i])
		}
		if item.takeOver != wantTakeOver[i] {
			t.Errorf("item %d (%s) takeOver = %v, want %v", i, item.key, item.takeOver, wantTakeOver[i])
		}
	}
}

// runPlain drives the plain numbered fallback with scripted stdin and records
// which actions fired. The fallback must keep the exact old behaviour: number
// keys select, Enter defaults to item 1, 0/q exit.
func runPlain(t *testing.T, input string, actions menuActions) error {
	t.Helper()
	return runPlainMenuFrom(testContext(), actions, strings.NewReader(input))
}

func TestPlainMenuNumberFallback(t *testing.T) {
	allNames := []string{"openDashboard", "startDetached", "runServer", "stopDetached", "status", "startTray"}
	cases := []struct {
		name   string
		input  string
		called []string
	}{
		{"number selects directly", "2\n", []string{"startDetached"}},
		{"blank line takes the default item 1", "\n", []string{"openDashboard"}},
		{"explicit 1", "1\n", []string{"openDashboard"}},
		{"0 exits without acting", "0\n", nil},
		{"q exits without acting", "q\n", nil},
		{"unknown input exits without acting", "zz\n", nil},
		{"stop loops back then exit", "4\n0\n", []string{"stopDetached"}},
		{"status loops back then exit", "5\n0\n", []string{"status"}},
		{"tray ends the menu", "6\n", []string{"startTray"}},
		{"foreground server ends the menu", "3\n", []string{"runServer"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordedActions{}
			if err := runPlain(t, tc.input, rec.actions()); err != nil {
				t.Fatalf("runPlainMenu(%q) = %v, want nil", tc.input, err)
			}
			if len(tc.called) == 0 {
				for _, name := range allNames {
					if rec.has(name) {
						t.Errorf("input %q unexpectedly called %s", tc.input, name)
					}
				}
				return
			}
			got := rec.calls()
			if strings.Join(got, ",") != strings.Join(tc.called, ",") {
				t.Errorf("input %q called %v, want %v", tc.input, got, tc.called)
			}
		})
	}
}

// TestPlainMenuEOFBootsServer: a supervisor or the e2e harness hands the
// process a character-device stdin that ends at EOF. The old contract boots
// the server on that first empty read, and the fallback must keep it.
func TestPlainMenuEOFBootsServer(t *testing.T) {
	rec := &recordedActions{}
	if err := runPlain(t, "", rec.actions()); err != nil {
		t.Fatalf("runPlainMenu on EOF = %v, want nil", err)
	}
	if !rec.has("runServer") {
		t.Errorf("first-read EOF must boot the server, called %v", rec.calls())
	}
}

// TestPlainMenuErrorPropagates: a failing action ends the menu with the error,
// as the old switch did (return err straight to the CLI).
func TestPlainMenuErrorPropagates(t *testing.T) {
	rec := &recordedActions{errs: map[string]error{"stopDetached": errors.New("stop failed: refused")}}
	err := runPlain(t, "4\n", rec.actions())
	if err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Errorf("runPlainMenu(\"4\") = %v, want the stop error", err)
	}
}

// keyStroke builds the KeyMsg a real key press produces.
func keyStroke(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// press feeds one keystroke to the model and hands back the follow-up command
// (nil when the key triggered no action).
func press(m menuModel, key string) (menuModel, tea.Cmd) {
	next, cmd := m.Update(keyStroke(key))
	nm, ok := next.(menuModel)
	if !ok {
		panic("Update did not return a menuModel")
	}
	return nm, cmd
}

// runAction executes the command a selection returned and feeds its result
// back into the model, exactly as the Bubble Tea runtime would.
func runAction(t *testing.T, m menuModel, cmd tea.Cmd) menuModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("selection returned no command")
	}
	next, _ := m.Update(cmd())
	nm, ok := next.(menuModel)
	if !ok {
		t.Fatal("Update did not return a menuModel after actionDoneMsg")
	}
	return nm
}

// quitRequested reports whether the picker ended. The model signals exit by
// returning tea.Quit; the done flag is the same decision, kept as state so
// tests can assert it.
func (m menuModel) quitRequested() bool { return m.done }

func TestMenuModelNavigationAndQuit(t *testing.T) {
	rec := &recordedActions{}
	m := newMenuModel(testContext(), rec.actions())

	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0 (item 1 is the Enter default)", m.cursor)
	}
	m, _ = press(m, "down")
	m, _ = press(m, "j")
	if m.cursor != 2 {
		t.Errorf("cursor after down,j = %d, want 2", m.cursor)
	}
	m, _ = press(m, "up")
	m, _ = press(m, "k")
	if m.cursor != 0 {
		t.Errorf("cursor after up,k = %d, want 0", m.cursor)
	}
	// The cursor clamps at both ends rather than wrapping into a pick.
	m, _ = press(m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor above first item = %d, want clamped 0", m.cursor)
	}
	for i := 0; i < 10; i++ {
		m, _ = press(m, "down")
	}
	if m.cursor != 6 {
		t.Errorf("cursor past the last row = %d, want clamped 6 (Exit)", m.cursor)
	}

	for _, key := range []string{"q", "esc", "ctrl+c", "0", "Q"} {
		mm, cmd := press(m, key)
		if cmd == nil {
			t.Errorf("key %q returned no quit command", key)
		} else if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
			t.Errorf("key %q did not produce tea.Quit", key)
		}
		if mm.chosen != "" {
			t.Errorf("key %q set chosen=%q, want empty", key, mm.chosen)
		}
		if !mm.quitRequested() {
			t.Errorf("key %q did not end the picker", key)
		}
	}
}

func TestMenuModelNumberKeysSelectDirectly(t *testing.T) {
	// Number keys keep the old behaviour: 1..6 pick that item immediately,
	// 0 exits. Item 3 is recorded for the post-TUI hand-off, item 6 runs.
	rec := &recordedActions{}
	m := newMenuModel(testContext(), rec.actions())

	m, cmd := press(m, "3")
	if m.chosen != "3" {
		t.Errorf("key 3 did not record the take-over pick, chosen=%q", m.chosen)
	}
	if cmd == nil {
		t.Fatal("key 3 returned no command (want quit)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("key 3 did not quit the picker")
	}
	if rec.count("runServer") != 0 {
		t.Error("the foreground server must not run until the TUI has exited")
	}

	m2 := newMenuModel(testContext(), rec.actions())
	m2, cmd = press(m2, "6")
	if m2.chosen != "" {
		t.Errorf("key 6 must run inside the picker, chosen=%q", m2.chosen)
	}
	m2 = runAction(t, m2, cmd)
	if !rec.has("startTray") {
		t.Error("key 6 did not run the tray action")
	}
	// Success on a non-loop item ends the menu, like the old switch.
	if !m2.quitRequested() {
		t.Error("successful tray pick should quit the picker")
	}

	rec2 := &recordedActions{}
	m3 := newMenuModel(testContext(), rec2.actions())
	m3, cmd = press(m3, "0")
	if cmd == nil {
		t.Fatal("key 0 returned no command (want quit)")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("key 0 did not quit the picker")
	}
	if rec2.count("openDashboard")+rec2.count("startDetached")+rec2.count("runServer")+rec2.count("stopDetached")+rec2.count("status")+rec2.count("startTray") != 0 {
		t.Error("key 0 must not run any action")
	}
}

func TestMenuModelEnterDefaultAndLoopBack(t *testing.T) {
	rec := &recordedActions{}
	m := newMenuModel(testContext(), rec.actions())

	// Enter on nothing = item 1 (the old Choose [1]: default).
	m, cmd := press(m, "enter")
	m = runAction(t, m, cmd)
	if !rec.has("openDashboard") {
		t.Error("Enter at the initial cursor must run item 1 (open dashboard)")
	}
	if !m.quitRequested() {
		t.Error("a successful item-1 pick should end the menu")
	}

	// Old items 4 and 5 return to the menu after reporting.
	rec2 := &recordedActions{}
	m2 := newMenuModel(testContext(), rec2.actions())
	m2, cmd = press(m2, "4")
	m2 = runAction(t, m2, cmd)
	if !rec2.has("stopDetached") {
		t.Error("item 4 did not run the stop action")
	}
	if m2.quitRequested() {
		t.Error("item 4 success must return to the menu, not quit")
	}
	m2, cmd = press(m2, "5")
	m2 = runAction(t, m2, cmd)
	if !rec2.has("status") {
		t.Error("item 5 did not run the status action")
	}
	if m2.quitRequested() {
		t.Error("item 5 success must return to the menu, not quit")
	}
	// The menu is still navigable after the loop-back items.
	m2, _ = press(m2, "down")
	if m2.cursor != 1 {
		t.Errorf("cursor after loop-back navigation = %d, want 1", m2.cursor)
	}
}

func TestMenuModelRenderNoPanic(t *testing.T) {
	rec := &recordedActions{}
	m := newMenuModel(testContext(), rec.actions())

	// States worth rendering: fresh, mid-navigation, busy, erroring, at the
	// bottom of the list. None of these may panic.
	states := []menuModel{m}
	m, _ = press(m, "down")
	m.busy = true
	states = append(states, m)
	m.busy = false
	m.err = errors.New("9routerx is already running on http://127.0.0.1:20128 — stop it first")
	states = append(states, m)
	for i := 0; i <= len(m.items); i++ {
		mm := newMenuModel(testContext(), rec.actions())
		mm.cursor = i
		states = append(states, mm)
	}
	for i, st := range states {
		v := st.View()
		if v == "" {
			t.Errorf("state %d rendered an empty view", i)
		}
		if !strings.Contains(v, "9routerx v") {
			t.Errorf("state %d view missing the version header", i)
		}
		if !strings.Contains(v, "Server:") {
			t.Errorf("state %d view missing the server status line", i)
		}
		if !strings.Contains(v, "0) Exit") {
			t.Errorf("state %d view missing the Exit row", i)
		}
	}
	if got := states[0].View(); !strings.Contains(got, "Open dashboard") {
		t.Error("view missing item 1 label")
	}
	if got := states[0].View(); !strings.Contains(got, "stopped") && !strings.Contains(got, "running") {
		t.Error("view missing the running/stopped server state")
	}
}

// TestMenuModelErrorVisibleInTUI: an action that fails must surface its error
// inside the picker (rendered by the model), not print-and-vanish.
func TestMenuModelErrorVisibleInTUI(t *testing.T) {
	rec := &recordedActions{errs: map[string]error{
		"startDetached": fmt.Errorf("9routerx is already running on http://127.0.0.1:20128 (started outside this binary) — stop it first"),
	}}
	m := newMenuModel(testContext(), rec.actions())

	m, cmd := press(m, "2")
	m = runAction(t, m, cmd)

	if m.err == nil {
		t.Fatal("a failed start must set the model error")
	}
	v := m.View()
	if !strings.Contains(v, "[!] 9routerx is already running") {
		t.Errorf("failed start error not rendered in the picker view:\n%s", v)
	}
	if m.quitRequested() {
		t.Error("a failed pick must keep the menu open so the error can be read")
	}
}

// TestMenuShowsErrorWhenPortInUse is the real integration case: something
// already listens on the configured port, the operator picks "Start in
// background", and the picker must show the failure.
func TestMenuShowsErrorWhenPortInUse(t *testing.T) {
	// Hold a real listener so the daemon's port probe sees the port busy.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a loopback listener: %v", err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	// Point the config and the daemon state dir at this test only.
	t.Setenv("PORT", strconv.Itoa(port))
	t.Setenv("DATA_DIR", t.TempDir())

	// The real action list: this exercises startDetached exactly as the menu
	// does, with no injected failure.
	m := newMenuModel(testContext(), realMenuActions())
	m, cmd := press(m, "2")
	m = runAction(t, m, cmd)

	if m.err == nil {
		t.Fatalf("start against a busy port must fail; view:\n%s", m.View())
	}
	v := m.View()
	if !strings.Contains(v, "already running") {
		t.Errorf("port-in-use error not rendered in the picker view:\n%s", v)
	}
	if m.quitRequested() {
		t.Error("the menu must stay open after a failed start")
	}
}
