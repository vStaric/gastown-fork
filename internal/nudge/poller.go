// poller.go provides a background nudge-queue poller for agents that lack
// turn-boundary drain hooks (e.g., Gemini, Codex). Claude Code drains its
// queue via the UserPromptSubmit hook on every turn. Other runtimes have no
// equivalent hook, so queued nudges would sit undelivered forever.
//
// The poller runs as a background goroutine launched by crew/manager.Start().
// It polls the queue every PollInterval, waits for the agent to be idle, then
// drains and injects the formatted nudges via tmux NudgeSession.
//
// Lifecycle: StartPoller() → background loop → StopPoller() (or session death).
// A PID file at <townRoot>/.runtime/nudge_poller/<session>.pid allows Stop()
// to clean up even if the original manager has been replaced.
package nudge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/util"
)

// Poller tuning defaults (overridable via flags or tests).
var (
	// DefaultPollInterval is how often the poller checks the queue.
	DefaultPollInterval = "10s"
	// DefaultIdleTimeout is how long to wait for the agent to become idle
	// before skipping this poll cycle and trying again next interval.
	DefaultIdleTimeout = "3s"
)

// pollerPidDir returns the directory for poller PID files.
func pollerPidDir(townRoot string) string {
	return filepath.Join(townRoot, constants.DirRuntime, "nudge_poller")
}

// pollerPidFile returns the PID file path for a session's poller.
func pollerPidFile(townRoot, session string) string {
	safe := strings.ReplaceAll(session, "/", "_")
	return filepath.Join(pollerPidDir(townRoot), safe+".pid")
}

// StartPoller launches a background `gt nudge-poller <session>` process.
// The process is detached (Setpgid) so it survives the caller's exit.
// Returns the PID of the launched process, or an error.
func StartPoller(townRoot, session string) (int, error) {
	pidDir := pollerPidDir(townRoot)
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		return 0, fmt.Errorf("creating poller pid dir: %w", err)
	}

	// Check if a poller is already running for this session.
	if pid, alive := pollerAlive(townRoot, session); alive {
		return pid, nil // already running
	}

	// Find the gt binary.
	gtBin, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding gt binary: %w", err)
	}

	// DOUBLE-FORK so the poller is adopted by init regardless of who spawned it.
	//
	// Previously the poller was a direct child and only reached ppid=1 because the
	// spawner happened to exit — true for short-lived commands like `gt crew start`,
	// FALSE for the long-lived daemon. A daemon-spawned poller stayed a daemon child,
	// and when it died nothing reaped it: it sat defunct for 25+ hours while 41
	// queued nudges piled up (hq-8xac / hq-7onb).
	//
	// Setsid alone does NOT fix this — measured: a Setsid child of a live,
	// non-reaping parent still shows STAT=Z with ppid=<spawner>. The parent has to
	// stop being the spawner, which is what the intermediate accomplishes: it starts
	// the poller and exits at once, so init inherits and reaps it.
	pid, err := spawnDetachedPoller(gtBin, townRoot, session)
	if err != nil {
		return 0, err
	}

	// Write PID file for later cleanup.
	pidPath := pollerPidFile(townRoot, session)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		// Non-fatal — the process is running, we just can't track it.
		fmt.Fprintf(os.Stderr, "Warning: failed to write poller PID file: %v\n", err)
	}

	// Record the spawn. Nothing logged poller starts, which made a live poller's
	// origin unknowable after the fact: on hq-8xac the deacon's working poller
	// (pid 94170) could not be attributed to any spawner, because its parent had
	// already exited and no log line named it. Two agents independently
	// mis-attributed it — first to the daemon, then to launchd — from ppid=1
	// alone. One line answers that in seconds.
	fmt.Fprintf(os.Stderr, "nudge-poller: started pid %d for %s\n", pid, session)

	return pid, nil
}

// SpawnPollerProcess starts the real poller process and returns its pid without
// waiting for it. Called ONLY by the `--spawn-detached` intermediate stage, which
// exits straight after so the poller reparents to init (hq-7onb).
func SpawnPollerProcess(session string) (int, error) {
	gtBin, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("finding gt binary: %w", err)
	}
	townRoot, err := os.Getwd()
	if err != nil {
		return 0, fmt.Errorf("resolving town root: %w", err)
	}
	cmd := buildPollerCommand(gtBin, townRoot, session)
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting nudge-poller: %w", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// spawnDetachedPoller starts the nudge-poller through a short-lived intermediate
// so the poller reparents to init. Returns the POLLER's pid, not the
// intermediate's — the intermediate is gone by the time this returns.
func spawnDetachedPoller(gtBin, townRoot, session string) (int, error) {
	inner := buildPollerCommand(gtBin, townRoot, session)

	// The intermediate prints the poller's pid and exits immediately, so Wait()
	// here is cheap and cannot hang on the long-running poller itself.
	outer := exec.Command(gtBin, "nudge-poller", session, "--spawn-detached")
	outer.Dir = townRoot
	util.SetDetachedProcessGroup(outer)
	out, err := outer.Output()
	if err == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(out))); convErr == nil && pid > 0 {
			return pid, nil
		}
	}

	// Fall back to a direct spawn if the intermediate is unavailable (older binary
	// on PATH, unexpected output). Behaviour then matches the previous code —
	// correct, just without the reparenting guarantee.
	if startErr := inner.Start(); startErr != nil {
		return 0, fmt.Errorf("starting nudge-poller: %w", startErr)
	}
	pid := inner.Process.Pid
	_ = inner.Process.Release()
	return pid, nil
}

