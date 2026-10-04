package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const seedOrgUsage = "usage: ghostctl seed-org <org.json>"

const seedOrgTimeout = 2 * time.Minute

// runSeedOrg upserts the vendor's employees (people, email and Slack identities, reporting
// lines) from a company file such as fixtures/world/org.json. It is idempotent. The file is
// validated before the database is touched.
func runSeedOrg(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(seedOrgUsage)
	}
	company, err := graph.LoadCompany(args[0])
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "target database host: %s\n", hostOf(cfg.DatabaseURL))

	ctx, cancel := context.WithTimeout(context.Background(), seedOrgTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	rep, err := graph.SeedCompany(ctx, db, company, time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "seeded %d employees: %d created, %d updated\n", len(company.People), rep.Created, rep.Updated)
	return nil
}
