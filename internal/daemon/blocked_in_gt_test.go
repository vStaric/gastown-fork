package daemon

import (
	"testing"
	"time"
)

// TestBlockedInGt_LongRunningCallSuppressesTheKill is the behaviour hq-z9ze
// asked for: a live agent parked inside a gt call must not be killed as dead.
func TestBlockedInGt_LongRunningCallSuppressesTheKill(t *testing.T) {
	age, blocked := blockedInGtCallFrom([]gtProcess{
		{pid: 1, age: 78 * time.Minute, command: "/Users/vid/.local/bin/gt hook"},
	})
	if !blocked {
		t.Fatal("a 78-minute gt hook was not recognised as a blocked call; the deacon would be killed")
	}
	if age != 78*time.Minute {
		t.Fatalf("age = %s, want 78m", age)
	}
}

// THE CRITICAL SAFETY TEST. The daemon's own helpers run forever by design. If
// they counted as "blocked", the guard would suppress EVERY deacon restart
// permanently — far worse than the bug it fixes.
func TestBlockedInGt_LongLivedHelpersDoNotSuppressTheKill(t *testing.T) {
	procs := []gtProcess{
		{pid: 1, age: 5 * 24 * time.Hour, command: "/Users/vid/.local/bin/gt daemon run"},
		{pid: 2, age: 5 * 24 * time.Hour, command: "/Users/vid/.local/bin/gt nudge-poller wqp-witness"},
		{pid: 3, age: 5 * 24 * time.Hour, command: "/Users/vid/.local/bin/gt nudge-poller hq-deacon"},
	}
	if _, blocked := blockedInGtCallFrom(procs); blocked {
		t.Fatal("daemon helpers counted as a blocked call; this would disable deacon restarts forever")
	}
}

// A short-lived gt call is normal and must not suppress a restart.
func TestBlockedInGt_ShortCallsAreNormal(t *testing.T) {
	if _, blocked := blockedInGtCallFrom([]gtProcess{
		{pid: 1, age: 900 * time.Millisecond, command: "/Users/vid/.local/bin/gt hook"},
	}); blocked {
		t.Fatal("a sub-second gt call was treated as blocked")
	}
}

// Negative control: no gt processes at all means a genuinely dead agent, which
// MUST still be restartable.
func TestBlockedInGt_NoProcessesMeansRestartable(t *testing.T) {
	if _, blocked := blockedInGtCallFrom(nil); blocked {
		t.Fatal("an empty process list reported a blocked call; a dead deacon would never be restarted")
	}
}

func TestIsGtCommand_MatchesExecutableNotSubstring(t *testing.T) {
	cases := map[string]bool{
		"/Users/vid/.local/bin/gt hook":    true,
		"gt deacon heartbeat":              true,
		"/Users/vid/gastown-fork/gt hook":  true,
		"git status":                       false,
		"/usr/bin/gtar -xf x":              false,
		"claude --prompt 'run gt hook'":    false,
		"node /path/gt-helper.js":          false,
	}
	for cmd, want := range cases {
		if got := isGtCommand(cmd); got != want {
			t.Errorf("isGtCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestParseETime(t *testing.T) {
	cases := map[string]time.Duration{
		"01:30":       90 * time.Second,
		"10:00":       10 * time.Minute,
		"1:18:05":     78*time.Minute + 5*time.Second,
		"05-23:48:15": 5*24*time.Hour + 23*time.Hour + 48*time.Minute + 15*time.Second,
	}
	for in, want := range cases {
		got, ok := parseETime(in)
		if !ok {
			t.Errorf("parseETime(%q) failed", in)
			continue
		}
		if got != want {
			t.Errorf("parseETime(%q) = %s, want %s", in, got, want)
		}
	}
	if _, ok := parseETime("garbage"); ok {
		t.Error("parseETime accepted garbage")
	}
}

// The real process table must not report a blocked call in steady state, or the
// guard would suppress restarts on this machine right now.
func TestBlockedInGt_LiveSystemIsNotBlocked(t *testing.T) {
	if age, blocked := blockedInGtCall(); blocked {
		t.Fatalf("live system reports a blocked gt call (age %s); check helper classification", age)
	}
}
