// 9RouterX — all source code port and modification by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/urfave/cli/v2"
	"golang.org/x/term"

	"9routerx/proxy/internal/config"
	"9routerx/proxy/internal/daemon"
	"9routerx/proxy/internal/proc"
	"9routerx/proxy/internal/updater"
)

// menuActions collects the operations the menu can trigger. Both renderers —
// the Bubble Tea model and the plain numbered fallback — call these same
// functions, so start/stop/status logic exists exactly once (in daemon_cmd.go
// and here for the tray), and a menu pick can never drift from the
// `9routerx start|stop|status` sub-commands.
type menuActions struct {
	openDashboard func(*cli.Context) error
	startDetached func(*cli.Context) error
	runServer     func(*cli.Context) error
	stopDetached  func(*cli.Context) error
	status        func(*cli.Context) error
	startTray     func(*cli.Context) error
}

// realMenuActions wires the actions the old switch called, unchanged.
func realMenuActions() menuActions {
	return menuActions{
		openDashboard: openDashboard,
		startDetached: startDetached,
		runServer:     runServer,
		stopDetached:  stopDetached,
		status:        statusDetached,
		startTray:     startTray,
	}
}

// menuItem pairs one entry the operator sees with its action and with how the
// loop treats a successful pick. The mapping mirrors the old switch exactly:
// item 3 takes the terminal over (it only makes sense once the TUI has given
// the screen back), items 4 and 5 return to the menu after running, and every
// other successful pick ends the menu as it always did.
type menuItem struct {
	key        string
	label      string
	action     func(*cli.Context) error
	backToMenu bool // success returns to the menu (old items 4 and 5)
	takeOver   bool // must run on the plain terminal after the TUI exits
}

func buildMenuItems(a menuActions) []menuItem {
	return []menuItem{
		{key: "1", label: "Open dashboard (Web UI)", action: a.openDashboard},
		{key: "2", label: "Start in background", action: a.startDetached},
		{key: "3", label: "Start in this terminal (foreground)", action: a.runServer, takeOver: true},
		{key: "4", label: "Stop background server", action: a.stopDetached, backToMenu: true},
		{key: "5", label: "Show status", action: a.status, backToMenu: true},
		{key: "6", label: "Run in background with tray icon (frees the terminal)", action: a.startTray},
	}
}

// stdinIsTerminal reports whether the bare invocation has a human at the
// keyboard. A service unit, a container, or a pipe runs the same command with
// no TTY, and those must keep booting the server exactly as before.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// runMenu is the bare-`9routerx` entry point. On a real terminal it shows the
// interactive picker (arrows/numbers, colour highlight); anything else —
// pipe, CI, service, /dev/null — takes the plain numbered fallback, which is
// byte-for-byte the behaviour this binary always had.
func runMenu(cCtx *cli.Context) error {
	actions := realMenuActions()
	// ModeCharDevice alone is not enough: /dev/null and a closed pipe are
	// character devices, which is why stdinIsTerminal let supervisors through
	// to the old menu's EOF branch. term.IsTerminal is the real check —
	// raw key input only exists on an actual console, so that is the gate for
	// the TUI; every other stdin keeps the plain loop.
	if stdinIsTerminal() && term.IsTerminal(int(os.Stdin.Fd())) {
		chosen, err := runBubbleMenu(cCtx, actions)
		if err == nil {
			if chosen == "3" {
				// The foreground server owns the terminal, so it only runs
				// after the picker has left the screen (as it did before the
				// TUI existed).
				return actions.runServer(cCtx)
			}
			return nil
		}
		// Bubble Tea could not drive this terminal (start error). Fall back
		// to the plain numbered menu rather than failing the invocation.
	}
	return runPlainMenu(cCtx, actions)
}

// runPlainMenu is the pre-TUI interface, kept verbatim for the non-TTY
// fallback: print the numbered list, read one line, act. On a first-read EOF
// there was never an operator, so it boots the server exactly as before —
// supervisors and the e2e harness depend on that.
func runPlainMenu(cCtx *cli.Context, actions menuActions) error {
	return runPlainMenuFrom(cCtx, actions, os.Stdin)
}

// runPlainMenuFrom is the same loop over an injected stream, so the tests can
// drive the fallback without a terminal.
func runPlainMenuFrom(cCtx *cli.Context, actions menuActions, in io.Reader) error {
	br := bufio.NewReader(in)
	first := true
	for {
		fmt.Print(menuText())
		line, ok := readChoice(br)
		if !ok {
			// The read ended at EOF before a byte arrived, so there is nobody
			// behind it however the stdin stat looked. /dev/null and a closed
			// pipe are both character devices, which is why stdinIsTerminal
			// let us in — and why a supervisor or the e2e harness, which give
			// the process exactly that, ended up here. That is the
			// non-interactive case, so boot the server as it always did rather
			// than exiting 1 with a menu nobody read.
			if first {
				return actions.runServer(cCtx)
			}
			return nil
		}
		first = false
		choice := parseChoice(line)
		switch choice {
		case "1":
			return actions.openDashboard(cCtx)
		case "2":
			return actions.startDetached(cCtx)
		case "3":
			return actions.runServer(cCtx)
		case "4":
			if err := actions.stopDetached(cCtx); err != nil {
				return err
			}
		case "5":
			if err := actions.status(cCtx); err != nil {
				return err
			}
		case "6":
			return actions.startTray(cCtx)
		default:
			return nil
		}
	}
}

