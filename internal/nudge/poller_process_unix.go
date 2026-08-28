//go:build !windows

package nudge

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func pollerProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}

	// Signal(0) succeeds on a ZOMBIE — an unreaped process still owns its PID
	// entry — so a dead poller read as "alive" forever and StartPoller kept
	// returning early instead of respawning it. The deacon's poller sat defunct
	// for 25+ hours while 41 queue-mode nudges piled up undelivered, and every
	// `gt nudge --mode=queue` truthfully printed "✓ Nudged (queue)" because the
	// ENQUEUE really did succeed (hq-8xac).
	//
	// Verified on the live zombie: Signal(0) returned nil for PID 85527, STAT=Z.
	return !pollerProcessZombie(pid)
}

// pollerProcessZombie reports whether a PID is an unreaped (defunct) process.
// Returns false when the state cannot be determined, so a detection failure
// degrades to the previous behaviour rather than killing a healthy poller.
func pollerProcessZombie(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	// STAT begins with the process state; "Z" (optionally with flags) is defunct.
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
