package tmux

import (
	"os/exec"
	"strings"
	"testing"
)

// TestComposerStaged_LoadedComposerIsNotSafeToInject is the guard that stands
// between an automated nudge and an unauthored instruction being submitted.
//
// It uses a real tmux pane because the failure it prevents is precisely that a
// LOADED composer is indistinguishable from an idle one by prompt detection —
// a mocked pane cannot demonstrate that.
func TestComposerStaged_LoadedComposerIsNotSafeToInject(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	// Isolated socket: never touch the live town server, whose panes hold real
	// unsent operator text.
	const sock = "gt-test-composer-staged-sock"
	sess := "gt-test-composer-staged"
	tmx := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-L", sock}, args...)...)
	}
	if out, err := tmx("new-session", "-d", "-s", sess, "cat").CombinedOutput(); err != nil {
		t.Skipf("cannot create tmux session: %v: %s", err, out)
	}
	defer func() { _ = tmx("kill-server").Run() }()

	tm := NewTmuxWithSocket(sock)

	// Write a prompt line with parked text, exactly as a loaded composer shows it.
	target := firstPaneTarget(sess)
	if out, err := tmx("send-keys", "-t", target, "-l",
		DefaultReadyPromptPrefix+"delete the stale branches on origin").CombinedOutput(); err != nil {
		t.Fatalf("send-keys failed: %v: %s", err, out)
	}

	line, err := tm.ReadyPromptLine(sess)
	if err != nil {
		t.Fatalf("ReadyPromptLine: %v", err)
	}
	if !strings.Contains(line, "delete the stale branches") {
		t.Fatalf("prompt line = %q, want the parked text", line)
	}
	if strings.TrimSpace(line) == "" {
		t.Fatal("parked text read as empty; the guard would inject into a loaded composer")
	}
}

// TestComposerStaged_EmptyPromptIsSafe is the negative control: the guard must
// be able to return false, or it would wedge delivery for every session forever.
func TestComposerStaged_EmptyPromptIsSafe(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	const sock = "gt-test-composer-empty-sock"
	sess := "gt-test-composer-empty"
	tmx := func(args ...string) *exec.Cmd {
		return exec.Command("tmux", append([]string{"-L", sock}, args...)...)
	}
	if out, err := tmx("new-session", "-d", "-s", sess, "cat").CombinedOutput(); err != nil {
		t.Skipf("cannot create tmux session: %v: %s", err, out)
	}
	defer func() { _ = tmx("kill-server").Run() }()

	tm := NewTmuxWithSocket(sock)
	target := firstPaneTarget(sess)
	// A bare prompt with nothing after it.
	if out, err := tmx("send-keys", "-t", target, "-l",
		DefaultReadyPromptPrefix).CombinedOutput(); err != nil {
		t.Fatalf("send-keys failed: %v: %s", err, out)
	}

	line, err := tm.ReadyPromptLine(sess)
	if err != nil {
		t.Fatalf("ReadyPromptLine: %v", err)
	}
	if strings.TrimSpace(line) != "" {
		t.Fatalf("empty composer read as %q; guard would withhold forever", line)
	}
}
