package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// logUsagePath is the JSONL file where command usage is recorded.
// Location: $GT_HOME/.gt when GT_HOME is set, otherwise ~/.gt.
var logUsagePath = filepath.Join(gtDataDir(), "cmd-usage.jsonl")

// noLogCommands are top-level commands excluded from telemetry.
// These fire per-tool-use and would dominate the log.
var noLogCommands = map[string]bool{
	"tap":    true,
	"signal": true,
}

// logCommandUsage appends one JSONL line to the cmd-usage.jsonl log.
// Location: $GT_HOME/.gt/cmd-usage.jsonl when GT_HOME is set, else ~/.gt/.
// Fire-and-forget: all errors are silently ignored.
func logCommandUsage(cmd *cobra.Command, args []string) {
	// Walk up to the first subcommand under root to check exclusions.
	root := cmd
	for root.Parent() != nil && root.Parent().Parent() != nil {
		root = root.Parent()
	}
	if noLogCommands[root.Name()] {
		return
	}

	actor := os.Getenv("GT_ROLE")
	if actor == "" {
		actor = "unknown"
	}

	rotateUsageLogIfLarge(logUsagePath)

	f, err := os.OpenFile(logUsagePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().Format(time.RFC3339)
	cmdPath := buildCommandPath(cmd)
	fmt.Fprintf(f, `{"ts":"%s","cmd":"%s","actor":"%s","argc":%d}`+"\n",
		ts, cmdPath, actor, len(args))
}

// usageLogMaxBytes is the size at which cmd-usage.jsonl is rotated.
//
// This log had no rotation at all: logCommandUsage is append-only and
// fire-and-forget, so it grew unbounded for four months to 188 MB / 2.2M lines
// and was still being written every time any agent ran any gt command. On a
// machine at 96% used with 21 GB left, an unbounded append-only log that every
// command touches is a slow disk-exhaustion path — and disk exhaustion blocks
// spawns town-wide (cf. the wqp-7syf spawn-gate work). hq-m9pk.
//
// 16 MB keeps roughly 190k lines, which is far more than `gt metrics` needs to
// be useful, and bounds the total at 32 MB with the single .1 generation.
const usageLogMaxBytes = 16 << 20

// rotateUsageLogIfLarge renames the usage log to <path>.1 once it exceeds
// usageLogMaxBytes, discarding any previous .1. One generation only: this is
// best-effort usage telemetry, not an audit trail, and the failure mode being
// fixed is unbounded growth — keeping many generations would reintroduce it.
//
// Fire-and-forget like its caller: every error is ignored. A rotation that fails
// must never block the command the user actually ran.
func rotateUsageLogIfLarge(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= usageLogMaxBytes {
		return
	}
	_ = os.Rename(path, path+".1")
}
