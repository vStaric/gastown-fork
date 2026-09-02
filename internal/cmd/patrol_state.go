package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Patrol state values reported by agentPatrolState.
const (
	// patrolStateNeverArmed: the agent has no patrol wisps at all. It has never
	// patrolled, however healthy its process looks.
	patrolStateNeverArmed = "never-armed"
	// patrolStateActive: the agent has patrolled at least once.
	patrolStateActive = "active"
	// patrolStateUnknown: the wisp query failed. Deliberately distinct from
	// never-armed — reporting a failed lookup as "never armed" would be the same
	// class of bug this exists to fix (a failure indistinguishable from a
	// negative result).
	patrolStateUnknown = "unknown"
)

// PatrolState summarises an agent's patrol history.
type PatrolState struct {
	Count int
	Last  string
	State string
}

// agentPatrolState reports whether an agent has ever patrolled.
//
// This exists because `gt refinery status` and `gt witness status` report only
// process liveness ("running") and the MERGE queue depth ("Queue: N pending").
// Neither is patrol state, so an agent that was NEVER ARMED is indistinguishable
// from a healthy one — api and kbs refineries ran ~6 days with zero patrol wisps
// while reporting "running" (hq-m3yx).
//
// Patrol records are wisps, which live in a separate `wisps` table that `bd list`
// does not query (hq-maii), so this goes through `bd sql` — the same route
// internal/mail uses for wisp messages. `bd mol wisp list --json` is NOT usable
// here: it exposes no assignee field and returns a truncated set.
func agentPatrolState(assignee string) PatrolState {
	if assignee == "" {
		return PatrolState{State: patrolStateUnknown}
	}
	query := fmt.Sprintf(
		"SELECT COUNT(*) AS n, MAX(created_at) AS last FROM wisps WHERE assignee = '%s'",
		escapeSQLLiteral(assignee),
	)
	out, err := BdCmd("sql", "--json", query).Output()
	if err != nil {
		return PatrolState{State: patrolStateUnknown}
	}
	var rows []struct {
		N    int    `json:"n"`
		Last string `json:"last"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 {
		return PatrolState{State: patrolStateUnknown}
	}
	ps := PatrolState{Count: rows[0].N, Last: rows[0].Last}
	if ps.Count == 0 {
		ps.State = patrolStateNeverArmed
		return ps
	}
	ps.State = patrolStateActive
	ps.Last = humanizePatrolTime(ps.Last)
	return ps
}

// humanizePatrolTime renders a wisp timestamp as an age. Returns the input
// unchanged if it cannot be parsed — a display helper must not discard data it
// does not recognise.
func humanizePatrolTime(ts string) string {
	if ts == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// escapeSQLLiteral escapes backslashes and single quotes for Dolt/MySQL string
// literals, matching internal/mail's escapeSQLString. Duplicated rather than
// exported across packages to keep this helper self-contained.
func escapeSQLLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", "''")
}
