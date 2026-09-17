package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSkipIsNotAPass is the defect in hq-orc1: a rig with no test commands
// returned nil, the caller read nil as "passed", and the cycle reported
// "9 tested, 0 failed" having run nothing. A skip must be its own state.
func TestSkipIsNotAPass(t *testing.T) {
	rigPath := t.TempDir()
	// A rig with no config.json at all -> nothing to run.
	cfg, err := loadRigGateConfig(rigPath)
	if err != nil {
		t.Fatalf("loadRigGateConfig: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil gate config for an unconfigured rig, got %+v", cfg)
	}

	// The sentinel must not be mistakable for success.
	if errNoTestCommands == nil {
		t.Fatal("errNoTestCommands is nil; a skip would again be indistinguishable from a pass")
	}
	if !errors.Is(errNoTestCommands, errNoTestCommands) {
		t.Fatal("errNoTestCommands is not matchable with errors.Is")
	}
}

// TestLoadRigGateConfig_EmptyCommandsAreNotRunnable guards the other vacuous
// paths: empty gates or an empty test_command must NOT produce a config, because
// `sh -c ""` exits 0 and would be logged as a pass having run nothing.
func TestLoadRigGateConfig_EmptyCommandsAreNotRunnable(t *testing.T) {
	cases := map[string]any{
		"empty gates map":     map[string]any{"merge_queue": map[string]any{"gates": map[string]any{}}},
		"empty test_command":  map[string]any{"merge_queue": map[string]any{"test_command": ""}},
		"gate with empty cmd": map[string]any{"merge_queue": map[string]any{"gates": map[string]any{"unit": map[string]any{"cmd": ""}}}},
		"no merge_queue":      map[string]any{"other": 1},
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			data, err := json.Marshal(content)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := loadRigGateConfig(dir)
			if err != nil {
				t.Fatalf("loadRigGateConfig: %v", err)
			}
			if cfg != nil {
				t.Fatalf("empty/absent commands produced a runnable config %+v; the patrol would report a pass having run nothing", cfg)
			}
		})
	}
}

// Negative control: a REAL command must still produce a runnable config, or the
// guard above would disable the patrol entirely rather than fix its reporting.
func TestLoadRigGateConfig_RealCommandIsRunnable(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"merge_queue":{"gates":{"unit":{"cmd":"go test ./..."}}}}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadRigGateConfig(dir)
	if err != nil {
		t.Fatalf("loadRigGateConfig: %v", err)
	}
	if cfg == nil || cfg.Gates["unit"] != "go test ./..." {
		t.Fatalf("a real gate command was not loaded: %+v", cfg)
	}
}

// TestVacuousCycleLogsAreNotGreen asserts what a READER sees, which is the
// actual harm in hq-orc1: the old cycle logged "9 tested, 0 failed" and a
// per-rig "passed" while running nothing, so a real breakage would have
// produced a byte-identical line.
func TestVacuousCycleLogsAreNotGreen(t *testing.T) {
	var buf bytes.Buffer
	d := &Daemon{
		logger: log.New(&buf, "", 0),
		config: &Config{TownRoot: t.TempDir()},
		ctx:    context.Background(),
	}

	// Two rigs, neither configured with test commands.
	for _, name := range []string{"rig_a", "rig_b"} {
		if err := os.MkdirAll(filepath.Join(d.config.TownRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	d.runMainBranchTestsForRigs([]string{"rig_a", "rig_b"}, time.Minute)
	out := buf.String()

	if strings.Contains(out, ": passed") {
		t.Fatalf("a rig that ran nothing was logged as passed:\n%s", out)
	}
	if !strings.Contains(out, "2 skipped") {
		t.Fatalf("skips were not counted separately:\n%s", out)
	}
	if !strings.Contains(out, "0 tested") {
		t.Fatalf("rigs that ran nothing were counted as tested:\n%s", out)
	}
	if !strings.Contains(out, "NO main-branch coverage") {
		t.Fatalf("a zero-coverage cycle did not say so:\n%s", out)
	}
}
