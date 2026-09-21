package daemon

import (
	"fmt"
	"testing"

	"github.com/steveyegge/gastown/internal/formula"
)

// dogFormulas are the formulas the daemon pours. Every one of them must be able
// to map all of its declared steps, or the unmapped ones can never be closed.
var dogFormulas = []string{
	"mol-dog-backup",
	"mol-dog-checkpoint",
	"mol-dog-compactor",
	"mol-dog-doctor",
	"mol-dog-jsonl",
	"mol-dog-phantom-db",
	"mol-dog-reaper",
	"mol-dog-stale-db",
}

// TestEveryDogStepIsDiscoverable is the regression for hq-g3z5. The old
// keyword-guessing switch silently dropped or collided steps in 5 of these 8
// formulas; only mol-dog-backup was noisy enough to be noticed.
func TestEveryDogStepIsDiscoverable(t *testing.T) {
	for _, name := range dogFormulas {
		t.Run(name, func(t *testing.T) {
			f := loadDogFormula(name)
			if f == nil {
				t.Fatalf("could not load embedded formula %q", name)
			}
			if len(f.Steps) == 0 {
				t.Fatalf("%s declares no steps", name)
			}

			titleToID := formulaStepTitles(name)

			// Simulate discovery over wisps titled from the formula.
			got := map[string]string{}
			for i, step := range f.Steps {
				slug, ok := titleToID[normalizeStepTitle(step.Title)]
				if !ok {
					t.Errorf("step %q (title %q) maps to no slug — it could never be closed", step.ID, step.Title)
					continue
				}
				if slug != step.ID {
					t.Errorf("step %q (title %q) mapped to WRONG slug %q", step.ID, step.Title, slug)
				}
				if prev, dup := got[slug]; dup {
					t.Errorf("step %q collides with step %q on slug %q", step.ID, prev, slug)
				}
				got[slug] = step.ID
				_ = i
			}

			if len(got) != len(f.Steps) {
				t.Errorf("%s: %d steps declared but only %d discoverable", name, len(f.Steps), len(got))
			}
		})
	}
}

// TestBackupSyncStepIsMapped pins the exact reported case: the "sync" step of
// mol-dog-backup, whose title contains "backup" and was therefore filed under
// the wrong slug and then overwritten.
func TestBackupSyncStepIsMapped(t *testing.T) {
	titleToID := formulaStepTitles("mol-dog-backup")

	for title, wantID := range map[string]string{
		"Sync databases to backup remotes":     "sync",
		"Sync backups to offsite storage":      "offsite",
		"Report findings and return to kennel": "report",
	} {
		got, ok := titleToID[normalizeStepTitle(title)]
		if !ok {
			t.Errorf("title %q maps to nothing", title)
			continue
		}
		if got != wantID {
			t.Errorf("title %q -> %q, want %q", title, got, wantID)
		}
	}

	if n := declaredStepCount("mol-dog-backup"); n != 3 {
		t.Errorf("declaredStepCount = %d, want 3", n)
	}
}

// TestMissingSteps reports declared steps that discovery did not map, which is
// what the pour-time warning prints.
func TestMissingSteps(t *testing.T) {
	missing := missingSteps("mol-dog-backup", map[string]string{"offsite": "x", "report": "y"})
	if len(missing) != 1 || missing[0] != "sync" {
		t.Fatalf("missingSteps = %v, want [sync]", missing)
	}

	// Negative control: a fully-discovered molecule reports nothing missing, or
	// the warning would fire on every healthy pour.
	none := missingSteps("mol-dog-backup", map[string]string{"sync": "a", "offsite": "b", "report": "c"})
	if len(none) != 0 {
		t.Fatalf("missingSteps on a complete map = %v, want empty", none)
	}
}

// Unknown or unparseable formulas must degrade quietly rather than panic:
// molecule tracking is observability, not control flow.
func TestLoadDogFormula_UnknownIsNilNotPanic(t *testing.T) {
	if f := loadDogFormula("mol-dog-does-not-exist"); f != nil {
		t.Fatal("unknown formula returned non-nil")
	}
	if f := loadDogFormula(""); f != nil {
		t.Fatal("empty formula name returned non-nil")
	}
	if n := declaredStepCount("mol-dog-does-not-exist"); n != 0 {
		t.Fatalf("declaredStepCount for unknown formula = %d, want 0", n)
	}
}

func TestNormalizeStepTitle(t *testing.T) {
	cases := map[string]string{
		"Sync databases to backup remotes":        "sync databases to backup remotes",
		"  Sync   databases  to backup remotes  ": "sync databases to backup remotes",
		"SYNC DATABASES TO BACKUP REMOTES":        "sync databases to backup remotes",
	}
	for in, want := range cases {
		if got := normalizeStepTitle(in); got != want {
			t.Errorf("normalizeStepTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

var _ = fmt.Sprintf
var _ formula.Formula
