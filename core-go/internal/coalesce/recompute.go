package coalesce

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/claimstore"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// Errors a recompute can fail with besides infrastructure errors.
var (
	// ErrVersionConflict: another writer changed the account's state after we read it.
	ErrVersionConflict = errors.New("coalesce: account state changed concurrently")
	// ErrLeaseLost: the job's lease expired and another worker took it over.
	ErrLeaseLost = errors.New("coalesce: job lease lost")
)

// releaseTimeout bounds handing a failed job back even when the caller's context is cancelled.
const releaseTimeout = 10 * time.Second

// RunOnce claims and processes one due job. claimed is false when nothing was due. A failing job
// is released for retry and its error returned with claimed true.
func (s *Service) RunOnce(ctx context.Context) (rec Recompute, claimed bool, err error) {
	if s.breaker != nil && s.breaker.Open() {
		return Recompute{}, false, nil // provider circuit breaker open: claim nothing until it closes
	}
	job, err := s.Claim(ctx)
	if err != nil || job == nil {
		return Recompute{}, false, err
	}
	rec, err = s.process(ctx, *job)
	if err == nil {
		return rec, true, nil
	}
	relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	cause := err
	parkNow := claims.IsProviderUnavailable(err)
	if parkNow {
		cause = fmt.Errorf("parked without retry, provider unavailable (non-retryable): %w", err)
	}
	if relErr := s.release(relCtx, *job, cause, parkNow); relErr != nil {
		err = errors.Join(err, relErr)
	}
	return Recompute{}, true, fmt.Errorf("coalesce: job %d (account %s, attempt %d): %w", job.ID, job.AccountID, job.Attempts, err)
}

