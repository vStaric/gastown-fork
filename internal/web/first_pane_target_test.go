package web

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/tmux"
)

// TestIsClaudeRunningTargetHasNoLiteralIndex guards the tmux target used by
// isClaudeRunningInSession. A literal "<session>:0.0" is invalid on any server
// with base-index=1 (a common .tmux.conf setting); display-message then fails
// and the function reports "no agent running" for every live session (hq-aqwp).
func TestIsClaudeRunningTargetHasNoLiteralIndex(t *testing.T) {
	target := tmux.FirstPaneTarget("wqp-witness")

	if strings.Contains(target, ":0") || strings.Contains(target, ".0") {
		t.Fatalf("target %q hardcodes a literal index; breaks under base-index=1", target)
	}
	if want := "wqp-witness:^"; target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
}
