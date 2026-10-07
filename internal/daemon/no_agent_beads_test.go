package daemon

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"
)

// TestReportNoAgentBeads_ThrottlesRepeats is the regression for hq-89wn: the
// per-run warning fired 20,016 times in five days (~6k/day) while the GUPP and
// orphaned-work checks sat dead, burying the one signal that mattered.
func TestReportNoAgentBeads_ThrottlesRepeats(t *testing.T) {
	var buf bytes.Buffer
	d := &Daemon{logger: log.New(&buf, "", 0)}

	for i := 0; i < 50; i++ {
		d.reportNoAgentBeads("GUPP")
	}

	got := strings.Count(buf.String(), "AGENT BEADS MISSING")
	if got != 1 {
		t.Fatalf("50 calls produced %d log lines, want 1 (throttle not applied)", got)
	}
}

// It must still report once the interval has passed — a throttle that never
// re-fires would make a sustained outage invisible, which is worse.
func TestReportNoAgentBeads_ReportsAgainAfterInterval(t *testing.T) {
	var buf bytes.Buffer
	d := &Daemon{logger: log.New(&buf, "", 0)}

	d.reportNoAgentBeads("GUPP")
	// Backdate the throttle as if the interval had elapsed.
	d.noAgentBeadsMu.Lock()
	d.noAgentBeadsLoggedAt = time.Now().Add(-2 * noAgentBeadsLogInterval)
	d.noAgentBeadsMu.Unlock()
	d.reportNoAgentBeads("orphaned work")

	if got := strings.Count(buf.String(), "AGENT BEADS MISSING"); got != 2 {
		t.Fatalf("got %d log lines across the interval boundary, want 2", got)
	}
}

// The message must say what is BROKEN, not just that something failed. The old
// text ("listing agent beads failed") read like a transient hiccup.
func TestReportNoAgentBeads_MessageNamesTheConsequence(t *testing.T) {
	var buf bytes.Buffer
	d := &Daemon{logger: log.New(&buf, "", 0)}
	d.reportNoAgentBeads("GUPP")

	out := buf.String()
	for _, want := range []string{"GUPP", "stalled polecat", "gt:agent", "reopen"} {
		if !strings.Contains(out, want) {
			t.Errorf("message does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "listing agent beads failed") {
		t.Error("still using the old failure-shaped wording for a state")
	}
}

// The sentinel must be matchable, or the call sites cannot tell the empty-state
// apart from a real listing error.
func TestErrNoAgentBeads_IsMatchable(t *testing.T) {
	if !errors.Is(ErrNoAgentBeads, ErrNoAgentBeads) {
		t.Fatal("ErrNoAgentBeads is not matchable with errors.Is")
	}
	wrapped := errors.New("other failure")
	if errors.Is(wrapped, ErrNoAgentBeads) {
		t.Fatal("an unrelated error matched ErrNoAgentBeads")
	}
}
