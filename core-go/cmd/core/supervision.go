package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/reactions"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
)

// newSupervision builds the HAR-120 supervision service: scanning turns follow-on activity into
// customer reactions and business outcomes whose evidence lands on knowledge through the lifecycle
// rules — so the service needs GHOST_KNOWLEDGE_RULES, the same file the episode view's as-of read
// uses. With no rules file the endpoint is not served and the coalescer never scans.
func newSupervision(db *sql.DB, cfg config.Config, logger *slog.Logger) (*reactions.Service, error) {
	if cfg.KnowledgeRulesPath == "" {
		logger.Warn("GHOST_KNOWLEDGE_RULES is empty: supervision scanning is off and /episodes/{id}/reactions is not served")
		return nil, nil
	}
	rules, err := knowledge.LoadRules(cfg.KnowledgeRulesPath)
	if err != nil {
		return nil, fmt.Errorf("core: knowledge rules: %w", err)
	}
	return reactions.New(db, rules, reactions.Options{SilenceWindow: cfg.SupervisionSilence})
}

// withSupervision wraps the recompute hook: after inner has written everything it owns inside the
// recompute transaction, the account's sent episodes are scanned for supervision in that same
// transaction, so a reaction row, its knowledge evidence and the state the scan ran on commit (or
// roll back) atomically.
func withSupervision(svc *reactions.Service) func(coalesce.Hook) coalesce.Hook {
	return func(inner coalesce.Hook) coalesce.Hook {
		return coalesce.HookFunc(func(ctx context.Context, tx *sql.Tx, prev *reducer.AccountState, next reducer.AccountState,
			activityIDs []string, conflicts []claims.Conflict) error {
			if err := inner.AfterRecompute(ctx, tx, prev, next, activityIDs, conflicts); err != nil {
				return err
			}
			_, err := svc.ScanAccountTx(ctx, tx, next.AccountID)
			return err
		})
	}
}
