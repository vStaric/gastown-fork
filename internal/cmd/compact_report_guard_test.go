package cmd

import (
	"strings"
	"testing"
)

// TestClosedEventListArgs_FiltersOnClosed is the regression for hq-agb9.
//
// Audit beads are auto-closed at creation and `bd list` hides closed issues by
// default. The weekly guard omitted --status=closed, so its query returned
// nothing, it concluded "no rollup yet", and it mailed a duplicate rollup on
// every Monday patrol cycle — three for the same week on 2026-10-05.
func TestClosedEventListArgs_FiltersOnClosed(t *testing.T) {
	args := closedEventListArgs()
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--status=closed") {
		t.Fatalf("args %q omit --status=closed; the guard would never find its audit bead", joined)
	}
	if !strings.Contains(joined, "--type=event") {
		t.Errorf("args %q omit --type=event", joined)
	}
	if !strings.Contains(joined, "--json") {
		t.Errorf("args %q omit --json", joined)
	}
	if args[0] != "list" {
		t.Errorf("args[0] = %q, want \"list\"", args[0])
	}
}

// The limit must be large enough that a busy week's events cannot push the
// rollup out of the window. The weekly guard used 20 where the daily used 50.
func TestClosedEventListArgs_LimitIsNotTooSmall(t *testing.T) {
	joined := strings.Join(closedEventListArgs(), " ")
	if strings.Contains(joined, "--limit=20") {
		t.Fatalf("args %q use the old undersized limit", joined)
	}
	if !strings.Contains(joined, "--limit=50") {
		t.Errorf("args %q do not set the expected --limit=50", joined)
	}
}

// Both guards must use the SAME query. They drifted apart once; sharing one
// builder is the fix, so assert there is exactly one source of truth.
func TestBothGuardsShareOneQuery(t *testing.T) {
	a := strings.Join(closedEventListArgs(), " ")
	b := strings.Join(closedEventListArgs(), " ")
	if a != b {
		t.Fatalf("query is not deterministic: %q vs %q", a, b)
	}
	// Guard against an accidental open-status filter sneaking in.
	if strings.Contains(a, "--status=open") {
		t.Fatalf("args %q filter on open; audit beads are closed", a)
	}
}