func buildPollerCommand(gtBin, townRoot, session string) *exec.Cmd {
	cmd := exec.Command(gtBin, "nudge-poller", session)
	cmd.Dir = townRoot
	cmd.Stdout = nil // discard
	cmd.Stderr = nil // discard
	util.SetDetachedProcessGroup(cmd)
	return cmd
}

// StopPoller terminates the nudge-poller for a session, if running.
func StopPoller(townRoot, session string) error {
	pidPath := pollerPidFile(townRoot, session)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no poller to stop
		}
		return fmt.Errorf("reading poller PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return nil // corrupt PID file, clean up
	}

	if !pollerProcessAlive(pid) {
		// Process already dead.
		_ = os.Remove(pidPath)
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		_ = os.Remove(pidPath)
		return nil
	}

	// Send SIGTERM for graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("sending SIGTERM to poller (pid %d): %w", pid, err)
	}

	_ = os.Remove(pidPath)
	return nil
}

// pollerAlive checks if a poller is running for the given session.
// Returns the PID and whether the process is alive.
func pollerAlive(townRoot, session string) (int, bool) {
	pidPath := pollerPidFile(townRoot, session)

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}

	if !pollerProcessAlive(pid) {
		// Stale PID file — clean up.
		_ = os.Remove(pidPath)
		return 0, false
	}

	return pid, true
}

// Watcher provides a filesystem-event-driven interface to the nudge queue.
// This is an ACP-safe alternative to polling and is preferred for long-running
// watchers like ACP Propeller.
type Watcher struct {
	townRoot string
	session  string
	dir      string
	closed   chan struct{}
	wg       sync.WaitGroup
	events   chan struct{}
}

// NewWatcher creates a new watcher for the given town root and session.
// The watcher observes nudge queue writes and signals via the Events() channel.
func NewWatcher(townRoot, session string) (*Watcher, error) {
	dir := queueDir(townRoot, session)
	// Ensure the directory exists so watch can start immediately.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating nudge queue dir: %w", err)
	}

	w := &Watcher{
		townRoot: townRoot,
		session:  session,
		dir:      dir,
		closed:   make(chan struct{}),
		events:   make(chan struct{}, 1), // buffer one signal for coalescing
	}

	w.wg.Add(1)
	go w.watch()
	return w, nil
}

// Events returns a channel that receives a struct{} when the queue may have
// changed. Multiple changes within a short window are coalesced.
func (w *Watcher) Events() <-chan struct{} {
	return w.events
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	select {
	case <-w.closed:
		return fmt.Errorf("watcher already closed")
	default:
	}
	close(w.closed)
	w.wg.Wait()
	return nil
}

func (w *Watcher) watch() {
	defer w.wg.Done()

	// Use fsnotify directly.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Log but don't block; fallback behavior is explicit in callers.
		fmt.Fprintf(os.Stderr, "nudge watcher init failed for %s: %v\n", w.dir, err)
		return
	}
	defer func() { _ = watcher.Close() }()

	// Watch the directory.
	if err := watcher.Add(w.dir); err != nil {
		fmt.Fprintf(os.Stderr, "nudge watcher failed to add dir %s: %v\n", w.dir, err)
		return
	}

	// Coalescing window.
	coalesceTimer := time.NewTicker(100 * time.Millisecond)
	defer coalesceTimer.Stop()

	pending := false
	for {
		select {
		case <-w.closed:
			return
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only care about file creation/modification in the queue dir
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				// Filter: only .json files in the queue directory
				if strings.HasSuffix(event.Name, ".json") && filepath.Dir(event.Name) == w.dir {
					pending = true
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "nudge watcher error: %v\n", err)
		case <-coalesceTimer.C:
			if pending {
				pending = false
				select {
				case w.events <- struct{}{}:
				default:
				}
			}
		}
	}
}

// WatcherForSession returns a Watcher for a specific session or an error if
// creation fails (e.g., filesystem issues). Callers should handle cleanup.
func WatcherForSession(townRoot, session string) (*Watcher, error) {
	return NewWatcher(townRoot, session)
}