// menuText renders the header plus the numbered items, with the live daemon
// state in front of the choice so the pick is informed.
func menuText() string {
	state := "stopped"
	if pid := daemon.RunningPID(); pid != 0 {
		state = fmt.Sprintf("\x1b[32mrunning (pid %d)\x1b[0m", pid)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s v%s (%s/%s)\n", binaryName(), updater.CurrentVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "  Server: %s  [%s]\n\n", daemonURL(), state)
	b.WriteString("  1) Open dashboard (Web UI)\n")
	b.WriteString("  2) Start in background\n")
	b.WriteString("  3) Start in this terminal (foreground)\n")
	b.WriteString("  4) Stop background server\n")
	b.WriteString("  5) Show status\n")
	b.WriteString("  6) Run in background with tray icon (frees the terminal)\n")
	b.WriteString("  0) Exit\n\n")
	b.WriteString("  Choose [1]: ")
	return b.String()
}

// readChoice blocks for one line. ok is false when the stream ended without
// yielding anything: the caller distinguishes "the operator pressed Ctrl+Z" from
// "there was never an operator" by whether this was the first read.
func readChoice(in *bufio.Reader) (line string, ok bool) {
	line, err := in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "0", true
	}
	// io.EOF with a partial line (a terminal that closed mid-line) still
	// carries the operator's input; EOF with nothing does not.
	return line, line != ""
}

// parseChoice maps the typed line to a menu key. Enter alone takes the
// default (open dashboard); anything unrecognised exits rather than looping.
func parseChoice(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return "1"
	}
	switch s {
	case "0", "1", "2", "3", "4", "5", "6":
		return s
	case "q", "Q", "exit":
		return "0"
	}
	return "0"
}

// ---------------------------------------------------------------------------
// Bubble Tea interactive picker.
//
// The accent is the dashboard's own --9x-accent (web/src/index.css), so the
// CLI highlight matches the Web UI instead of inventing a colour.
// ---------------------------------------------------------------------------

var (
	menuAccent = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#2f5bd3"))
	menuLive   = lipgloss.NewStyle().Foreground(lipgloss.Color("46"))
	menuDown   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	menuError  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

type menuModel struct {
	ctx    *cli.Context
	items  []menuItem
	cursor int // 0..len(items) — the last position is the Exit row
	err    error
	busy   bool
	done   bool   // the picker has ended (quit key, successful pick, or take-over)
	chosen string // set only for the take-over item, handled after the TUI exits
}

func newMenuModel(cCtx *cli.Context, actions menuActions) menuModel {
	return menuModel{ctx: cCtx, items: buildMenuItems(actions)}
}

// actionDoneMsg carries the outcome of a menu action that ran off the UI
// thread (start background can block for the full health-probe timeout, and
// freezing the renderer that whole time would make the picker look hung).
type actionDoneMsg struct {
	index int
	err   error
}

// statusTickMsg re-renders the header so the running/stopped line tracks a
// daemon started or stopped from another terminal.
type statusTickMsg struct{}

func statusTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return statusTickMsg{} })
}

func (m menuModel) Init() tea.Cmd {
	return statusTick()
}

// selectItem starts the chosen item's action. Item 3 (foreground) is the one
// pick the TUI cannot run itself: it replaces the screen, so it is recorded
// and executed by runMenu after the program has left the terminal.
func (m menuModel) selectItem(i int) (tea.Model, tea.Cmd) {
	if m.busy {
		return m, nil
	}
	if i == len(m.items) { // the Exit row
		m.done = true
		return m, tea.Quit
	}
	item := m.items[i]
	if item.takeOver {
		m.chosen = item.key
		m.done = true
		return m, tea.Quit
	}
	m.err = nil
	m.busy = true
	index := i
	ctx := m.ctx
	action := item.action
	return m, func() tea.Msg {
		return actionDoneMsg{index: index, err: action(ctx)}
	}
}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusTickMsg:
		return m, statusTick()

	case actionDoneMsg:
		m.busy = false
		if msg.err != nil {
			// The error is rendered in the picker itself — inside an active
			// program a bare fmt.Println would end up interleaved with the
			// frame, so a failed start/stop must land in the model to be seen.
			m.err = msg.err
			return m, nil
		}
		if msg.index < len(m.items) && m.items[msg.index].backToMenu {
			// Old items 4 and 5: report and return to the menu.
			return m, nil
		}
		m.done = true
		return m, tea.Quit

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q", "Q", "0":
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items) {
				m.cursor++
			}
		case "enter":
			return m.selectItem(m.cursor)
		case "1", "2", "3", "4", "5", "6":
			// Number keys keep the old direct-select behaviour.
			return m.selectItem(int(msg.String()[0] - '1'))
		}
	}
	return m, nil
}

