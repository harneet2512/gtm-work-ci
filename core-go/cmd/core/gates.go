package main

import (
	"database/sql"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket2run"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/recompute"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// attachGates gives the strategy store its Bucket 2 gate runner (HAR-97 D1-D10), so the gates run inside the real
// path: after the choice and each edit, the send, and the answer to Message 3. The model judges go through the worker
// (/v1/decision-judge) like every other model call, so the one-time recording captures them. Without a worker URL
// there is no judge: the deterministic gates still run and the model gates stay "not measured".
func attachGates(db *sql.DB, cfg config.Config, logger *slog.Logger, strategies *strategystore.Service) error {
	var judge bucket2run.Judge
	if cfg.WorkerURL != "" {
		client, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return err
		}
		judge = client
	} else {
		logger.Warn("no worker URL: the Bucket 2 model gates (D1-D3, D8) will read not measured")
	}
	rec, err := recompute.New(db, nil)
	if err != nil {
		return err
	}
	runner, err := bucket2run.New(db, strategies, rec, judge)
	if err != nil {
		return err
	}
	b1, err := newBucket1Runner(db, cfg, logger)
	if err != nil {
		return err
	}
	strategies.SetGates(bothGates{b2: runner, b1: b1}) // Bucket 2 gates, then Bucket 1's B9, after every human step
	return nil
}
