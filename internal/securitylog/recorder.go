// 9RouterX — this file is part of a port maintained by https://github.com/yanstd888/ — https://t.me/aiclub_id
package securitylog

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"9routerx/proxy/internal/db"
	"9routerx/proxy/internal/log"
)

// Default and boundary limits for security event retention.
const (
	DefaultRetention = 500
	MaxRetention     = 2000
	BufferCapacity   = 1024
)

// Entry is a normalized security event.
type Entry struct {
	ID        int64  `json:"id"`
	At        string `json:"at"`
	Type      string `json:"type"`
	Severity  string `json:"severity"` // "info", "warn", "critical"
	IP        string `json:"ip"`
	Actor     string `json:"actor,omitempty"`
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`
	Status    int    `json:"status,omitempty"`
	Detail    string `json:"detail,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`

	Level          string `json:"level,omitempty"`
	Classification string `json:"classification,omitempty"`
	Label          string `json:"label,omitempty"`
}

// Normalize ensures string bounds and required field defaults.
func (e Entry) Normalize() Entry {
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339)
	}
	if e.Type == "" {
		e.Type = "unknown"
	}
	if len(e.Type) > 40 {
		e.Type = e.Type[:40]
	}
	sev := strings.ToLower(e.Severity)
	if sev != "warn" && sev != "critical" {
		sev = "info"
	}
	e.Severity = sev
	if e.IP == "" {
		e.IP = "unknown"
	}
	if len(e.IP) > 64 {
		e.IP = e.IP[:64]
	}
	if len(e.Actor) > 120 {
		e.Actor = e.Actor[:120]
	}
	e.Method = strings.ToUpper(e.Method)
	if len(e.Method) > 8 {
		e.Method = e.Method[:8]
	}
	if len(e.Path) > 300 {
		e.Path = e.Path[:300]
	}
	if len(e.Detail) > 400 {
		e.Detail = e.Detail[:400]
	}
	if len(e.UserAgent) > 200 {
		e.UserAgent = e.UserAgent[:200]
	}
	return e
}

// Recorder manages asynchronous logging with a buffered channel and drop-oldest overflow strategy.
type Recorder struct {
	repo      *db.Repo
	ch        chan Entry
	retention int
	stopCh    chan struct{}
	doneCh    chan struct{}
	dropped   atomic.Uint64
	syncCh    chan chan struct{}
}

var (
	defaultMu       sync.RWMutex
	defaultRecorder *Recorder
)

// DefaultRecorder returns the globally configured security log recorder.
func DefaultRecorder() *Recorder {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultRecorder
}

// SetDefaultRecorder sets the global recorder instance.
func SetDefaultRecorder(r *Recorder) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultRecorder = r
}

// NewRecorder starts an async security log worker.
func NewRecorder(repo *db.Repo, retention int) *Recorder {
	if retention <= 0 || retention > MaxRetention {
		retention = DefaultRetention
	}
	r := &Recorder{
		repo:      repo,
		ch:        make(chan Entry, BufferCapacity),
		retention: retention,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		syncCh:    make(chan chan struct{}),
	}
	go r.worker()
	return r
}

// Record queues an event non-blocking. If the buffer is full, the oldest event is dropped.
func (r *Recorder) Record(e Entry) {
	if r == nil {
		return
	}
	e = e.Normalize()
	select {
	case r.ch <- e:
	default:
		// Drop oldest item to guarantee non-blocking execution under heavy load
		select {
		case <-r.ch:
			r.dropped.Add(1)
		default:
		}
		select {
		case r.ch <- e:
		default:
			r.dropped.Add(1)
		}
	}
}

// Record emits an event to the default recorder, if configured.
func Record(e Entry) {
	if rec := DefaultRecorder(); rec != nil {
		rec.Record(e)
	}
}

// DroppedCount returns the count of dropped events due to buffer saturation.
func (r *Recorder) DroppedCount() uint64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// Flush blocks until all currently queued events are written to the database.
func (r *Recorder) Flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	ack := make(chan struct{})
	select {
	case r.syncCh <- ack:
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the background worker gracefully.
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	close(r.stopCh)
	<-r.doneCh
}

func (r *Recorder) worker() {
	defer close(r.doneCh)
	insertCount := 0

	for {
		select {
		case <-r.stopCh:
			// Drain remaining events before exiting
			for {
				select {
				case e := <-r.ch:
					r.persist(e)
				default:
					return
				}
			}

		case ack := <-r.syncCh:
			// Drain all pending events in channel before acknowledging
			for {
				select {
				case e := <-r.ch:
					r.persist(e)
				default:
					goto drained
				}
			}
		drained:
			close(ack)

		case e := <-r.ch:
			r.persist(e)
			insertCount++
			if insertCount >= 50 {
				insertCount = 0
				r.trim()
			}
		}
	}
}

func (r *Recorder) persist(e Entry) {
	if r.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := r.repo.InsertSecurityLog(
		ctx, e.At, e.Type, e.Severity, e.IP, e.Actor, e.Method, e.Path, e.Status, e.Detail, e.UserAgent,
	); err != nil {
		log.Warn("securitylog", "failed to persist event", "error", err)
	}
}

func (r *Recorder) trim() {
	if r.repo == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.repo.TrimSecurityLog(ctx, r.retention); err != nil {
		log.Warn("securitylog", "failed to trim log", "error", err)
	}
}

// Enrich applies classification to a list of raw db entries.
func Enrich(entries []db.SecurityLogEntry) []Entry {
	res := make([]Entry, len(entries))
	for i, row := range entries {
		e := Entry{
			ID:        row.ID,
			At:        row.At,
			Type:      row.Type,
			Severity:  row.Severity,
			IP:        row.IP,
			Actor:     row.Actor,
			Method:    row.Method,
			Path:      row.Path,
			Status:    row.Status,
			Detail:    row.Detail,
			UserAgent: row.UserAgent,
		}
		v := Classify(e)
		e.Level = v.Level
		e.Classification = v.Classification
		e.Label = v.Label
		res[i] = e
	}
	return res
}
