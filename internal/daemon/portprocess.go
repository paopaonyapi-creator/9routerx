// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"9routerx/proxy/internal/proc"
)

type ProcessInfo struct {
	SystemScope bool   `json:"SystemScope"`
	PID         int    `json:"ProcessId"`
	ParentPID   int    `json:"ParentProcessId"`
	Name        string `json:"Name"`
	Executable  string `json:"ExecutablePath"`
	CommandLine string `json:"CommandLine"`
}

func listenerProcess(port int) (ProcessInfo, error) {
	if runtime.GOOS == "windows" {
		out, err := boundedCommand("netstat.exe", "-ano", "-p", "tcp")
		if err != nil {
			return ProcessInfo{}, err
		}
		for _, line := range strings.Split(out, "\n") {
			f := strings.Fields(line)
			if len(f) < 5 || f[0] != "TCP" || f[3] != "LISTENING" {
				continue
			}
			if !strings.HasSuffix(f[1], ":"+strconv.Itoa(port)) {
				continue
			}
			pid, _ := strconv.Atoi(f[4])
			return inspectProcess(pid)
		}
		return ProcessInfo{}, fmt.Errorf("listener PID unavailable for port %d", port)
	}
	out, err := boundedCommand("lsof", "-nP", "-t", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN")
	if err == nil {
		if f := strings.Fields(out); len(f) > 0 {
			pid, _ := strconv.Atoi(f[0])
			return inspectProcess(pid)
		}
	}
	out, err = boundedCommand("ss", "-ltnp", fmt.Sprintf("sport = :%d", port))
	if err == nil {
		if _, rest, ok := strings.Cut(out, "pid="); ok {
			pid, _ := strconv.Atoi(strings.SplitN(rest, ",", 2)[0])
			return inspectProcess(pid)
		}
	}
	return ProcessInfo{}, fmt.Errorf("listener PID unavailable; install lsof or allow ss process inspection")
}
func inspectProcess(pid int) (ProcessInfo, error) {
	p := ProcessInfo{PID: pid}
	if pid <= 0 {
		return p, fmt.Errorf("invalid listener PID")
	}
	if runtime.GOOS == "windows" {
		script := fmt.Sprintf("Get-CimInstance Win32_Process -Filter 'ProcessId = %d' | Select-Object ProcessId,ParentProcessId,Name,ExecutablePath,CommandLine,@{n='SystemScope';e={@(Get-CimInstance Win32_Service -Filter 'ProcessId = %d').Count -gt 0}} | ConvertTo-Json -Compress", pid, pid)
		out, err := boundedCommand("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		if err == nil && json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(out), "\ufeff")), &p) == nil && p.Executable != "" {
			return p, nil
		}
		name, _ := boundedCommand("tasklist.exe", "/fi", fmt.Sprintf("PID eq %d", pid), "/fo", "csv", "/nh")
		p.Name = strings.Trim(strings.SplitN(strings.TrimSpace(name), ",", 2)[0], "\"")
		return p, fmt.Errorf("process executable/command line unavailable for PID %d", pid)
	}
	out, err := boundedCommand("ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=", "-o", "args=")
	if err != nil {
		return p, err
	}
	f := strings.Fields(out)
	if len(f) < 3 {
		return p, fmt.Errorf("process details unavailable for PID %d", pid)
	}
	p.ParentPID, _ = strconv.Atoi(f[0])
	p.Name = f[1]
	p.CommandLine = strings.Join(f[2:], " ")
	if runtime.GOOS == "linux" {
		p.Executable, _ = os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	} else {
		p.Executable = f[1]
	}
	return p, nil
}

// SameProcess rejects recycled PIDs and missing identity rather than guessing.
func SameProcess(want ProcessInfo) bool {
	got, err := inspectProcess(want.PID)
	return err == nil && want.Executable != "" && want.CommandLine != "" && got.Executable == want.Executable && got.CommandLine == want.CommandLine && got.ParentPID == want.ParentPID
}

// RequestPortStop never escalates. The caller must warn and confirm ForcePortStop separately.
func RequestPortStop(p PortConflict) error {
	if current, err := listenerProcess(p.Port); err != nil || current.PID != p.Process.PID || !SameProcess(p.Process) {
		return fmt.Errorf("process identity changed or cannot be verified; rerun take-over")
	}
	if p.Kind == GatewayX && p.Process.PID == RunningPID() && requestGracefulStop() {
		return nil
	}
	if runtime.GOOS == "windows" {
		_, err := boundedCommand("taskkill.exe", "/PID", strconv.Itoa(p.Process.PID))
		return err
	}
	process, err := os.FindProcess(p.Process.PID)
	if err != nil {
		return err
	}
	return process.Signal(syscall.SIGTERM)
}
func ForcePortStop(p PortConflict) error {
	if current, err := listenerProcess(p.Port); err != nil || current.PID != p.Process.PID || !SameProcess(p.Process) {
		return fmt.Errorf("process identity changed or cannot be verified; refusing force kill")
	}
	return proc.ForceKill(p.Process.PID)
}
