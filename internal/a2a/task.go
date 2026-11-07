// 9RouterX — source port and modifications by https://github.com/yanstd888/ — https://t.me/aiclub_id
package a2a

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Task states, in lifecycle order: submitted → working → completed | failed |
// canceled. The last three are terminal.
const (
	StateSubmitted = "submitted"
	StateWorking   = "working"
	StateCompleted = "completed"
	StateFailed    = "failed"
	StateCanceled  = "canceled"
)

// Artifact is one skill result attached to a task. Data carries the skill's
// payload verbatim, so a caller gets structured JSON rather than a string it
// has to parse back.
type Artifact struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	Data any    `json:"data,omitempty"`
}

// Task is one A2A task. Every field is safe to read after the call that owns
// the task returns; transitions go through the TaskManager, so the sweeper and
// tasks/get never race a handler mutating state.
type Task struct {
	ID        string         `json:"id"`
	Skill     string         `json:"skill,omitempty"`
	State     string         `json:"state"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Error     *rpcError      `json:"error,omitempty"`
}

// Terminal reports whether the task has reached a state it cannot leave.
func (t *Task) Terminal() bool {
	switch t.State {
	case StateCompleted, StateFailed, StateCanceled:
		return true
	}
	return false
}

// TaskManager owns the task map and its expiry. The map is the only shared
// mutable state in the package, so one mutex covers it: tasks are small and
// every operation is O(1) apart from the sweep and list.
type TaskManager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	ttl   time.Duration
	stop  chan struct{}
	once  sync.Once
}

// NewTaskManager builds a manager with the given TTL and starts the background
// sweeper. Close stops the sweeper.
func NewTaskManager(ttl time.Duration) *TaskManager {
	if ttl <= 0 {
		ttl = DefaultTaskTTL
	}
	m := &TaskManager{
		tasks: make(map[string]*Task),
		ttl:   ttl,
		stop:  make(chan struct{}),
	}
	go m.sweepLoop()
	return m
}

// Close stops the background sweeper.
func (m *TaskManager) Close() {
	m.once.Do(func() { close(m.stop) })
}

// Count reports how many tasks are currently held.
func (m *TaskManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tasks)
}

// Create registers a task in the submitted state and returns a copy of it.
func (m *TaskManager) Create(skill string) Task {
	now := time.Now().UTC()
	t := &Task{
		ID:        newTaskID(),
		Skill:     skill,
		State:     StateSubmitted,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.tasks[t.ID] = t
	m.mu.Unlock()
	return *t
}

// Get returns a copy of the task, or ok=false when the id is unknown or the
// task has already been swept.
func (m *TaskManager) Get(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// List returns the most recently updated tasks, newest first. limit <= 0 means
// the default page size.
func (m *TaskManager) List(limit int) []Task {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	m.mu.Lock()
	all := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		all = append(all, *t)
	}
	m.mu.Unlock()

	// Newest first: the newest tasks are the ones a caller is waiting on.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].UpdatedAt.After(all[j-1].UpdatedAt); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// Transition moves a task to a new state. Unknown ids are ignored: the task
// may have been swept while its handler was still running.
func (m *TaskManager) Transition(id, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.State = state
		t.UpdatedAt = time.Now().UTC()
	}
}

// Complete marks a task done and attaches the skill's artifacts.
func (m *TaskManager) Complete(id string, artifacts []Artifact, metadata map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.State = StateCompleted
		t.Artifacts = artifacts
		t.Metadata = metadata
		t.UpdatedAt = time.Now().UTC()
	}
}

// Fail marks a task failed and records the JSON-RPC error a caller would have
// received.
func (m *TaskManager) Fail(id string, e *rpcError) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		t.State = StateFailed
		t.Error = e
		t.UpdatedAt = time.Now().UTC()
	}
}

// Cancel moves a non-terminal task to canceled and returns the resulting task.
// A task that already reached a terminal state is returned unchanged, so a
// repeated cancel is a no-op rather than an error.
func (m *TaskManager) Cancel(id string) (Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return Task{}, false
	}
	if !t.Terminal() {
		t.State = StateCanceled
		t.UpdatedAt = time.Now().UTC()
	}
	return *t, true
}

// Sweep drops tasks that reached a terminal state more than one TTL ago.
// Non-terminal tasks are kept: a handler still running must stay queryable.
func (m *TaskManager) Sweep(now time.Time) int {
	cutoff := now.Add(-m.ttl)
	m.mu.Lock()
	defer m.mu.Unlock()
	dropped := 0
	for id, t := range m.tasks {
		if t.Terminal() && t.UpdatedAt.Before(cutoff) {
			delete(m.tasks, id)
			dropped++
		}
	}
	return dropped
}

// sweepLoop runs Sweep on a timer until Close.
func (m *TaskManager) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.Sweep(time.Now())
		}
	}
}

// newTaskID returns a random hex id. crypto/rand rather than a counter so ids
// are not guessable from the outside, and not a UUID dependency for the same
// reason the rest of this package has none.
func newTaskID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand failing means the platform is broken; a time-based id
		// keeps the surface working rather than refusing every delegation.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return hex.EncodeToString(buf[:])
}
