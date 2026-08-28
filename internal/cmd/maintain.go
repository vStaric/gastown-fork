package cmd

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

const (
	// defaultMaintainThreshold is the minimum commit count before flatten triggers.
	defaultMaintainThreshold = 100
	// maintainGCTimeout is the timeout for CALL dolt_gc() on a single database.
	maintainGCTimeout = 5 * time.Minute
	// maintainBackupTimeout is the timeout for dolt backup sync on a single database.
	maintainBackupTimeout = 2 * time.Minute
	// maintainQueryTimeout is the timeout for individual SQL queries during flatten.
	maintainQueryTimeout = 30 * time.Second
)

var (
	maintainForce     bool
	maintainDryRun    bool
	maintainThreshold int
)

var maintainCmd = &cobra.Command{
	Use:     "maintain",
	GroupID: GroupServices,
	Short:   "Run full Dolt maintenance (reap + flatten + gc)",
	Long: `Run the full Dolt maintenance pipeline in a single command.

All operations run via SQL on the running server — no downtime needed.

This encapsulates the maintenance procedure:
  1. Backup all databases (dolt backup sync)
  2. Reap closed wisps from each database
  3. Flatten databases over commit threshold
  4. Run dolt_gc() on each database

Use --force for non-interactive mode (daemon/cron), or run interactively
to review the plan before proceeding.

Examples:
  gt maintain                # Interactive (shows plan, asks confirmation)
  gt maintain --force        # Non-interactive (daemon/cron use)
  gt maintain --dry-run      # Preview what would happen
  gt maintain --threshold 50 # Custom commit threshold`,
	RunE: runMaintain,
}

func init() {
	maintainCmd.Flags().BoolVar(&maintainForce, "force", false, "Non-interactive mode (skip confirmation)")
	maintainCmd.Flags().BoolVar(&maintainDryRun, "dry-run", false, "Preview without making changes")
	maintainCmd.Flags().IntVar(&maintainThreshold, "threshold", defaultMaintainThreshold, "Commit count threshold for flatten")
	rootCmd.AddCommand(maintainCmd)
}

// maintainDBInfo holds per-database info for the maintenance plan.
type maintainDBInfo struct {
	name        string
	commitCount int
	hasBackup   bool
	backupName  string
}

