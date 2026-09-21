//go:build !windows

package lock

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestFlockAcquire_BoundedWhenHeld is the defect in hq-z9ze: a blocking
// LOCK_EX has no deadline, so a contended acquire parks at 0% CPU for as long
// as the holder keeps it — 78 minutes in the reported case, during which the
// deacon could not heartbeat and the daemon killed it as "stuck".
func TestFlockAcquire_BoundedWhenHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.flock")

	release, ok, err := FlockTryAcquire(path)
	if err != nil || !ok {
		t.Fatalf("holder could not take the lock: ok=%v err=%v", ok, err)
	}
	defer release()

	start := time.Now()
	unlock, err := FlockAcquireTimeout(path, 200*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		unlock()
		t.Fatal("acquired a lock that was already held; the bound did not apply")
	}
	if !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("got %v, want ErrLockTimeout", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waited %s for a 200ms bound; the acquire is still effectively unbounded", elapsed)
	}
}

// Negative control: an UNcontended acquire must still succeed promptly, or the
// bound would have broken every lock in the tree rather than fixed one.
func TestFlockAcquire_UncontendedStillSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "free.flock")

	start := time.Now()
	unlock, err := FlockAcquire(path)
	if err != nil {
		t.Fatalf("uncontended acquire failed: %v", err)
	}
	defer unlock()

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("uncontended acquire took %s", elapsed)
	}
}

// A lock released by its holder must become available to a waiter — the bound
// must not turn a normal handoff into a failure.
func TestFlockAcquire_SucceedsOnceHolderReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.flock")

	release, ok, err := FlockTryAcquire(path)
	if err != nil || !ok {
		t.Fatalf("holder could not take the lock: ok=%v err=%v", ok, err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		release()
	}()

	unlock, err := FlockAcquireTimeout(path, 5*time.Second)
	if err != nil {
		t.Fatalf("waiter never acquired after release: %v", err)
	}
	unlock()
}

// A non-positive timeout preserves the original blocking behaviour for any
// caller that deliberately wants it.
func TestFlockAcquire_ZeroTimeoutBlocksAsBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blocking.flock")

	unlock, err := FlockAcquireTimeout(path, 0)
	if err != nil {
		t.Fatalf("blocking acquire on a free lock failed: %v", err)
	}
	unlock()
}
