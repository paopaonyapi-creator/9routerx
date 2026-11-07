// 9RouterX — derived from the work of https://github.com/yanstd888/ — port and modifications by the maintainers — https://t.me/aiclub_id
package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"9routerx/proxy/internal/config"
	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
)

// BackupItem represents a single saved backup file on disk.
type BackupItem struct {
	Filename  string    `json:"filename"`
	Path      string    `json:"path"`
	SizeBytes int64     `json:"sizeBytes"`
	Format    string    `json:"format"` // "sqlite" or "json"
	CreatedAt time.Time `json:"createdAt"`
}

// Manager orchestrates periodic and on-demand database backups and rotation.
type Manager struct {
	repo   *db.Repo
	now    func() time.Time
	stopCh chan struct{}
	doneCh chan struct{}
	mu     sync.Mutex
}

// NewManager creates a backup manager for the given repository.
func NewManager(repo *db.Repo) *Manager {
	return &Manager{
		repo:   repo,
		now:    time.Now,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// SetClock allows tests to simulate time progression.
func (m *Manager) SetClock(nowFn func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = nowFn
}

// ResolveBackupDir returns the directory where backups are stored.
func (m *Manager) ResolveBackupDir(custom string) string {
	if strings.TrimSpace(custom) != "" {
		return custom
	}
	return filepath.Join(config.ResolveDataDir(), "backups")
}

// BackupResult carries paths of generated backup files.
type BackupResult struct {
	SQLitePath string    `json:"sqlitePath,omitempty"`
	JSONPath   string    `json:"jsonPath,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	SizeBytes  int64     `json:"sizeBytes"`
}

// CreateBackup creates an immediate SQLite point-in-time snapshot using VACUUM INTO,
// and rotates old backups exceeding the configured retention.
func (m *Manager) CreateBackup(ctx context.Context, jsonExporter func(context.Context) ([]byte, error)) (*BackupResult, error) {
	if m.repo == nil {
		return nil, fmt.Errorf("backup manager: nil repo")
	}

	cfg, err := m.repo.GetAutoBackupConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read backup config: %w", err)
	}

	dir := m.ResolveBackupDir(cfg.Destination)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create backup dir %s: %w", dir, err)
	}

	now := m.now().UTC()
	stamp := now.Format("2006-01-02-150405")
	sqliteFile := filepath.Join(dir, fmt.Sprintf("backup-%s.sqlite", stamp))

	if err := m.repo.VacuumInto(ctx, sqliteFile); err != nil {
		status := db.AutoBackupStatus{
			LastBackupAt: now.Format(time.RFC3339),
			LastStatus:   "error",
			LastError:    err.Error(),
		}
		_ = m.repo.SetAutoBackupStatus(ctx, status)
		return nil, err
	}

	fi, err := os.Stat(sqliteFile)
	if err != nil {
		return nil, fmt.Errorf("stat sqlite backup: %w", err)
	}
	totalBytes := fi.Size()

	var jsonFile string
	if jsonExporter != nil {
		jsonData, expErr := jsonExporter(ctx)
		if expErr == nil && len(jsonData) > 0 {
			jsonFile = filepath.Join(dir, fmt.Sprintf("backup-%s.json", stamp))
			if err := os.WriteFile(jsonFile, jsonData, 0600); err == nil {
				if jfi, sErr := os.Stat(jsonFile); sErr == nil {
					totalBytes += jfi.Size()
				}
			}
		}
	}

	// Rotate backups exceeding retention limit
	m.rotateBackups(dir, cfg.Retention)

	// Update status
	status := db.AutoBackupStatus{
		LastBackupAt:    now.Format(time.RFC3339),
		LastBackupFile:  filepath.Base(sqliteFile),
		LastBackupBytes: totalBytes,
		LastStatus:      "success",
		LastError:       "",
	}
	_ = m.repo.SetAutoBackupStatus(ctx, status)

	return &BackupResult{
		SQLitePath: sqliteFile,
		JSONPath:   jsonFile,
		CreatedAt:  now,
		SizeBytes:  totalBytes,
	}, nil
}

// rotateBackups prunes oldest backup files beyond the retention count.
func (m *Manager) rotateBackups(dir string, retention int) {
	if retention <= 0 {
		retention = 7
	}
	items, err := m.ListBackups(dir)
	if err != nil {
		return
	}

	// Filter unique snapshots by prefix (e.g. backup-2006-01-02-150405)
	snapshots := make(map[string][]BackupItem)
	order := make([]string, 0)
	for _, item := range items {
		base := strings.TrimSuffix(item.Filename, filepath.Ext(item.Filename))
		if _, exists := snapshots[base]; !exists {
			order = append(order, base)
		}
		snapshots[base] = append(snapshots[base], item)
	}

	if len(order) > retention {
		toDelete := order[retention:]
		for _, base := range toDelete {
			for _, file := range snapshots[base] {
				_ = os.Remove(file.Path)
			}
		}
	}
}

// ListBackups returns all available backup files sorted newest first.
func (m *Manager) ListBackups(customDir string) ([]BackupItem, error) {
	dir := m.ResolveBackupDir(customDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}

	var items []BackupItem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".sqlite" && ext != ".json" {
			continue
		}
		if !strings.HasPrefix(name, "backup-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		format := "sqlite"
		if ext == ".json" {
			format = "json"
		}
		items = append(items, BackupItem{
			Filename:  name,
			Path:      filepath.Join(dir, name),
			SizeBytes: info.Size(),
			Format:    format,
			CreatedAt: info.ModTime().UTC(),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})

	return items, nil
}

// Start launches a periodic auto-backup scheduler checking hourly.
func (m *Manager) Start(jsonExporter func(context.Context) ([]byte, error)) {
	go func() {
		defer close(m.doneCh)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.tick(jsonExporter)
			}
		}
	}()
}

// Stop terminates the backup manager scheduler.
func (m *Manager) Stop() {
	close(m.stopCh)
	<-m.doneCh
}

func (m *Manager) tick(jsonExporter func(context.Context) ([]byte, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cfg, err := m.repo.GetAutoBackupConfig(ctx)
	if err != nil || !cfg.Enabled {
		return
	}

	status, _ := m.repo.GetAutoBackupStatus(ctx)
	interval := time.Duration(cfg.IntervalHours) * time.Hour
	now := m.now().UTC()

	if status.LastBackupAt != "" {
		if last, err := time.Parse(time.RFC3339, status.LastBackupAt); err == nil {
			if now.Sub(last) < interval {
				return
			}
		}
	}

	if _, err := m.CreateBackup(ctx, jsonExporter); err != nil {
		log.Warn("backup", "periodic auto-backup failed", "error", err)
	}
}

// RestoreDatabase restores the database from a given file (.sqlite or .json).
func RestoreDatabase(ctx context.Context, repo *db.Repo, filePath string, jsonImporter func(context.Context, []byte) error) error {
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("backup file does not exist: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".sqlite":
		return repo.RestoreFromSQLiteBackup(ctx, filePath)
	case ".json":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read json backup: %w", err)
		}
		if jsonImporter != nil {
			return jsonImporter(ctx, data)
		}
		return fmt.Errorf("no json importer provided")
	default:
		return fmt.Errorf("unsupported backup format %q (expected .sqlite or .json)", ext)
	}
}