// process extracts claims for the job's activities, then adjudicates, folds and persists in one transaction.
func (s *Service) process(ctx context.Context, job Job) (Recompute, error) {
	extracted, extractErr := s.extractClaims(ctx, job)
	if extractErr != nil {
		// A model failure costs only the AI claims: keep every deterministic claim and the AI claims of
		// the activities that did succeed, then fail the job so it retries and finally parks.
		if _, err := claimstore.InsertClaims(ctx, s.db, extracted); err != nil {
			return Recompute{}, errors.Join(extractErr, err)
		}
		return Recompute{}, extractErr
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Recompute{}, fmt.Errorf("coalesce: begin recompute: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	prev, res, adj, err := s.fold(ctx, tx, job, extracted)
	if err != nil {
		return Recompute{}, err
	}
	state := res.Account
	if err := persistState(ctx, tx, prev, state, job.ActivityIDs); err != nil {
		return Recompute{}, err
	}
	if err := persistOpportunities(ctx, tx, res.Opportunities, job.ActivityIDs); err != nil {
		return Recompute{}, err
	}
	if s.detector != nil {
		rel, open, err := s.detector.Detect(ctx, tx, state, res.Opportunities, job.ActivityIDs)
		if err != nil {
			return Recompute{}, fmt.Errorf("coalesce: transition detector: %w", err)
		}
		state.RelationshipState, state.OpenTransition = &rel, open
	}
	if err := s.hook.AfterRecompute(ctx, tx, prev, state, job.ActivityIDs, adj.Conflicts); err != nil {
		return Recompute{}, fmt.Errorf("coalesce: after-recompute hook: %w", err)
	}
	if err := finishJob(ctx, tx, job, s.worker); err != nil {
		return Recompute{}, err
	}
	if err := tx.Commit(); err != nil {
		return Recompute{}, fmt.Errorf("coalesce: commit recompute: %w", err)
	}
	return Recompute{AccountID: job.AccountID, Version: state.Version, ActivityIDs: job.ActivityIDs, Conflicts: adj.Conflicts}, nil
}

// fold writes the extracted claims, adjudicates every claim of the account, stores the status
// changes and reduces the winners into the next account state and one state per deal. prev is nil for the
// account's first state.
func (s *Service) fold(ctx context.Context, tx *sql.Tx, job Job, extracted []claims.Claim) (*reducer.AccountState, reducer.Result, claims.Adjudication, error) {
	none := reducer.Result{}
	if _, err := claimstore.InsertClaims(ctx, tx, extracted); err != nil {
		return nil, none, claims.Adjudication{}, err
	}
	all, err := claimstore.LoadAccountClaims(ctx, tx, job.AccountID)
	if err != nil {
		return nil, none, claims.Adjudication{}, err
	}
	now := s.clk.Now()
	adjudicateAt := now
	if s.adjClk != nil {
		adjudicateAt = s.adjClk.Now()
	}
	adj := claims.Adjudicate(all, adjudicateAt)
	if err := claimstore.ApplyUpdates(ctx, tx, adj.Updates); err != nil {
		return nil, none, claims.Adjudication{}, err
	}
	facts, err := loadAccountFacts(ctx, tx, job.AccountID, all)
	if err != nil {
		return nil, none, claims.Adjudication{}, err
	}
	prev, err := lockPreviousState(ctx, tx, job.AccountID)
	if err != nil {
		return nil, none, claims.Adjudication{}, err
	}
	version := 1
	if prev != nil {
		version = prev.Version + 1
	}
	res := reducer.ReduceAll(reducer.Input{
		AccountID: job.AccountID, AccountName: facts.name, Version: version, ComputedAt: now,
		Adjudication: adj, Activities: facts.activities, People: facts.people, OwnDomain: s.ownDomain,
	})
	for _, w := range res.Warnings {
		s.log.WarnContext(ctx, "coalesce: claim skipped during fold", "account", job.AccountID, "warning", w)
	}
	return prev, res, adj, nil
}

// finishJob deletes the in-flight job; it fails when another worker took the lease over.
func finishJob(ctx context.Context, tx *sql.Tx, job Job, worker string) error {
	res, err := tx.ExecContext(ctx, `DELETE FROM recompute_jobs WHERE id = $1 AND claimed_by = $2 AND claimed_at IS NOT NULL`, job.ID, worker)
	if err != nil {
		return fmt.Errorf("coalesce: delete finished job: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrLeaseLost
	}
	return nil
}

// extractClaims runs the rule and model extractors over the job's activities. It uses the pool, not
// the recompute transaction, so a slow worker call never holds row locks.
//
// Liveness: only an error classified permanent (a malformed payload, a request the worker rejects as
// invalid) quarantines the activity, which is then skipped so one poison activity never blocks its
// account. Every other failure (a worker outage, a timeout, a cache read error) is retryable: it is
// returned together with the claims extracted so far, which include the deterministic claims of the
// activities whose model call failed, so the job retries and finally parks without losing them.
func (s *Service) extractClaims(ctx context.Context, job Job) ([]claims.Claim, error) {
	acts, err := loadActivityInputs(ctx, s.db, job.ActivityIDs)
	if err != nil {
		return nil, err
	}
	known, err := loadKnownPeople(ctx, s.db, job.AccountID)
	if err != nil {
		return nil, err
	}
	skip, err := s.quarantinedAmong(ctx, job.ActivityIDs)
	if err != nil {
		return nil, err
	}
	dir := claimstore.Directory{DB: s.db}
	pipe := claims.Pipeline{Rules: claims.RuleExtractor{Dir: dir}, LLM: s.extractor, Cache: claimstore.Cache{DB: s.db}, Version: s.version}
	var out []claims.Claim
	var retryable []error
	for _, act := range acts {
		if skip[act.ID] || act.AccountID != job.AccountID { // quarantined, or re-attributed (its new account's job covers it)
			continue
		}
		if err := s.Renew(ctx, job); err != nil { // heartbeat: serial model calls must not outlast the lease
			return out, err
		}
		res, err := pipe.Run(ctx, act, known, s.resolver(ctx, act, known, dir))
		out = append(out, res.Claims...)
		if err != nil {
			if qerr := s.quarantinePermanent(ctx, job, act.ID, err); qerr != nil {
				return out, qerr
			}
			if claims.IsProviderUnavailable(err) {
				return out, err // every further call would fail (and cost) the same way: stop at the first
			}
			if !claims.IsPermanent(err) {
				retryable = append(retryable, err)
			}
			continue
		}
		s.log.DebugContext(ctx, "coalesce: extracted", "activity", act.ID, "claims", len(res.Claims), "skipped", len(res.Skipped), "dropped", len(res.Dropped), "llm_calls", res.LLMCalls)
	}
	if err := s.Renew(ctx, job); err != nil {
		return out, err
	}
	return out, errors.Join(retryable...)
}

// quarantinePermanent quarantines the activity when the error is permanent; any other error is a no-op.
func (s *Service) quarantinePermanent(ctx context.Context, job Job, activityID string, cause error) error {
	if !claims.IsPermanent(cause) {
		return nil
	}
	s.log.WarnContext(ctx, "coalesce: quarantining activity", "activity", activityID, "account", job.AccountID, "attempts", job.Attempts)
	return s.quarantine(ctx, job, activityID, cause, true)
}

// resolver resolves identities through the activity's participants and the account's people, then
// by email address through the directory.
func (s *Service) resolver(ctx context.Context, act claims.ActivityInput, known []claims.KnownPerson, dir claims.Directory) claims.Resolver {
	ix := claims.NewIdentityIndex(act.Participants, known)
	return func(identity string) (string, bool) {
		if id, ok := ix.Resolve(identity); ok {
			return id, true
		}
		if strings.Contains(identity, "@") {
			if id, found, err := dir.PersonIDByEmail(ctx, identity); err == nil && found {
				return id, true
			}
		}
		return "", false
	}
}

// lockPreviousState reads and row-locks the account's current state; nil when it has none yet.
func lockPreviousState(ctx context.Context, tx *sql.Tx, accountID string) (*reducer.AccountState, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT state FROM account_state WHERE account_id = $1::uuid FOR UPDATE`, accountID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("coalesce: read previous state: %w", err)
	}
	var prev reducer.AccountState
	if err := json.Unmarshal(raw, &prev); err != nil {
		return nil, fmt.Errorf("coalesce: decode previous state of %s: %w", accountID, err)
	}
	return &prev, nil
}

// persistState writes state_history and upserts account_state with an optimistic version check.
func persistState(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, st reducer.AccountState, trigger []string) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("coalesce: encode state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO state_history (account_id, version, as_of, computed_at, trigger_activity_ids, state)
VALUES ($1::uuid, $2, $3, $4, $5::uuid[], $6::jsonb)`, st.AccountID, st.Version, st.AsOf, st.ComputedAt, pgTextArray(trigger), string(raw)); err != nil {
		if isUniqueViolation(err) { // another writer produced this version first
			return ErrVersionConflict
		}
		return fmt.Errorf("coalesce: insert state history v%d: %w", st.Version, err)
	}
	opp, last := nullable(st.OpportunityID), nullable(st.LastActivityID)
	if prev == nil {
		_, err = tx.ExecContext(ctx, `
INSERT INTO account_state (account_id, opportunity_id, version, as_of, computed_at, last_activity_id, state)
VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7::jsonb)`, st.AccountID, opp, st.Version, st.AsOf, st.ComputedAt, last, string(raw))
		if isUniqueViolation(err) {
			return ErrVersionConflict
		}
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `
UPDATE account_state SET opportunity_id = $2::uuid, version = $3, as_of = $4, computed_at = $5, last_activity_id = $6::uuid, state = $7::jsonb
 WHERE account_id = $1::uuid AND version = $8`, st.AccountID, opp, st.Version, st.AsOf, st.ComputedAt, last, string(raw), prev.Version)
		if err == nil {
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrVersionConflict
			}
		}
	}
	if err != nil {
		return fmt.Errorf("coalesce: write account state v%d: %w", st.Version, err)
	}
	return nil
}

func nullable(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