func (m menuModel) View() string {
	var b strings.Builder
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s v%s (%s/%s)\n", binaryName(), updater.CurrentVersion, runtime.GOOS, runtime.GOARCH)

	state := menuDown.Render("stopped")
	if pid := daemon.RunningPID(); pid != 0 {
		state = menuLive.Render(fmt.Sprintf("running (pid %d)", pid))
	}
	fmt.Fprintf(&b, "  Server: %s  [%s]\n", daemonURL(), state)

	if m.busy {
		b.WriteString("  Working...\n")
	}
	if m.err != nil {
		fmt.Fprintf(&b, "  %s\n", menuError.Render("[!] "+m.err.Error()))
	}
	b.WriteString("\n")
	for i, item := range m.items {
		row := fmt.Sprintf("%s) %s", item.key, item.label)
		if i == m.cursor {
			fmt.Fprintf(&b, "  %s\n", menuAccent.Render("> "+row))
			continue
		}
		fmt.Fprintf(&b, "  %s\n", menuDown.Render("  "+row))
	}
	exit := "0) Exit"
	if m.cursor == len(m.items) {
		fmt.Fprintf(&b, "  %s\n", menuAccent.Render("> "+exit))
	} else {
		fmt.Fprintf(&b, "  %s\n", menuDown.Render("  "+exit))
	}
	b.WriteString("\n")
	b.WriteString(menuDown.Render("  ↑/↓ or j/k move · Enter select · 1-6 direct · 0/q/Esc quit") + "\n")
	return b.String()
}

// runBubbleMenu drives the picker and reports the take-over choice, if any.
// A start error returns (chosen, err) so runMenu can fall back to the plain
// numbered menu; normal quits return (chosen, nil).
func runBubbleMenu(cCtx *cli.Context, actions menuActions) (string, error) {
	p := tea.NewProgram(newMenuModel(cCtx, actions))
	out, err := p.Run()
	if err != nil {
		return "", err
	}
	if mm, ok := out.(menuModel); ok {
		return mm.chosen, nil
	}
	return "", nil
}

// startTray hands the tray to its own binary and returns, so the terminal is
// free while the icon stays.
//
// The tray lives in cmd/9router-tray because a GUI toolkit cannot be linked
// into this binary without breaking the CGO_ENABLED=0 cross-build for all five
// targets (docs/specs/tray-companion-architecture.md §X2). Spawning keeps the
// menu entry the Node CLI has ("Hide to Tray") without importing it here.
//
// Only the sibling binary is tried: a PATH lookup could pick up a different
// install's tray, which would then talk to a gateway that is not this one.
func startTray(cCtx *cli.Context) error {
	self, err := proc.SelfExecutable()
	if err != nil {
		return fmt.Errorf("tray: locate this binary: %w", err)
	}
	exe := filepath.Join(filepath.Dir(self), trayBinaryName())
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if _, err := os.Stat(exe); err != nil {
		fmt.Printf("tray companion %s not found next to %s; falling back to background server...\n", filepath.Base(exe), filepath.Base(self))
		return startDetached(cCtx)
	}
	// CommandContext with a context that is never cancelled, not a deadline:
	// this process is released immediately below and is meant to outlive the
	// terminal, so a deadline here would kill the tray it just started.
	cmd := exec.CommandContext(context.Background(), exe, "--port", strconv.Itoa(config.LoadConfig().Port))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tray: start %s: %w", filepath.Base(exe), err)
	}
	// Releasing lets the tray outlive this terminal, the same reason the
	// background daemon is released rather than waited on.
	_ = cmd.Process.Release()
	fmt.Printf("Tray started. Right-click the icon to open the dashboard or quit.\n")
	return nil
}

// trayBinaryName follows the same rename rule as binaryName, so an
// `opencode-go` install looks for `opencode-go-tray`.
func trayBinaryName() string {
	return binaryName() + "-tray"
}

// openDashboard makes sure the daemon is up, then hands the dashboard URL to
// the platform browser.
func openDashboard(cCtx *cli.Context) error {
	url := daemonURL()
	target := url + "/dashboard"
	if daemon.RunningPID() == 0 {
		res, err := daemon.Start(url, forwardArgs(cCtx))
		if err != nil && !errors.Is(err, daemon.ErrAlreadyRunning) {
			return err
		}
		if res.URL != "" {
			target = res.URL
		}
	}
	fmt.Printf("Opening %s\n", target)
	return openInBrowser(cCtx.Context, target)
}

// openInBrowser uses the OS opener so no dependency is pulled in.
//
// CommandContext rather than Command: the linters require a context, and it
// costs nothing here — the opener is detached from the caller and the process
// outlives the call either way. The dashboard URL is built from the daemon's
// own listen address, never from client input, so it cannot smuggle an
// argument to the opener.
func openInBrowser(ctx context.Context, target string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		return exec.CommandContext(ctx, "open", target).Start()
	default:
		return exec.CommandContext(ctx, "xdg-open", target).Start()
	}
}
