// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/urfave/cli/v2"

	"9routerx/proxy/internal/config"
)

// Service argv keeps the effective port/data location across supervisor logins.
func applyServiceConfig(c *cli.Context) error {
	for _, key := range []struct{ flag, env string }{{"port", "PORT"}, {"data-dir", "DATA_DIR"}, {"db-path", "DB_PATH"}} {
		if !c.IsSet(key.flag) {
			continue
		}
		value := c.String(key.flag)
		if key.flag == "port" {
			value = strconv.Itoa(c.Int(key.flag))
		}
		if err := os.Setenv(key.env, value); err != nil {
			return fmt.Errorf("service configuration: %w", err)
		}
	}
	return nil
}
func serviceArgs(c *cli.Context) ([]string, error) {
	cfg := config.LoadConfig()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	data, err := filepath.Abs(config.ResolveDataDir())
	if err != nil {
		return nil, err
	}
	db, err := filepath.Abs(cfg.DatabasePath)
	if err != nil {
		return nil, err
	}
	return append(forwardArgs(c), "--service-process", "--port="+strconv.Itoa(cfg.Port), "--data-dir="+data, "--db-path="+db), nil
}
