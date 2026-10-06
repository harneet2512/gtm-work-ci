package play

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/stageevents"
)

// tracker records the stages of one Play as the code executes them (HAR-145: no timers, no placeholders). It is
// the Play's view of stageevents: every method is safe without a recorder, and a recording error is logged and
// dropped — the pipeline's work must not fail because its bookkeeping did, and a stage that could not be written
// simply stays as it was (waiting, or stale running), never invented.
type tracker struct {
	rec   *stageevents.Recorder
	scope stageevents.Scope
	db    *sql.DB
}

func (s *Service) tracker(m Manifest) tracker {
	return tracker{rec: s.stages, db: s.db, scope: stageevents.Scope{ManifestID: m.ID, AccountID: m.AccountID}}
}

func (t tracker) begin(ctx context.Context, stage stageevents.Stage) *stageevents.Handle {
	h, err := t.rec.Begin(ctx, t.scope, stage)
	if err != nil {
		slog.WarnContext(ctx, "play: could not record the start of a stage", "stage", stage, "manifest_id", t.scope.ManifestID, "error", err)
		return nil
	}
	return h
}

// complete records that a stage ran to its end. It says nothing about quality: `passed` is an eval verdict.
func (t tracker) complete(ctx context.Context, h *stageevents.Handle, out stageevents.Outcome) {
	t.finish(ctx, h, stageevents.Completed, out)
}

// finish records a stage's end with the status its artifact supports.
func (t tracker) finish(ctx context.Context, h *stageevents.Handle, status stageevents.Status, out stageevents.Outcome) {
	if err := h.Finish(ctx, status, out); err != nil {
		slog.WarnContext(ctx, "play: could not record the end of a stage", "manifest_id", t.scope.ManifestID, "error", err)
	}
}

func (t tracker) fail(ctx context.Context, h *stageevents.Handle, err error) {
	t.failWhile(ctx, h, err, "")
}

func (t tracker) failWhile(ctx context.Context, h *stageevents.Handle, err error, while string) {
	if werr := h.FailWhile(ctx, stageFailure(err), while); werr != nil {
		slog.WarnContext(ctx, "play: could not record a stage failure", "manifest_id", t.scope.ManifestID, "error", werr)
	}
}

// skip records the stages a non-material event never owes (the decision belongs to a run that will not exist).
func (t tracker) skip(ctx context.Context, detail string, stages ...stageevents.Stage) {
	for _, s := range stages {
		if err := t.rec.Record(ctx, t.scope, s, stageevents.Skipped, stageevents.Outcome{Detail: detail}); err != nil {
			slog.WarnContext(ctx, "play: could not record a skipped stage", "stage", s, "manifest_id", t.scope.ManifestID, "error", err)
		}
	}
}

// stageFailure classifies the Play's own errors for the stage record: a timeout or an unreachable dependency is
// a transport problem (never a verdict); a release that contradicts the manifest is a contract problem.
func stageFailure(err error) error {
	var visible *VisibleError
	switch {
	case errors.Is(err, ErrTimeout), errors.Is(err, ErrGraphUnavailable), errors.Is(err, ErrSourceUnavailable):
		return stageevents.MarkTransport(err)
	case errors.Is(err, ErrReleaseMismatch), errors.As(err, &visible):
		return stageevents.MarkContract(err)
	default:
		return err
	}
}

