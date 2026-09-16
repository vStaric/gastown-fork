package tmux

import "testing"

// The real dialog, captured from a live wqi refinery spawn (hq-i0wf).
// Note which option carries the cursor: the REFUSING one.
const realTrustDialog = ` Accessing workspace:
 /Users/vid/towns/queue-town/watch_queue_infrastructure/refinery/rig
 Quick safety check: Is this a project you created or one you trust?
 Claude Code'll be able to read, edit, and execute files here.
 Security guide
 ❯ No, exit
   Yes, I trust this folder
 Enter to confirm · Esc to cancel`

// TestTrustAffirmative_NeverMatchesTheRefusingOption is the one that matters:
// treating "No, exit" as affirmative is exactly the bug — a bare Enter on it
// exits the agent with code 1 and the daemon respawns forever.
func TestTrustAffirmative_NeverMatchesTheRefusingOption(t *testing.T) {
	if isTrustAffirmativeOption("❯ No, exit") {
		t.Fatal(`"No, exit" matched as affirmative; Enter there kills the agent`)
	}
	if isTrustAffirmativeOption("No, exit") {
		t.Fatal(`"No, exit" matched as affirmative`)
	}
}

func TestTrustAffirmative_MatchesTheTrustingOption(t *testing.T) {
	for _, line := range []string{
		"Yes, I trust this folder",
		"❯ Yes, I trust this folder",
		"yes, proceed",
	} {
		if !isTrustAffirmativeOption(line) {
			t.Errorf("%q should match as affirmative", line)
		}
	}
}

// TestTrustSelection_RealDialogNeedsAMove proves the handler must move the
// selection: on the real dialog the cursor starts on the refusing option.
func TestTrustSelection_RealDialogNeedsAMove(t *testing.T) {
	if !containsWorkspaceTrustDialog(realTrustDialog) {
		t.Fatal("real dialog not detected")
	}
	if !contentHasTrustAffirmative(realTrustDialog) {
		t.Fatal("affirmative option not found in real dialog")
	}
	if trustAffirmativeSelected(realTrustDialog) {
		t.Fatal("cursor reported on the affirmative option; it starts on 'No, exit', so a bare Enter would exit")
	}
}

// Negative control: once the cursor IS on the trusting option, no move is needed.
func TestTrustSelection_AlreadyOnAffirmativeNeedsNoMove(t *testing.T) {
	moved := ` Quick safety check: Is this a project you created or one you trust?
   No, exit
 ❯ Yes, I trust this folder
 Enter to confirm · Esc to cancel`
	if !trustAffirmativeSelected(moved) {
		t.Fatal("cursor is on the trusting option but was not detected")
	}
}

// An unrecognised dialog must report no affirmative option, so the handler
// declines to press keys blindly.
func TestTrustSelection_UnknownLayoutIsNotGuessed(t *testing.T) {
	unknown := " Quick safety check: something new\n ❯ Option A\n   Option B"
	if contentHasTrustAffirmative(unknown) {
		t.Fatal("unknown layout reported an affirmative option")
	}
}