func runMaintain(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	config := doltserver.DefaultConfig(townRoot)
	if config.IsRemote() {
		return fmt.Errorf("maintain requires local Dolt server (remote: %s)", config.HostPort())
	}

	// Verify server is running (needed for reap + flatten phases).
	running, _, err := doltserver.IsRunning(townRoot)
	if err != nil || !running {
		return fmt.Errorf("Dolt server not running — start with 'gt dolt start'")
	}

	// Phase 0: Build and display maintenance plan.
	fmt.Printf("%s Building maintenance plan...\n", style.Bold.Render("●"))

	databases, err := doltserver.ListDatabases(townRoot)
	if err != nil {
		return fmt.Errorf("listing databases: %w", err)
	}
	if len(databases) == 0 {
		fmt.Printf("%s No databases found — nothing to maintain\n", style.Dim.Render("○"))
		return nil
	}

	dbInfos := make([]maintainDBInfo, 0, len(databases))
	for _, dbName := range databases {
		info := maintainDBInfo{name: dbName}
		if count, err := maintainCountCommits(config, dbName); err == nil {
			info.commitCount = count
		}
		info.backupName = maintainBackupName(config.DataDir, dbName)
		info.hasBackup = info.backupName != ""
		dbInfos = append(dbInfos, info)
	}

	// Display plan.
	flattenCount := 0
	backupCount := 0
	fmt.Printf("\n%s Maintenance plan:\n", style.Bold.Render("●"))
	for _, db := range dbInfos {
		tags := ""
		if db.commitCount >= maintainThreshold {
			tags += fmt.Sprintf(" %s", style.Warning.Render("→ flatten"))
			flattenCount++
		}
		if db.hasBackup {
			tags += fmt.Sprintf(" %s", style.Dim.Render("[backup]"))
			backupCount++
		}
		fmt.Printf("  %s: %d commits%s\n", db.name, db.commitCount, tags)
	}
	fmt.Printf("\n  Databases: %d\n", len(dbInfos))
	fmt.Printf("  Will backup: %d\n", backupCount)
	fmt.Printf("  Will flatten: %d (threshold: %d commits)\n", flattenCount, maintainThreshold)
	fmt.Printf("  Will gc: %d\n", len(dbInfos))

	if maintainDryRun {
		fmt.Printf("\n%s Dry run complete — no changes made\n", style.Dim.Render("ℹ"))
		return nil
	}

	// Interactive confirmation.
	if !maintainForce {
		fmt.Printf("\nProceed? [y/N] ")
		reader := bufio.NewReader(os.Stdin)
		answer, _ := reader.ReadString('\n')
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	start := time.Now()

	// No need to park rigs or stop the server — all operations (flatten, gc)
	// are safe on a running server per Tim Sehn (2026-02-28).

	// Phase 2: Backup.
	// Run the phase unconditionally: when NOTHING is configured we still need to
	// say so. Gating on backupCount > 0 meant a town with zero backups printed
	// nothing at all about backups (hq-vrli).
	if len(dbInfos) > 0 {
		fmt.Printf("\n%s Backing up databases...\n", style.Bold.Render("●"))
		for _, db := range dbInfos {
			if !db.hasBackup {
				// Say so. Silently skipping is how the town's most important
				// database went unbacked 271 times without anyone being told
				// (hq-vrli). gt never runs `dolt backup add`, so an unconfigured
				// database stays unconfigured forever unless a human notices.
				fmt.Printf("  %s %s: NO BACKUP CONFIGURED — not backed up (configure with: cd %s && dolt backup add <name> <url>)\n",
					style.Warning.Render("!"), db.name, filepath.Join(config.DataDir, db.name))
				continue
			}
			if err := maintainBackupSync(config.DataDir, db.name, db.backupName); err != nil {
				fmt.Printf("  %s %s: backup failed: %v\n", style.Warning.Render("!"), db.name, err)
			} else {
				fmt.Printf("  %s %s backed up\n", style.Bold.Render("✓"), db.name)
			}
		}
	}

	// Phase 3: Reap (server up).
	fmt.Printf("\n%s Reaping closed wisps...\n", style.Bold.Render("●"))
	totalReaped := 0
	for _, db := range dbInfos {
		purged, err := doltserver.PurgeClosedEphemerals(townRoot, db.name, false)
		if err != nil {
			fmt.Printf("  %s %s: reap failed: %v\n", style.Warning.Render("!"), db.name, err)
		} else if purged > 0 {
			fmt.Printf("  %s %s: reaped %d wisps\n", style.Bold.Render("✓"), db.name, purged)
			totalReaped += purged
		} else {
			fmt.Printf("  %s %s: nothing to reap\n", style.Dim.Render("○"), db.name)
		}
	}

	// Phase 4: Flatten (server up).
	totalFlattened := 0
	if flattenCount > 0 {
		fmt.Printf("\n%s Flattening databases...\n", style.Bold.Render("●"))
		for _, db := range dbInfos {
			if db.commitCount < maintainThreshold {
				continue
			}
			preCount := db.commitCount
			if err := maintainFlattenDB(config, db.name); err != nil {
				fmt.Printf("  %s %s: flatten failed: %v\n", style.Bold.Render("✗"), db.name, err)
			} else {
				postCount, _ := maintainCountCommits(config, db.name)
				fmt.Printf("  %s %s: %d → %d commits\n", style.Bold.Render("✓"), db.name, preCount, postCount)
				totalFlattened++
			}
		}
	}

	// Phase 5: GC (safe on running server — no downtime needed).
	gcCount := 0
	fmt.Printf("\n%s Running GC (via SQL on running server)...\n", style.Bold.Render("●"))
	for _, db := range dbInfos {
		gcStart := time.Now()
		if err := maintainGCDatabase(config, db.name); err != nil {
			fmt.Printf("  %s %s: gc failed: %v\n", style.Warning.Render("!"), db.name, err)
		} else {
			fmt.Printf("  %s %s: gc completed (%v)\n",
				style.Bold.Render("✓"), db.name, time.Since(gcStart).Round(time.Millisecond))
			gcCount++
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("\n%s Maintenance complete (%v)\n", style.Success.Render("✓"), elapsed.Round(time.Second))
	fmt.Printf("  Wisps reaped: %d\n", totalReaped)
	fmt.Printf("  Databases flattened: %d\n", totalFlattened)
	fmt.Printf("  Databases gc'd: %d\n", gcCount)

	return nil
}

// maintainCountCommits returns the number of Dolt commits in a database.
func maintainCountCommits(config *doltserver.Config, dbName string) (int, error) {
	db, err := maintainOpenDB(config, dbName)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), maintainQueryTimeout)
	defer cancel()

	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.dolt_log", dbName)
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// maintainHasBackup checks if a database has a <name>-backup remote configured.
// maintainBackupName returns the name of the database's configured Dolt backup
// target, or "" if it has none.
//
// It reads the ACTUAL name from `dolt backup` rather than assuming one. The
// previous code looked for a hardcoded "<db>-backup", which nothing creates — the
// real targets in this town are named "backup_export" — so it never matched, every
// database looked unbacked, and the backup phase was skipped entirely while
// reporting nothing. Verified: `dolt backup sync watch_queue_api-backup` returns
// "backup 'watch_queue_api-backup' not found" while `dolt backup sync
// backup_export` succeeds (hq-vrli).
func maintainBackupName(dataDir, dbName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbDir := filepath.Join(dataDir, dbName)
	cmd := exec.CommandContext(ctx, "dolt", "backup")
	cmd.Dir = dbDir

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(output), "\n") {
		// `dolt backup` lists one target per line; some builds append the URL.
		fields := strings.Fields(line)
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// maintainBackupSync runs dolt backup sync for a single database.
func maintainBackupSync(dataDir, dbName, backupName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), maintainBackupTimeout)
	defer cancel()

	dbDir := filepath.Join(dataDir, dbName)
	cmd := exec.CommandContext(ctx, "dolt", "backup", "sync", backupName)
	cmd.Dir = dbDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// maintainOpenDB opens a connection to the Dolt server for a database.
func maintainOpenDB(config *doltserver.Config, dbName string) (*sql.DB, error) {
	// wa-d6f: socket-first DSN (TCP fallback) to avoid TIME_WAIT churn from
	// short-lived gt maintain invocations.
	dsn := buildDoltDSNFromConfig(config, dbName, dsnOpts{
		ParseTime:    true,
		Timeout:      "5s",
		ReadTimeout:  "30s",
		WriteTimeout: "30s",
	})
	return sql.Open("mysql", dsn)
}

// maintainFlattenDB flattens a database's commit history to a single commit.
// Uses direct SQL on the running server — no branches, no downtime.
// Per Tim Sehn (2026-02-28): DOLT_RESET --soft + DOLT_COMMIT is safe on a
// running server. Concurrent writes during flatten are safe.
func maintainFlattenDB(config *doltserver.Config, dbName string) error {
	db, err := maintainOpenDB(config, dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), maintainQueryTimeout)
	defer cancel()

	// Verify connection.
	var dummy int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&dummy); err != nil {
		return fmt.Errorf("connection check: %w", err)
	}

	// Pre-flight: record row counts for integrity verification.
	preCounts, err := flattenGetRowCounts(db, dbName)
	if err != nil {
		return fmt.Errorf("pre-flight row counts: %w", err)
	}

	// Find root commit.
	var rootHash string
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT commit_hash FROM `%s`.dolt_log ORDER BY date ASC LIMIT 1", dbName),
	).Scan(&rootHash); err != nil {
		return fmt.Errorf("find root commit: %w", err)
	}

	// USE database for session-scoped operations.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("USE `%s`", dbName)); err != nil {
		return fmt.Errorf("use database: %w", err)
	}

	// Soft-reset to root on main — all data remains staged.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_RESET('--soft', '%s')", rootHash)); err != nil {
		return fmt.Errorf("soft reset: %w", err)
	}

	// Commit flattened data.
	commitMsg := fmt.Sprintf("maintain: flatten %s history", dbName)
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CALL DOLT_COMMIT('-Am', '%s')", commitMsg)); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Verify integrity: row counts must match pre-flight.
	postCounts, err := flattenGetRowCounts(db, dbName)
	if err != nil {
		return fmt.Errorf("post-flatten row counts: %w", err)
	}
	for table, preCount := range preCounts {
		postCount, ok := postCounts[table]
		if !ok {
			return fmt.Errorf("integrity: table %q missing after flatten", table)
		}
		if preCount != postCount {
			return fmt.Errorf("integrity: %q pre=%d post=%d", table, preCount, postCount)
		}
	}

	return nil
}

// maintainGCDatabase runs dolt gc via SQL on the running server.
// Safe on a running server — no downtime needed (Tim Sehn, 2026-02-28).
func maintainGCDatabase(config *doltserver.Config, dbName string) error {
	db, err := maintainOpenDB(config, dbName)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), maintainGCTimeout)
	defer cancel()

	if _, err := db.ExecContext(ctx, "CALL dolt_gc()"); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("timeout after %v", maintainGCTimeout)
		}
		return fmt.Errorf("dolt_gc: %w", err)
	}
	return nil
}
