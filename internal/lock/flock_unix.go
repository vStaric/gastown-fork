//go:build !windows

package lock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// ErrLockTimeout reports that an advisory lock could not be acquired within the
// caller's deadline.
var ErrLockTimeout = errors.New("timed out acquiring lock")

// DefaultFlockTimeout bounds how long a blocking acquire will wait.
//
// An UNBOUNDED syscall.Flock(LOCK_EX) parks the process in the kernel at 0% CPU
// for as long as the holder keeps the lock. That is what let a single gt call
// sit for 78 minutes of wall clock on ~8s of CPU while the deacon could not
// emit a heartbeat, so the daemon's stuck-detector read the starved heartbeat
// as a dead agent and killed a live session — 18 times in one daemon.log
// (hq-z9ze). Failing fast is strictly better: the caller retries on the next
// patrol tick, whereas a parked call takes the agent down with it.
const DefaultFlockTimeout = 30 * time.Second

// flockPollInterval is how often a bounded acquire retries the non-blocking
// lock. Short enough that an uncontended handoff is not noticeably delayed,
// long enough not to spin a core while waiting.
const flockPollInterval = 25 * time.Millisecond

// FlockAcquire opens a flock file and acquires an exclusive advisory lock.
// Returns a cleanup function that releases the lock and closes the file.
// This is a general-purpose cross-process lock suitable for any read-modify-write
// operation that needs serialization across separate CLI invocations.
func FlockAcquire(path string) (func(), error) {
	return flockAcquire(path)
}

// FlockAcquireTimeout is FlockAcquire with an explicit deadline. It returns an
// error wrapping ErrLockTimeout if the lock is still held when the deadline
// passes.
func FlockAcquireTimeout(path string, timeout time.Duration) (func(), error) {
	return flockAcquireTimeout(path, timeout)
}

// flockAcquire opens a flock file and acquires an exclusive advisory lock,
// bounded by DefaultFlockTimeout.
func flockAcquire(path string) (func(), error) {
	return flockAcquireTimeout(path, DefaultFlockTimeout)
}

// flockAcquireTimeout acquires an exclusive advisory lock, giving up after
// timeout with ErrLockTimeout.
//
// It polls LOCK_NB rather than issuing a bare blocking LOCK_EX. A blocking
// flock has no deadline: once entered, the process is parked in the kernel
// until the holder releases, however long that takes. Polling keeps the wait
// bounded and observable, and costs one syscall per interval.
//
// A non-positive timeout means "wait forever" and restores the old behaviour
// for any caller that genuinely needs it; nothing in-tree asks for that today.
func flockAcquireTimeout(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644) //nolint:gosec // G304,G306: lock files are internal operational data
	if err != nil {
		return nil, fmt.Errorf("opening flock file: %w", err)
	}

	cleanup := func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()
	}

	if timeout <= 0 {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			f.Close()
			return nil, fmt.Errorf("acquiring flock: %w", err)
		}
		return cleanup, nil
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return cleanup, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("acquiring flock: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("%w after %s: %s", ErrLockTimeout, timeout, path)
		}
		time.Sleep(flockPollInterval)
	}
}

// FlockTryAcquire attempts a non-blocking exclusive advisory lock on the given path.
// Returns (cleanup, true, nil) if the lock was acquired, or (nil, false, nil) if
// another process already holds it. The cleanup function releases the lock and
// closes the file descriptor.
func FlockTryAcquire(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644) //nolint:gosec // G304,G306: lock files are internal operational data
	if err != nil {
		return nil, false, fmt.Errorf("opening flock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("acquiring flock: %w", err)
	}

	cleanup := func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
		f.Close()
	}
	return cleanup, true, nil
}
