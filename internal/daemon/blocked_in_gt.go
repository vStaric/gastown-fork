package daemon

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// minBlockedGtAge is how long a gt process must have been running before its
// presence is taken as evidence that an agent is BLOCKED rather than dead.
//
// Ordinary gt calls finish in about a second. Anything still running after a
// minute is contending for something, and a heartbeat that went stale while
// such a call is in flight is starved, not dead.
const minBlockedGtAge = 60 * time.Second

// blockedInGtCall reports whether a gt process has been running long enough to
// explain a starved heartbeat.
//
// The daemon's stuck-detector cannot otherwise tell "the agent died" from "the
// agent is alive but parked inside a gt call it cannot return from". Those need
// opposite responses: killing a dead agent recovers it, while killing a blocked
// one destroys a LIVE agent mid-call and guarantees a repeat — the respawned
// deacon runs gt hook as its first act and blocks again. That loop killed a
// live deacon 18 times in one daemon.log (hq-z9ze).
//
// Detection is by process age, NOT by walking the agent's process tree: a gt
// call started in the background reparents to init (ppid=1), so an
// ancestry-based check misses exactly the long-lived calls that matter.
//
// Returns the age of the oldest qualifying gt process. Conservative by
// construction: on any inspection failure it reports false, so an unreadable
// process table cannot suppress a legitimate restart.
func blockedInGtCall() (time.Duration, bool) {
	return blockedInGtCallFrom(psGtProcesses())
}

// gtProcess is one observed gt invocation.
type gtProcess struct {
	pid     int
	age     time.Duration
	command string
}

// blockedInGtCallFrom is the testable core of blockedInGtCall.
func blockedInGtCallFrom(procs []gtProcess) (time.Duration, bool) {
	var oldest time.Duration
	found := false
	for _, p := range procs {
		if p.age < minBlockedGtAge {
			continue
		}
		// The daemon's own long-lived helpers are not evidence of a blocked
		// agent: they are SUPPOSED to run forever. Counting them would suppress
		// every restart permanently, which is far worse than the bug being fixed.
		if isLongLivedGtHelper(p.command) {
			continue
		}
		if p.age > oldest {
			oldest = p.age
		}
		found = true
	}
	return oldest, found
}

// isLongLivedGtHelper matches gt subcommands that legitimately run indefinitely.
func isLongLivedGtHelper(command string) bool {
	for _, sub := range []string{
		"gt daemon",
		"gt nudge-poller",
		"gt proxy",
		"gt-proxy",
		"gt web",
		"gt feed",
	} {
		if strings.Contains(command, sub) {
			return true
		}
	}
	return false
}

// psGtProcesses snapshots running gt processes with their elapsed times.
func psGtProcesses() []gtProcess {
	out, err := exec.Command("ps", "-eo", "pid,etime,command").Output()
	if err != nil {
		return nil
	}
	var procs []gtProcess
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		command := strings.Join(fields[2:], " ")
		if !isGtCommand(command) {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		age, ok := parseETime(fields[1])
		if !ok {
			continue
		}
		procs = append(procs, gtProcess{pid: pid, age: age, command: command})
	}
	return procs
}

// isGtCommand reports whether a ps command line is a gt invocation.
// Matches the executable, not any command line that merely mentions "gt".
func isGtCommand(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	exe := fields[0]
	if i := strings.LastIndex(exe, "/"); i >= 0 {
		exe = exe[i+1:]
	}
	return exe == "gt"
}

// parseETime parses ps etime ([[DD-]HH:]MM:SS) into a duration.
func parseETime(s string) (time.Duration, bool) {
	days := 0
	if i := strings.Index(s, "-"); i >= 0 {
		d, err := strconv.Atoi(s[:i])
		if err != nil {
			return 0, false
		}
		days = d
		s = s[i+1:]
	}

	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		nums[i] = n
	}

	var h, m, sec int
	if len(parts) == 3 {
		h, m, sec = nums[0], nums[1], nums[2]
	} else {
		m, sec = nums[0], nums[1]
	}

	return time.Duration(days)*24*time.Hour +
		time.Duration(h)*time.Hour +
		time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second, true
}
