package stageevents

import (
	"fmt"
	"time"
)

// Deadlines say how long a stage may stay `running` before the reader stops believing it. A process that
// crashed mid-stage never writes the end; without a deadline the stage would be `running` forever and a poller
// would never stop. The Play stages sit inside Play's own timeout (50 s) with a margin; the run's stages cover
// generation, judging and a revision with their per-call budgets; the Cliff covers a Slack post that was
// reserved and never recorded.
var deadlines = map[Stage]time.Duration{
	Ingest: 2 * time.Minute, Resolve: 2 * time.Minute, Graph: 2 * time.Minute, State: 2 * time.Minute,
	Decide: 30 * time.Minute, Evals: 30 * time.Minute, Cliff: 15 * time.Minute,
}

// noHeartbeat is the detail of a running stage past its deadline.
func noHeartbeat(s Stage, d time.Duration) string {
	return fmt.Sprintf("no heartbeat: the %s stage has been running for more than %s without an end being recorded; "+
		"the process that ran it probably stopped, so its outcome is not known", s, d)
}

// withDeadline reports a `running` stage that has outlived its deadline as `unknown` (failure transport: the
// outcome is not known and a retry may succeed). It changes only what is read: the stored row is untouched, so a
// stage that really is still running and finishes late is read correctly on the next poll.
func withDeadline(d StageDoc, now time.Time) StageDoc {
	limit, ok := deadlines[d.Stage]
	if d.Status != Running || !ok || d.StartedAt == nil || now.Sub(*d.StartedAt) <= limit {
		return d
	}
	kind, detail := Transport, noHeartbeat(d.Stage, limit)
	d.Status, d.FailureKind, d.Detail = Unknown, &kind, &detail
	return d
}
