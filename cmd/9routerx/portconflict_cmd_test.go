// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"

	"9routerx/proxy/internal/daemon"
)

func TestFirstRunPromptDoesNotStealPickerInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command *cli.Command
		want    bool
	}{
		{"no command", nil, false},
		{"bare picker", &cli.Command{Name: "9routerx"}, false},
		{"explicit start", &cli.Command{Name: "start"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testContext()
			c.Command = tc.command
			if got := directStartCommand(c); got != tc.want {
				t.Fatalf("direct start=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestFirstRunMenuAbort(t *testing.T) {
	var out bytes.Buffer
	_, err := resolveFirstConflict(nil, daemon.PortConflict{Port: 20128, Kind: daemon.GatewayLegacy}, bufio.NewReader(strings.NewReader("3\n")), &out)
	if err == nil {
		t.Fatal("abort accepted")
	}
	for _, text := range []string{"[1] Take over", "[2] Use another port", "[3] Abort", "20129"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %s: %s", text, &out)
		}
	}
}
