package cmd

import (
	"strings"
	"testing"
)

// TestSuggestQualifiedTarget_NilTmuxIsSafe: the suggestion is a convenience on
// an error path and must never itself fail.
func TestSuggestQualifiedTarget_NilTmuxIsSafe(t *testing.T) {
	if got := suggestQualifiedTarget(nil, "Karsa"); got != "" {
		t.Fatalf("nil tmux returned %q, want empty", got)
	}
}

// An already-qualified target needs no suggestion — it failed for some other
// reason, and hinting "did you mean rig-qualified?" would be actively wrong.
func TestSuggestQualifiedTarget_AlreadyQualifiedGetsNoHint(t *testing.T) {
	if got := suggestQualifiedTarget(nil, "watch_queue_api/Karsa"); got != "" {
		t.Fatalf("qualified target returned %q, want empty", got)
	}
	if strings.Contains(suggestQualifiedTarget(nil, ""), "did you mean") {
		t.Fatal("empty target produced a hint")
	}
}
