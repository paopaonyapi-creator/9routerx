// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
// The config-bundle CLI commands (FASE 10.5, the CONFIG BUNDLE half of
// OmniRoute's "Sync Tokens & Config Bundle"): `9routerx export-bundle` and
// `9routerx import-bundle`.
//
// This file only DEFINES the commands; main.go's Commands slice gains
// syncBundleCommands() when the feature is wired, which is why everything
// here is one self-contained append.
package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	"9routerx/proxy/internal/config"
	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/syncbundle"
	"9routerx/proxy/internal/vault"
)

// syncBundleCommands returns the bundle subcommands for main's command list.
func syncBundleCommands() []*cli.Command {
	return []*cli.Command{
		{
			Name:      "export-bundle",
			Usage:     "Export the configuration (settings, keys, connections, combos, teams, webhooks) to a bundle file",
			ArgsUsage: "--out <file>",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Usage: "output bundle file path (required)"},
			},
			Action: exportBundle,
		},
		{
			Name:      "import-bundle",
			Usage:     "Import a configuration bundle written by export-bundle",
			ArgsUsage: "--in <file>",
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "in", Aliases: []string{"i"}, Usage: "input bundle file path (required)"},
			},
			Action: importBundle,
		},
	}
}

// openBundleRepo opens the install's database the same way the `db` backup
// subcommands do, plus the credential vault: without it the connections
// export redacted instead of hydrated, and the bundle would silently carry
// no credentials at all.
func openBundleRepo() (*db.Repo, func(), error) {
	cfg := config.LoadConfig()
	conn, err := db.OpenDatabase(cfg.DatabasePath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := db.EnsureCoreSchema(conn); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("ensure schema: %w", err)
	}
	repo := db.NewRepo(conn)
	repo.SetVault(vault.NewFromEnv())
	return repo, func() { conn.Close() }, nil
}

// writeBundleFile lands the bundle with owner-only permissions. On Windows
// the mode is advisory, but 0600 is the convention the database file itself
// uses (db.OpenDatabase), and on Linux/macOS it is the difference between a
// secret file and a readable one.
func writeBundleFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	return nil
}

func exportBundle(cCtx *cli.Context) error {
	outPath := cCtx.String("out")
	if outPath == "" {
		return fmt.Errorf("usage: 9routerx export-bundle --out <file>")
	}
	repo, closeRepo, err := openBundleRepo()
	if err != nil {
		return err
	}
	defer closeRepo()

	plain, err := syncbundle.Export(repo)
	if err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Encrypted when a master key exists; plaintext otherwise, but said out
	// loud — a file carrying provider credentials must never land quietly.
	if syncbundle.MasterKeyPresent() {
		if plain, err = syncbundle.Encrypt(plain); err != nil {
			return fmt.Errorf("encrypt failed: %w", err)
		}
		fmt.Println("Bundle encrypted with ROUTER_MASTER_KEY.")
	} else {
		fmt.Println("⚠️  No ROUTER_MASTER_KEY configured: the bundle is PLAINTEXT and contains provider credentials. Store it securely or set the key and re-export.")
	}
	if err := writeBundleFile(outPath, plain); err != nil {
		return err
	}
	fmt.Printf("✅ Bundle exported to %s\n", outPath)
	return nil
}

func importBundle(cCtx *cli.Context) error {
	inPath := cCtx.String("in")
	if inPath == "" {
		return fmt.Errorf("usage: 9routerx import-bundle --in <file>")
	}
	blob, err := os.ReadFile(inPath)
	if err != nil {
		return fmt.Errorf("read bundle: %w", err)
	}
	repo, closeRepo, err := openBundleRepo()
	if err != nil {
		return err
	}
	defer closeRepo()

	report, err := syncbundle.Import(repo, blob)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}
	fmt.Println("✅ Bundle imported:")
	fmt.Println(report.String())
	return nil
}
