package tmux

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFirstPaneTargetAvoidsLiteralIndices pins the fix for hq-9bpm: the nudge
// send-keys target must not hardcode window/pane index 0, because base-index=1
// is a near-universal .tmux.conf setting and "<session>:0.0" is then invalid.
func TestFirstPaneTargetAvoidsLiteralIndices(t *testing.T) {
	got := firstPaneTarget("wqp-witness")
	if want := "wqp-witness:^"; got != want {
		t.Fatalf("firstPaneTarget = %q, want %q", got, want)
	}
	if strings.Contains(got, ":0") || strings.HasSuffix(got, ".0") {
		t.Fatalf("firstPaneTarget %q hardcodes a literal index; breaks under base-index=1", got)
	}
	// ".^" is rejected by send-keys ("can't find pane: ^"), so it must not appear.
	if strings.Contains(got, ".^") {
		t.Fatalf("firstPaneTarget %q uses .^, which send-keys rejects", got)
	}
}

// TestFirstPaneTargetSendKeysUnderBaseIndexOne is the end-to-end guard: it
// stands up a real tmux server with base-index/pane-base-index set to 1 and
// asserts send-keys accepts the target. The old "<session>:0.0" form fails here.
func TestFirstPaneTargetSendKeysUnderBaseIndexOne(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	const sock = "gt-test-baseindex-hq9bpm"
	const sess = "probe"
	run := func(args ...string) (string, error) {
		out, err := exec.Command("tmux", append([]string{"-L", sock}, args...)...).CombinedOutput()
		return string(out), err
	}
	defer run("kill-server")

	// Start the server first: set-option -g needs a running server, and the
	// base-index must be set before the target session's window is created.
	if out, err := run("new-session", "-d", "-s", "bootstrap", "sh"); err != nil {
		t.Skipf("cannot start tmux test server: %v: %s", err, out)
	}
	if out, err := run("set-option", "-g", "base-index", "1"); err != nil {
		t.Fatalf("set base-index: %v: %s", err, out)
	}
	if out, err := run("set-option", "-g", "pane-base-index", "1"); err != nil {
		t.Fatalf("set pane-base-index: %v: %s", err, out)
	}
	if out, err := run("new-session", "-d", "-s", sess, "sh"); err != nil {
		t.Fatalf("new-session: %v: %s", err, out)
	}

	// Sanity: confirm the server really did assign index 1, else the test proves nothing.
	idx, err := run("display-message", "-t", sess, "-p", "#{window_index}")
	if err != nil {
		t.Fatalf("display-message: %v: %s", err, idx)
	}
	if strings.TrimSpace(idx) != "1" {
		t.Skipf("tmux did not honor base-index=1 (got %q)", strings.TrimSpace(idx))
	}

	if out, err := run("send-keys", "-t", firstPaneTarget(sess), "-l", ""); err != nil {
		t.Fatalf("send-keys to %q failed: %v: %s", firstPaneTarget(sess), err, out)
	}
	// The regression itself: the old hardcoded form must be the thing that breaks.
	if out, err := run("send-keys", "-t", sess+":0.0", "-l", ""); err == nil {
		t.Fatalf("expected %q to fail under base-index=1, but it succeeded: %s", sess+":0.0", out)
	}
}