// stateOutcome is what the recompute left for the released activity: the diff it folded into, the state
// version it reached and the trigger evaluation of that diff. The stage is completed only when the diff is found
// and named in its refs: a stage that cannot point at its artifact is `unknown`, never completed.
func (t tracker) stateOutcome(ctx context.Context, accountID, activityID string) (stageevents.Status, stageevents.Outcome) {
	if t.rec == nil {
		return stageevents.Completed, stageevents.Outcome{} // recording is off: read nothing
	}
	var out stageevents.Outcome
	var version int
	err := t.db.QueryRowContext(ctx, `SELECT id::text, to_version FROM state_diffs
 WHERE account_id = $1::uuid AND activity_ids @> ARRAY[$2]::uuid[] ORDER BY to_version LIMIT 1`, accountID, activityID).
		Scan(&out.Refs.StateDiffID, &version)
	if err != nil {
		return stageevents.Unknown, stageevents.Outcome{Detail: "the state diff of this event could not be read, so the state stage has no ref to show"}
	}
	out.Refs.StateVersion = version
	var eligible bool
	var codes []byte
	err = t.db.QueryRowContext(ctx, `SELECT id::text, eligible, to_jsonb(reason_codes) FROM trigger_evaluations
 WHERE state_diff_id = $1::uuid ORDER BY evaluated_at, id LIMIT 1`, out.Refs.StateDiffID).Scan(&out.Refs.TriggerEvaluationID, &eligible, &codes)
	switch {
	case err != nil:
		out.Detail = fmt.Sprintf("state v%d", version)
	case eligible:
		out.Detail = fmt.Sprintf("state v%d; trigger eligible", version)
	default:
		out.Detail = fmt.Sprintf("state v%d; trigger not eligible (%s)", version, reasonList(codes))
	}
	return stageevents.Completed, out
}

func reasonList(raw []byte) string {
	var codes []string
	if json.Unmarshal(raw, &codes) != nil || len(codes) == 0 {
		return "no reason recorded"
	}
	return strings.Join(codes, ", ")
}

// graphOutcome is the graph diff the projection recorded for the released source event. Without one the stage
// has nothing to point at and is `unknown`, never completed.
func (t tracker) graphOutcome(ctx context.Context, sourceEventID string) (stageevents.Status, stageevents.Outcome) {
	if t.rec == nil {
		return stageevents.Completed, stageevents.Outcome{} // recording is off: read nothing
	}
	var id sql.NullInt64
	if err := t.db.QueryRowContext(ctx, `SELECT min(id) FROM graph_projection_diffs WHERE $1::uuid = ANY (source_event_ids)`, sourceEventID).Scan(&id); err != nil || !id.Valid {
		return stageevents.Unknown, stageevents.Outcome{Detail: "no graph diff was recorded for this event, so the graph stage has no ref to show"}
	}
	return stageevents.Completed, stageevents.Outcome{Refs: stageevents.Refs{GraphDiffID: id.Int64}, Detail: "projected"}
}

// endGraph ends the graph stage, once the change is committed, with the status its graph diff supports.
func (t tracker) endGraph(ctx context.Context, h *stageevents.Handle, sourceEventID string) {
	status, out := t.graphOutcome(ctx, sourceEventID)
	t.finish(ctx, h, status, out)
}

// decisionOwed records what the trigger decided: an event that is not eligible owes no decision, so the decide,
// evals and cliff stages are skipped, with the trigger's own reason. An eligible event leaves them waiting for
// the run it opened.
func (t tracker) decisionOwed(ctx context.Context, eligible bool, reasons []string) {
	if eligible {
		return
	}
	why := "the trigger found nothing to decide"
	if len(reasons) > 0 {
		why += ": " + strings.Join(reasons, ", ")
	}
	t.skip(ctx, why, stageevents.Decide, stageevents.Evals, stageevents.Cliff)
}

// changeWritten attaches the AccountChange (and its update) to the resolve stage once they exist: the change is
// written after the state and graph stages, in the Play's last transaction.
func (t tracker) changeWritten(ctx context.Context, changeID string) {
	if t.rec == nil {
		return // recording is off: read nothing
	}
	refs := stageevents.Refs{AccountChangeID: changeID}
	var bi sql.NullString
	if err := t.db.QueryRowContext(ctx, `SELECT id::text FROM business_intelligence_updates WHERE account_change_id = $1::uuid`, changeID).Scan(&bi); err == nil && bi.Valid {
		refs.BIUpdateID = bi.String
	}
	if err := t.rec.MergeRefs(ctx, t.scope, stageevents.Resolve, refs); err != nil {
		slog.WarnContext(ctx, "play: could not attach the account change to the resolve stage", "manifest_id", t.scope.ManifestID, "error", err)
	}
}
