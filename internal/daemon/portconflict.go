// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

type GatewayKind string

const (
	GatewayUnknown GatewayKind = "unknown process"
	GatewayLegacy  GatewayKind = "9Router"
	GatewayX       GatewayKind = "9RouterX"
)

// PortConflict is a snapshot, not authority to signal its PID. Revalidate before acting.
type PortConflict struct {
	URL         string
	Port        int
	Busy        bool
	Kind        GatewayKind
	Process     ProcessInfo
	Diagnostics []string
}

func (p PortConflict) String() string {
	return fmt.Sprintf("Port %d is used by %s (PID %d, name %s). Executable: %s; command line: %s; parent PID: %d", p.Port, p.Kind, p.Process.PID, p.Process.Name, p.Process.Executable, p.Process.CommandLine, p.Process.ParentPID)
}
func (p PortConflict) Error() string {
	hint := fmt.Sprintf("Set PORT=%d to run side by side.", AlternativePort(p.Port))
	if p.Kind == GatewayLegacy || p.Kind == GatewayX {
		hint = fmt.Sprintf("Run `9routerx service take-over` to migrate, or set PORT=%d to run side by side.", AlternativePort(p.Port))
	}
	return p.String() + "\nA listener is already running on this port. " + hint
}

// AlternativePort is the suggested side-by-side port; callers still check availability.
func AlternativePort(port int) int {
	if port >= 65535 {
		return 20129
	}
	return port + 1
}

// DescribePortConflict is shared by first run, ordinary start errors and take-over.
func DescribePortConflict(url string) PortConflict {
	p := PortConflict{URL: url, Kind: GatewayUnknown}
	host, port, ok := splitHostPort(url)
	if !ok {
		return p
	}
	p.Port, _ = strconv.Atoi(port)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), time.Second)
	if err != nil {
		return p
	}
	conn.Close()
	p.Busy = true
	p.Process, err = listenerProcess(p.Port)
	if err != nil {
		p.Diagnostics = append(p.Diagnostics, err.Error())
	}
	p.Kind = fingerprintGateway(url)
	if p.Kind == GatewayUnknown {
		p.Kind = processGateway(p.Process)
	}
	return p
}

var fingerprintClient = &http.Client{
	Timeout:       1500 * time.Millisecond,
	Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true},
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}

func fingerprintGateway(url string) GatewayKind {
	for _, path := range []string{"/", "/health"} {
		resp, err := fingerprintClient.Get(strings.TrimSuffix(url, "/") + path)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
		resp.Body.Close()
		text := strings.ToLower(string(body))
		if strings.Contains(text, "9routerx") {
			return GatewayX
		}
		if strings.Contains(text, "9router") && (strings.Contains(text, "__next") || strings.Contains(text, "/_next/") || strings.Contains(text, "<title>")) {
			return GatewayLegacy
		}
	}
	return GatewayUnknown
}
func processGateway(p ProcessInfo) GatewayKind {
	name := strings.ToLower(path.Base(strings.ReplaceAll(p.Executable, "\\", "/")))
	if name == "." || name == "" {
		name = strings.ToLower(p.Name)
	}
	name = strings.TrimSuffix(name, ".exe")
	if name == "9routerx" {
		return GatewayX
	}
	command := strings.ToLower(strings.ReplaceAll(p.CommandLine, "\\", "/"))
	if name == "9router" || (name == "node" && strings.Contains(command, "/9router/") && strings.Contains(command, ".js")) {
		return GatewayLegacy
	}
	return GatewayUnknown
}
func boundedCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}

// WaitPortFree reuses the daemon restart's bounded listener check.
func WaitPortFree(url string) error { return waitPortFree(url) }

// WaitHealthy reuses the daemon health probe, then verifies the root fingerprint.
func WaitHealthy(url string) error {
	deadline := time.Now().Add(StartupTimeout)
	for {
		if probeHealthy(strings.TrimSuffix(url, "/")+HealthPath) && fingerprintGateway(url) == GatewayX {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("9RouterX health/fingerprint verification failed at %s", url)
		}
		time.Sleep(150 * time.Millisecond)
	}
}
