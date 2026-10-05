package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/coalesce"
	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const jobsUsage = "usage: ghostctl jobs | ghostctl jobs unquarantine <activity-id>"

const jobsTimeout = 2 * time.Minute

// runJobs lists the recompute work that needs an operator (parked jobs, quarantined activities), or
// lifts the quarantine of one activity and re-enqueues its account.
func runJobs(args []string, out io.Writer) error {
	unquarantine := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "unquarantine":
		unquarantine = args[1]
	default:
		return errors.New(jobsUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))
	ctx, cancel := context.WithTimeout(context.Background(), jobsTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	svc, err := coalesce.New(db, coalesce.Options{})
	if err != nil {
		return err
	}
	if unquarantine != "" {
		if err := svc.Unquarantine(ctx, unquarantine); err != nil {
			return err
		}
		fmt.Fprintf(out, "activity %s released from quarantine; its account is queued for recompute\n", unquarantine)
		return nil
	}
	parked, err := svc.Parked(ctx)
	if err != nil {
		return err
	}
	quarantined, err := svc.Quarantined(ctx)
	if err != nil {
		return err
	}
	formatJobs(out, parked, quarantined)
	return nil
}

// formatJobs prints the operator report.
func formatJobs(out io.Writer, parked []coalesce.ParkedJob, quarantined []coalesce.QuarantinedActivity) {
	fmt.Fprintf(out, "parked jobs: %d\n", len(parked))
	for _, p := range parked {
		fmt.Fprintf(out, "  account %s (%s) job %d: %d activities, %d attempts, parked %s ago: %s\n",
			p.AccountID, p.AccountName, p.JobID, p.ActivityCount, p.Attempts, p.Age.Round(time.Second), p.LastError)
	}
	fmt.Fprintf(out, "quarantined activities: %d\n", len(quarantined))
	for _, q := range quarantined {
		kind := "retries exhausted"
		if q.Permanent {
			kind = "permanent"
		}
		fmt.Fprintf(out, "  activity %s account %s, %s, %d attempts, quarantined %s ago: %s\n",
			q.ActivityID, q.AccountID, kind, q.Attempts, q.Age.Round(time.Second), q.Reason)
	}
}
