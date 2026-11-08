// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urfave/cli/v2"
)

func TestMigrationConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, input         string
		yes, terminal, want bool
	}{
		{"accept", "y\n", false, true, true}, {"decline", "n\n", false, true, false}, {"default", "\n", false, true, false},
		{"pipe cannot approve", "y\n", false, false, false}, {"explicit yes", "", true, false, true}, {"EOF", "", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := migrationPrompts{yes: tc.yes, terminal: tc.terminal, in: bufio.NewReader(strings.NewReader(tc.input)), out: &out}
			err := p.confirm("stop?")
			if (err == nil) != tc.want {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "stop?") {
				t.Fatal("action not reported")
			}
		})
	}
}
func TestMigrationCountsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"providerConnections", "apiKeys"} {
		if _, err := conn.Exec(`CREATE TABLE "` + table + `" (id TEXT); INSERT INTO "` + table + `" VALUES ('fixture')`); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printMigrationData(&out, path)
	for _, text := range []string{"providerConnections: 1", "apiKeys: 1"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %s: %s", text, &out)
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("read-only summary modified database")
	}
}
func TestMissingMigrationDatabaseNotCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	var out bytes.Buffer
	printMigrationData(&out, path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created missing database")
	}
	if !strings.Contains(out.String(), "location checked:") {
		t.Fatal(out.String())
	}
}
func TestServiceConfigPersistsEffectiveLocations(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("PORT", "20129")
	set := flag.NewFlagSet("test", flag.ContinueOnError)
	c := cli.NewContext(&cli.App{}, set, nil)
	c.Context = context.Background()
	args, err := serviceArgs(c)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, text := range []string{"--port=20129", "--data-dir=" + dir, "--db-path=" + filepath.Join(dir, "db", "data.sqlite")} {
		if !strings.Contains(joined, text) {
			t.Fatal(fmt.Sprint(args))
		}
	}
}
