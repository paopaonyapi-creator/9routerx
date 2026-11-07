// 9RouterX — ported & modified by https://github.com/yanstd888/ — https://t.me/aiclub_id
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/viper"

	"9routerx/proxy/internal/constants"
	"9routerx/proxy/internal/log"
)

// DefaultPort is the port the gateway binds when PORT is unset. It lives here
// so the validation error can name the fallback instead of a magic number.
const DefaultPort = 888

// Config holds the proxy gateway configuration.
type Config struct {
	Host                string
	Port                int
	DatabasePath        string
	JWTSecret           string
	InitialPassword     string
	RTKEnabled          bool
	CavemanEnabled      bool
	PonytailEnabled     bool
	ADHDEnabled         bool
	EnableWebExecutors  bool
	EnableA2A           bool
	EnableTLSStealth    bool
	SeatAttemptBudgetMs int

	// portRaw and dataDir keep the operator-supplied values behind the resolved
	// ones, so Validate can reject what the silent fallbacks below would
	// otherwise paper over. Unexported: they are audit input for Validate, not
	// settings anything else may act on.
	portRaw string
	dataDir string
}

// NewViper creates and configures a new Viper instance reading from .env with standard defaults.
func NewViper() *viper.Viper {
	return NewViperWithFile(".env")
}

// NewViperWithFile creates and configures a new Viper instance with the specified env file path.
func NewViperWithFile(configFile string) *viper.Viper {
	v := viper.New()
	if configFile != "" {
		v.SetConfigFile(configFile)
		v.SetConfigType("env")
	}

	v.AutomaticEnv()

	v.SetDefault("PORT", DefaultPort)
	v.SetDefault("RTK_ENABLED", true)
	v.SetDefault("CAVEMAN_ENABLED", false)
	v.SetDefault("PONYTAIL_ENABLED", false)
	v.SetDefault("ADHD_ENABLED", false)
	v.SetDefault("ENABLE_WEB_EXECUTORS", false)
	v.SetDefault("SEAT_ATTEMPT_BUDGET_MS", 0)

	if configFile != "" {
		if err := v.ReadInConfig(); err != nil {
			var configFileNotFoundError viper.ConfigFileNotFoundError
			if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) && !errors.As(err, &configFileNotFoundError) {
				log.Warn("config", "read config file failed", "file", configFile, "error", err)
			}
		}
	}

	return v
}

// ProvideViper returns a configured Viper instance for dependency injection.
func ProvideViper() *viper.Viper {
	return NewViper()
}

// ProvideConfig provides *Config for dependency injection using the provided Viper instance.
func ProvideConfig(v *viper.Viper) *Config {
	return LoadConfigFromViper(v)
}

// ResolveDataDir returns the base data directory: DATA_DIR env, else the
// value from .env, else the platform default (~/.9router, or
// %APPDATA%/9router on Windows).
//
// The .env read is load-bearing, not a convenience. The server resolves its
// data dir through viper (LoadConfigFromViper), which does read .env, while
// `9routerx status|stop|logs` runs in a separate short-lived process that
// never boots the server. Reading only os.Getenv here meant a deployment
// configured purely through .env — every Docker/compose setup — had the CLI
// look at a different directory than the running daemon: status reported "not
// running" against a live listener, stop refused to kill it, and logs claimed
// no log existed. Both sides now answer the same question the same way.
func ResolveDataDir() string {
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		return dataDir
	}
	if dataDir := dataDirFromEnvFile(); dataDir != "" {
		return dataDir
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		if runtime.GOOS == "windows" {
			appData := os.Getenv("APPDATA")
			if appData == "" {
				appData = filepath.Join(homeDir, "AppData", "Roaming")
			}
			return filepath.Join(appData, "9router")
		}
		return filepath.Join(homeDir, ".9router")
	}
	return ".9router"
}

// dataDirFromEnvFile reads DATA_DIR out of the .env next to the binary or in
// the working directory. Viper owns the file format, so this reuses it rather
// than hand-parsing KEY=VALUE and drifting from what the server accepted.
func dataDirFromEnvFile() string {
	for _, path := range envFileCandidates() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		v := viper.New()
		v.SetConfigFile(path)
		v.SetConfigType("env")
		if err := v.ReadInConfig(); err != nil {
			continue
		}
		if dir := strings.TrimSpace(v.GetString("DATA_DIR")); dir != "" {
			return dir
		}
	}
	return ""
}

// envFileCandidates lists the .env locations the server itself reads: the one
// in the working directory, then the one beside the executable so a daemon
// started from another directory still finds the same config its CLI reports on.
func envFileCandidates() []string {
	candidates := []string{".env"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ".env"))
	}
	return candidates
}

// LoadConfig loads the configuration from environment variables, .env file, and platform defaults using Viper.
func LoadConfig() *Config {
	return LoadConfigFromViper(NewViper())
}

