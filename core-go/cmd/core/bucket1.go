package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/harneet2512/gtm-work/core-go/internal/bucket1run"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/strategystore"
	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// newBucket1Runner builds the Bucket 1 (B1-B9) runner. Its model gates call the worker's normal judge endpoint (one
// call per gate per episode), so the one-time recording captures them; without a worker URL there is no judge and the
// semantic assertions read "not measured" while the deterministic ones still run.
func newBucket1Runner(db *sql.DB, cfg config.Config, logger *slog.Logger) (*bucket1run.Runner, error) {
	var judge bucket1run.Judge
	if cfg.WorkerURL != "" {
		client, err := workerclient.New(cfg.WorkerURL, workerTimeoutOptions(cfg)...)
		if err != nil {
			return nil, err
		}
		judge = client
	} else {
		logger.Warn("no worker URL: the Bucket 1 model assertions (B1, B3, B5, B8) will read not measured")
	}
	return bucket1run.New(db, judge, nil, logger)
}

// bothGates runs the Bucket 2 gates and then regrades Bucket 1's B9 after every human step of the real path.
type bothGates struct {
	b2 strategystore.GateRunner
	b1 *bucket1run.Runner
}

func (g bothGates) RunGates(ctx context.Context, runID string) error {
	return errors.Join(g.b2.RunGates(ctx, runID), g.b1.RunGates(ctx, runID))
}
