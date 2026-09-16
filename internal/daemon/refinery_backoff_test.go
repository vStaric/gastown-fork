package daemon

import (
	"testing"
	"time"
)

// TestRefineryBackoff_RepeatedFailuresStopTheSameRateRetry is the behaviour
// hq-i0wf asked for: 29 consecutive spawn failures ~6.5 min apart with no decay.
// After a few failures the tracker must refuse the next attempt.
func TestRefineryBackoff_RepeatedFailuresStopTheSameRateRetry(t *testing.T) {
	rt := NewRestartTracker(t.TempDir(), RestartTrackerConfig{})
	const id = "refinery-watch_queue_infrastructure"

	if !rt.CanRestart(id) {
		t.Fatal("first spawn must be allowed")
	}

	rt.RecordRestart(id)
	if rt.CanRestart(id) {
		t.Fatal("a spawn immediately after a failure must be refused (this is the missing backoff)")
	}
	if rt.GetBackoffRemaining(id) <= 0 {
		t.Fatal("no backoff window recorded after a failure")
	}
}

// TestRefineryBackoff_BackoffGrows proves the delay actually decays the retry
// rate rather than staying flat.
func TestRefineryBackoff_BackoffGrows(t *testing.T) {
	rt := NewRestartTracker(t.TempDir(), RestartTrackerConfig{})
	const id = "refinery-x"

	rt.RecordRestart(id)
	first := rt.GetBackoffRemaining(id)
	rt.RecordRestart(id)
	second := rt.GetBackoffRemaining(id)

	if second <= first {
		t.Fatalf("backoff did not grow: first=%s second=%s", first, second)
	}
}

// TestRefineryBackoff_DistinctRigsAreIndependent: one broken rig must not
// suppress spawns for healthy ones. wqi failed 29x while wqk/wqp/wqy were fine.
func TestRefineryBackoff_DistinctRigsAreIndependent(t *testing.T) {
	rt := NewRestartTracker(t.TempDir(), RestartTrackerConfig{})

	rt.RecordRestart("refinery-watch_queue_infrastructure")
	if !rt.CanRestart("refinery-watch_queue_api") {
		t.Fatal("a failure on one rig blocked another rig's refinery spawn")
	}
}

// TestRefineryBackoff_RecordSuccessDoesNotClearImmediately documents REAL
// semantics, against my first assumption. RecordSuccess only resets once the
// agent has been stable for StabilityPeriod; it is not an immediate clear. A
// refinery that spawns successfully right after failures therefore stays in
// backoff until it either ages out or stays up.
//
// That is acceptable here — the backoff windows are far shorter than the
// stability period, so a working refinery is not meaningfully delayed — but it
// must not be described as an immediate clear.
func TestRefineryBackoff_RecordSuccessDoesNotClearImmediately(t *testing.T) {
	rt := NewRestartTracker(t.TempDir(), RestartTrackerConfig{
		StabilityPeriod: 30 * time.Minute,
	})
	const id = "refinery-y"

	rt.RecordRestart(id)
	rt.RecordSuccess(id)

	if rt.CanRestart(id) {
		t.Fatal("RecordSuccess cleared backoff immediately; update the comment at the call site if this changed")
	}
}