// LoadConfigFromViper builds *Config using the given Viper instance.
func LoadConfigFromViper(v *viper.Viper) *Config {
	if v == nil {
		v = NewViper()
	}
	host := strings.TrimSpace(v.GetString("HOST"))
	if host == "" {
		host = strings.TrimSpace(v.GetString("BIND_ADDR"))
	}

	port := v.GetInt("PORT")
	if port <= 0 {
		port = DefaultPort // Default port (unified port)
	}

	dataDir := v.GetString("DATA_DIR")
	if dataDir == "" {
		dataDir = ResolveDataDir()
	}

	// Ensure the base data directory exists
	if err := os.MkdirAll(dataDir, constants.FilePermDir); err != nil {
		log.Warn("config", "create data dir failed", "dir", dataDir, "error", err)
	}

	// Database file: DB_PATH overrides default DATA_DIR/db/data.sqlite
	dbPath := v.GetString("DB_PATH")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "db", "data.sqlite")
	} else if fi, err := os.Stat(dbPath); err == nil && fi.IsDir() {
		if _, err := os.Stat(filepath.Join(dbPath, "db", "data.sqlite")); err == nil {
			dbPath = filepath.Join(dbPath, "db", "data.sqlite")
		} else if _, err := os.Stat(filepath.Join(dbPath, "data.sqlite")); err == nil {
			dbPath = filepath.Join(dbPath, "data.sqlite")
		} else if _, err := os.Stat(filepath.Join(dbPath, "9router.db")); err == nil {
			dbPath = filepath.Join(dbPath, "9router.db")
		} else {
			dbPath = filepath.Join(dbPath, "db", "data.sqlite")
		}
	}

	// INITIAL_PASSWORD has no hardcoded default — an empty value forces the
	// operator to set one explicitly rather than shipping a known password.
	initialPassword := v.GetString("INITIAL_PASSWORD")

	rtkEnabled := v.GetBool("RTK_ENABLED")
	cavemanEnabled := v.GetBool("CAVEMAN_ENABLED")
	ponytailEnabled := v.GetBool("PONYTAIL_ENABLED")
	adhdEnabled := v.GetBool("ADHD_ENABLED")
	enableWebExecutors := v.GetBool("ENABLE_WEB_EXECUTORS")
	enableA2A := v.GetBool("ROUTERX_ENABLE_A2A")
	enableTLSStealth := v.GetBool("ROUTERX_TLS_STEALTH")
	seatAttemptBudgetMs := v.GetInt("SEAT_ATTEMPT_BUDGET_MS")

	return &Config{
		Host:                host,
		Port:                port,
		DatabasePath:        dbPath,
		JWTSecret:           loadJWTSecret(v, dataDir),
		InitialPassword:     initialPassword,
		RTKEnabled:          rtkEnabled,
		CavemanEnabled:      cavemanEnabled,
		PonytailEnabled:     ponytailEnabled,
		ADHDEnabled:         adhdEnabled,
		EnableWebExecutors:  enableWebExecutors,
		EnableA2A:           enableA2A,
		EnableTLSStealth:    enableTLSStealth,
		SeatAttemptBudgetMs: seatAttemptBudgetMs,
		portRaw:             strings.TrimSpace(v.GetString("PORT")),
		dataDir:             dataDir,
	}
}

// Validate rejects operator-supplied settings the server cannot honor without
// quietly becoming a different deployment than the one the operator described.
// LoadConfig's silent fallbacks exist for the short-lived CLI helpers (menu,
// tray, status) that only want a port number; the server itself must instead
// stop and say which variable is wrong, so a typo'd PORT cannot quietly boot
// the gateway on 888 or a db directory cannot silently fail to exist.
//
// Each error names the variable and the fix, and is meant to be printed as a
// single line — no stack, no dependency-graph dump.
func (c *Config) Validate() error {
	if raw := c.portRaw; raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid PORT %q: must be a number between 1 and 65535 — unset PORT (or remove it from .env) to use the default %d", raw, DefaultPort)
		}
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid PORT %d: must be between 1 and 65535 — unset PORT (or remove it from .env) to use the default %d", port, DefaultPort)
		}
	}

	if fi, err := os.Stat(c.dataDir); err == nil && !fi.IsDir() {
		return fmt.Errorf("invalid DATA_DIR %q: the path exists as a file — set DATA_DIR to a directory path", c.dataDir)
	}
	if err := os.MkdirAll(c.dataDir, constants.FilePermDir); err != nil {
		return fmt.Errorf("invalid DATA_DIR %q: cannot create the directory (%v) — set DATA_DIR to a writable directory path", c.dataDir, err)
	}
	return nil
}

// ValidateEnvFile reports whether the .env the server is about to load can
// actually be parsed. NewViperWithFile only warns on a parse failure and then
// proceeds with every setting in the file silently discarded — an operator
// whose .env has one broken line boots on the default port with no
// INITIAL_PASSWORD and no data dir they chose. The server calls this before
// boot so the file is either honored or refused, never half-read.
//
// A missing file is fine: env vars and defaults carry the configuration then.
func ValidateEnvFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("env")
	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("invalid config file %s: %v — fix or remove the file; while it cannot be parsed, none of its settings apply", path, err)
	}
	return nil
}

func loadJWTSecret(v *viper.Viper, dataDir string) string {
	var secret string
	if v != nil {
		secret = v.GetString("JWT_SECRET")
	}
	if secret == "" {
		secret = os.Getenv("JWT_SECRET")
	}
	if secret != "" {
		return secret
	}

	secretFile := filepath.Join(dataDir, "jwt-secret")
	data, err := os.ReadFile(secretFile)
	if err == nil {
		return string(data)
	}

	// Generate 32 cryptographically secure random bytes
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Error("config", "crypto/rand failed to generate JWT secret; refusing to fall back to a static secret", "error", err)
		return ""
	}

	generated := hex.EncodeToString(bytes)
	if err := os.WriteFile(secretFile, []byte(generated), constants.FilePermKey); err != nil {
		log.Warn("config", "write JWT secret failed", "file", secretFile, "error", err)
	}
	return generated
}
