package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"syscall"
	"time"
)

// RunStatus is the public-facing lifecycle state of a run.
type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

// Run is one invocation of twincut.sh. The run goroutine owns reads from the
// child's stdout NDJSON stream, journaling, and broadcasting to subscribers.
// Public methods on Run are safe for concurrent callers.
type Run struct {
	ID        string
	Mode      string
	Args      []string
	StartedAt time.Time
	EndedAt   time.Time
	ExitCode  int

	mu      sync.RWMutex
	status  RunStatus
	events  []Event              // canonical history, in seq order
	subs    map[string]chan Event // SSE subscriber channels
	done    chan struct{}        // closed when the run finishes
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	journal *os.File
}

// Snapshot is a point-in-time view of a run, safe to JSON-encode.
type Snapshot struct {
	ID        string    `json:"id"`
	Mode      string    `json:"mode"`
	Status    RunStatus `json:"status"`
	Args      []string  `json:"args"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	ExitCode  int       `json:"exit_code"`
	NumEvents int       `json:"num_events"`
}

// Snapshot returns a copy of the run's metadata under the read lock.
func (r *Run) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return Snapshot{
		ID:        r.ID,
		Mode:      r.Mode,
		Status:    r.status,
		Args:      append([]string(nil), r.Args...),
		StartedAt: r.StartedAt,
		EndedAt:   r.EndedAt,
		ExitCode:  r.ExitCode,
		NumEvents: len(r.events),
	}
}

// Status returns the current lifecycle state.
func (r *Run) Status() RunStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// Done returns a channel that is closed when the run finishes (success,
// failure, or cancellation).
func (r *Run) Done() <-chan struct{} { return r.done }

// EventsSince returns all events with seq strictly greater than `since`,
// in order. Pass since=0 to get the full history.
func (r *Run) EventsSince(since int) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if since >= len(r.events) {
		return nil
	}
	out := make([]Event, len(r.events)-since)
	copy(out, r.events[since:])
	return out
}

// Subscribe registers a channel that receives every event going forward
// (events emitted before Subscribe was called are NOT replayed — call
// EventsSince(0) first if you want the full history). The returned function
// must be invoked to unregister.
//
// The channel is buffered. If the consumer can't keep up, events are
// dropped and the consumer should reconcile via EventsSince on reconnect.
func (r *Run) Subscribe() (string, <-chan Event, func()) {
	id := mustRandID()
	ch := make(chan Event, 64)
	r.mu.Lock()
	if r.subs == nil {
		r.subs = make(map[string]chan Event)
	}
	r.subs[id] = ch
	r.mu.Unlock()
	return id, ch, func() {
		r.mu.Lock()
		if c, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(c)
		}
		r.mu.Unlock()
	}
}

// Cancel sends SIGTERM to the run's process group. Returns true if a signal
// was actually sent (i.e. the run was still alive).
func (r *Run) Cancel() bool {
	r.mu.RLock()
	cmd := r.cmd
	st := r.status
	r.mu.RUnlock()
	if st != RunStatusRunning || cmd == nil || cmd.Process == nil {
		return false
	}
	// Negative pid → kill the whole process group (Setpgid'd at spawn).
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		log.Printf("run %s: cancel kill: %v", r.ID, err)
		return false
	}
	r.mu.Lock()
	r.status = RunStatusCancelled
	r.mu.Unlock()
	return true
}

// append records an event into the run's history under the write lock and
// fans it out to all subscribers. Slow subscribers drop events rather than
// block the run goroutine.
func (r *Run) append(ev Event) {
	r.mu.Lock()
	ev.Seq = len(r.events) + 1
	r.events = append(r.events, ev)
	subs := make([]chan Event, 0, len(r.subs))
	for _, c := range r.subs {
		subs = append(subs, c)
	}
	r.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- ev:
		default:
			// Subscriber is slow; drop. SSE handler will reconcile via
			// EventsSince on reconnect.
		}
	}
}

// closeSubs closes all subscriber channels. Called once when the run
// terminates so SSE handlers exit their range loops.
func (r *Run) closeSubs() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, c := range r.subs {
		close(c)
		delete(r.subs, id)
	}
}

// ----------------------------------------------------------------------------
// RunManager
// ----------------------------------------------------------------------------

// RunManager owns the active and historical runs in memory. It also persists
// per-run NDJSON event journals under <state-dir>/runs/<id>.ndjson so the
// History tab can reconstruct past runs after a server restart.
type RunManager struct {
	stateDir    string
	twincutPath string

	mu   sync.RWMutex
	runs map[string]*Run

	// SpawnHook, if non-nil, is invoked with each StartOptions before exec.
	// Used by tests to assert argv/stdin without spawning a real process.
	// When set, Start() returns a synthetic *Run without running the command.
	SpawnHook func(StartOptions)
}

// NewRunManager constructs a manager rooted at stateDir. twincutPath is the
// resolved path to twincut.sh (see LocateTwincut).
func NewRunManager(stateDir, twincutPath string) (*RunManager, error) {
	if err := os.MkdirAll(filepath.Join(stateDir, "runs"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir runs/: %w", err)
	}
	return &RunManager{
		stateDir:    stateDir,
		twincutPath: twincutPath,
		runs:        make(map[string]*Run),
	}, nil
}

// Get returns the run with the given id, or nil.
func (m *RunManager) Get(id string) *Run {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runs[id]
}

// List returns snapshots of every run currently held in memory, newest first.
func (m *RunManager) List() []Snapshot {
	m.mu.RLock()
	runs := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.RUnlock()
	snaps := make([]Snapshot, 0, len(runs))
	for _, r := range runs {
		snaps = append(snaps, r.Snapshot())
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].StartedAt.After(snaps[j].StartedAt)
	})
	return snaps
}

var runIDRegex = regexp.MustCompile(`^\d{8}T\d{6}Z-[a-z0-9]+$`)

// StartOptions describes a new run to spawn.
type StartOptions struct {
	// ID is optional; empty → newRunID(). If non-empty, must match
	// ^\d{8}T\d{6}Z-[a-z0-9]+$ and not collide with an existing journal.
	ID string
	// Mode is a free-form label used for display only — twincut.sh decides
	// the actual mode from Args. Known values:
	//   self_check_preview, self_check_apply
	//   cross_check_preview, cross_check_apply
	//   thumbnail_detect_preview, thumbnail_detect_apply
	//   restore
	Mode string
	// Args is appended to the base invocation. The manager prepends
	// --json-events automatically and sets TWINCUT_RUN_ID in the env.
	Args []string
	// Env are extra environment variables appended to the inherited env.
	Env []string
	// Stdin is an optional reader piped to the spawned process's stdin.
	// Used by Stage 9's apply mode to stream ApplyCommand JSON-lines.
	Stdin io.Reader
}

// Start spawns a new twincut.sh run and returns the Run. The returned Run is
// already registered with the manager and its event-pump goroutine is
// running.
func (m *RunManager) Start(opts StartOptions) (*Run, error) {
	var id string
	if opts.ID == "" {
		id = newRunID()
	} else {
		if !runIDRegex.MatchString(opts.ID) {
			return nil, fmt.Errorf("invalid caller-provided run ID: %q", opts.ID)
		}
		journalCheckPath := filepath.Join(m.stateDir, "runs", opts.ID+".ndjson")
		if _, err := os.Stat(journalCheckPath); err == nil {
			return nil, fmt.Errorf("run journal already exists for ID: %q", opts.ID)
		}
		id = opts.ID
	}

	journalPath := filepath.Join(m.stateDir, "runs", id+".ndjson")
	journal, err := os.OpenFile(journalPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_EXCL, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open journal %s: %w", journalPath, err)
	}

	// SpawnHook seam: tests can intercept before any process is spawned.
	if m.SpawnHook != nil {
		m.SpawnHook(opts)
		r := &Run{
			ID:        id,
			Mode:      opts.Mode,
			Args:      append([]string(nil), opts.Args...),
			StartedAt: time.Now(),
			status:    RunStatusSucceeded,
			done:      make(chan struct{}),
			journal:   journal,
		}
		close(r.done)
		_ = journal.Close()
		m.mu.Lock()
		m.runs[id] = r
		m.mu.Unlock()
		return r, nil
	}

	args := append([]string{"--json-events"}, opts.Args...)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "bash", append([]string{m.twincutPath}, args...)...)
	cmd.Env = append(os.Environ(), append(opts.Env, "TWINCUT_RUN_ID="+id)...)
	// Put the child in its own process group so Cancel can SIGTERM the
	// whole tree (twincut.sh + ffprobe etc.).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = journal.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = journal.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		_ = journal.Close()
		return nil, fmt.Errorf("start twincut.sh: %w", err)
	}

	r := &Run{
		ID:        id,
		Mode:      opts.Mode,
		Args:      append([]string(nil), opts.Args...),
		StartedAt: time.Now(),
		status:    RunStatusRunning,
		done:      make(chan struct{}),
		cmd:       cmd,
		cancel:    cancel,
		journal:   journal,
	}

	m.mu.Lock()
	m.runs[id] = r
	m.mu.Unlock()

	// Stderr goes to the server log (becomes the "Show log ▾" panel
	// content in stage 4+). Capture in a separate goroutine; failures
	// are non-fatal.
	go drainStderr(id, stderr)
	go m.pump(r, stdout)

	return r, nil
}

// pump reads NDJSON from the child's stdout, parses each line, journals it,
// and broadcasts it. Returns when the child closes stdout (which happens
// when twincut.sh exits).
func (m *RunManager) pump(r *Run, stdout io.ReadCloser) {
	defer func() {
		// Wait for child exit, record outcome, close subscribers.
		err := r.cmd.Wait()
		_ = r.journal.Close()

		r.mu.Lock()
		r.EndedAt = time.Now()
		r.ExitCode = r.cmd.ProcessState.ExitCode()
		// If Cancel already set Cancelled, keep that. Otherwise derive
		// from the exit code.
		if r.status == RunStatusRunning {
			if err == nil {
				r.status = RunStatusSucceeded
			} else {
				r.status = RunStatusFailed
			}
		}
		r.mu.Unlock()

		r.closeSubs()
		close(r.done)
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // dup_group lines can be long
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := ParseEvent(line)
		if err != nil {
			log.Printf("run %s: skipping malformed event line: %v", r.ID, err)
			continue
		}
		// Journal the original wire form (one JSON line, no transformation).
		if _, werr := r.journal.Write(append(append([]byte{}, line...), '\n')); werr != nil {
			log.Printf("run %s: journal write: %v", r.ID, werr)
		}
		r.append(ev)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		log.Printf("run %s: stdout scan: %v", r.ID, err)
	}
}

// drainStderr forwards stderr to the server log with a per-run prefix. In
// later stages this also feeds the "Show log ▾" panel.
func drainStderr(runID string, stderr io.ReadCloser) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		log.Printf("[%s/stderr] %s", shortID(runID), scanner.Text())
	}
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func newRunID() string {
	// Sortable + unique enough for a single-user local server.
	return time.Now().UTC().Format("20060102T150405Z") + "-" + mustRandID()
}

func mustRandID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("rand: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}
