//go:build windows

package lock

import (
	"errors"
	"time"
)

// ErrLockTimeout mirrors the Unix sentinel so callers can match on it
// regardless of platform. Windows acquires are no-ops and never return it.
var ErrLockTimeout = errors.New("timed out acquiring lock")

// DefaultFlockTimeout mirrors the Unix bound. Unused here: Windows acquires are
// no-ops, but the constant keeps cross-platform callers compiling.
const DefaultFlockTimeout = 30 * time.Second

// FlockAcquire is a no-op on Windows. Gas Town doesn't run on Windows
// in production, so the advisory lock is not critical here.
func FlockAcquire(path string) (func(), error) {
	return func() {}, nil
}

// flockAcquire is a no-op on Windows. Gas Town doesn't run on Windows
// in production, so the advisory lock is not critical here.
func flockAcquire(path string) (func(), error) {
	return func() {}, nil
}

// FlockTryAcquire is a no-op on Windows. Always reports success.
func FlockTryAcquire(path string) (func(), bool, error) {
	return func() {}, true, nil
}

// FlockAcquireTimeout is a no-op on Windows. Always reports success.
func FlockAcquireTimeout(path string, timeout time.Duration) (func(), error) {
	return func() {}, nil
}
