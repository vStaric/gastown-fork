package daemon

import (
	"testing"
	"time"
)

// TestCrashLoopMarkerExpires covers hq-vw9f: an unbounded crash-loop marker
// suppressed BOTH the restart ladder and the heartbeat-kill check for ~14.5h
// while the deacon sat frozen, and could only be cleared by a human running
// `gt daemon clear-backoff`.
//
// The deadlock is structural: the only code that clears CrashLoopSince lives in
// RecordRestart, which is unreachable while the marker is set (CanRestart returns
// false, so no restart is attempted, so RecordRestart never runs). This asserts
// the marker ages out so recovery resumes on its own.
func TestCrashLoopMarkerExpires(t *testing.T) {
	rt := &RestartTracker{
		state:  &RestartState{Agents: map[string]*AgentRestartInfo{}},
		config: DefaultRestartTrackerConfig(),
	}

	// A FRESH marker must still suppress — this is the #2086 guarantee and the
	// expiry must not weaken it.
	rt.state.Agents["deacon"] = &AgentRestartInfo{CrashLoopSince: time.Now()}
	if !rt.IsInCrashLoop("deacon") {
		t.Error("fresh crash-loop marker must still suppress (regression on #2086)")
	}
	if rt.CanRestart("deacon") {
		t.Error("fresh crash-loop marker must still block restarts")
	}
	if _, expired := rt.CrashLoopExpired("deacon"); expired {
		t.Error("fresh marker must not report as expired")
	}

	// A marker older than CrashLoopMaxAge must stop suppressing, or the agent
	// stays frozen until a human intervenes.
	rt.state.Agents["deacon"] = &AgentRestartInfo{
		CrashLoopSince: time.Now().Add(-(CrashLoopMaxAge + time.Minute)),
	}
	if rt.IsInCrashLoop("deacon") {
		t.Errorf("marker older than %s must not suppress recovery", CrashLoopMaxAge)
	}
	if !rt.CanRestart("deacon") {
		t.Errorf("marker older than %s must not block restarts", CrashLoopMaxAge)
	}
	age, expired := rt.CrashLoopExpired("deacon")
	if !expired {
		t.Error("stale marker must report as expired so the log records it")
	}
	if age < CrashLoopMaxAge {
		t.Errorf("reported age %s should be >= %s", age, CrashLoopMaxAge)
	}

	// No marker at all: not in a loop, and nothing to report as expired.
	rt.state.Agents["deacon"] = &AgentRestartInfo{}
	if rt.IsInCrashLoop("deacon") {
		t.Error("zero CrashLoopSince must not read as a crash loop")
	}
	if _, expired := rt.CrashLoopExpired("deacon"); expired {
		t.Error("absent marker must not report as expired")
	}
}
