package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const crmarenaReportUsage = "usage: ghostctl crmarena-report [--out <report.json>]"

// Distribution summarizes activity counts per entity.
type Distribution struct {
	Entities int     `json:"entities"`
	Min      int     `json:"min"`
	Median   float64 `json:"median"`
	P90      float64 `json:"p90"`
	Max      int     `json:"max"`
	Mean     float64 `json:"mean"`
}

// IngestReport is what the HAR-96 pipeline made of an ingested CRMArena snapshot (HAR-130 item 4).
type IngestReport struct {
	Activities         int            `json:"activities"`
	ByType             map[string]int `json:"activities_by_type"`
	Unresolved         int            `json:"unresolved"`
	UnresolvedByReason map[string]int `json:"unresolved_by_reason"`
	PerAccount         Distribution   `json:"activities_per_account"`
	PerDeal            Distribution   `json:"activities_per_deal"`
	OppHintNoOpp       map[string]int `json:"opportunity_hint_without_opportunity_by_type"`
	Entities           map[string]int `json:"entities"`
	Edges              map[string]int `json:"open_edges_by_rel_and_standing"`
	Anomalies          map[string]int `json:"resolution_anomalies"`
}

// reportQueries are single-number entity and anomaly counts.
var reportQueries = map[string]map[string]string{
	"entities": {
		"accounts":                    `SELECT count(*) FROM accounts`,
		"accounts_with_domain":        `SELECT count(*) FROM accounts WHERE domain IS NOT NULL`,
		"employees":                   `SELECT count(*) FROM people WHERE kind = 'employee'`,
		"contacts":                    `SELECT count(*) FROM people WHERE kind = 'contact'`,
		"opportunities":               `SELECT count(*) FROM opportunities`,
		"opportunities_with_owner":    `SELECT count(*) FROM opportunities WHERE owner_person_id IS NOT NULL`,
		"current_identity_mappings":   `SELECT count(*) FROM entity_source_mappings WHERE valid_to IS NULL`,
		"activities_with_opportunity": `SELECT count(*) FROM activities WHERE opportunity_id IS NOT NULL`,
	},
	"anomalies": {
		"contacts_created_by_email_domain_rule": `SELECT count(*) FROM entity_source_mappings WHERE entity_type = 'person' AND method = 'rule' AND evidence->>'rule' = 'email_domain_account'`,
		"contacts_without_account":              `SELECT count(*) FROM people WHERE kind = 'contact' AND account_id IS NULL`,
		"contacts_at_our_domain":                `SELECT count(*) FROM people WHERE kind = 'contact' AND primary_email LIKE '%@` + normalize.OurDomain + `'`,
		"accounts_sharing_a_name":               `SELECT coalesce(sum(n), 0) FROM (SELECT count(*) AS n FROM accounts GROUP BY name HAVING count(*) > 1) d`,
		"accounts_without_activity":             `SELECT count(*) FROM accounts a WHERE NOT EXISTS (SELECT 1 FROM activities x WHERE x.account_id = a.id)`,
		"participants_without_person":           `SELECT count(*) FROM activity_participants WHERE person_id IS NULL`,
		"people_participants_without_person":    `SELECT count(*) FROM activity_participants WHERE person_id IS NULL AND raw_identity NOT LIKE 'integration:%'`,
		"activities_on_another_accounts_deal":   `SELECT count(*) FROM activities x JOIN opportunities o ON o.id = x.opportunity_id WHERE o.account_id <> x.account_id`,
		"opportunities_without_belongs_to":      `SELECT count(*) FROM opportunities o WHERE NOT EXISTS (SELECT 1 FROM relationships r WHERE r.src_type = 'opportunity' AND r.src_id = o.id AND r.rel_type = 'belongs_to' AND r.valid_to IS NULL)`,
		"contacts_with_two_open_works_at":       `SELECT count(*) FROM (SELECT src_id FROM relationships WHERE src_type = 'person' AND rel_type = 'works_at' AND valid_to IS NULL GROUP BY src_id HAVING count(DISTINCT dst_id) > 1) d`,
	},
}

func runCRMArenaReport(args []string, out io.Writer) error {
	path := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--out":
		path = args[1]
	default:
		return errors.New(crmarenaReportUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	rep, err := buildReport(ctx, db)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("ghostctl: encode report: %w", err)
	}
	raw = append(raw, '\n')
	if path == "" {
		_, err = out.Write(raw)
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("ghostctl: write report: %w", err)
	}
	fmt.Fprintf(out, "report written to %s\n", path)
	return nil
}

func buildReport(ctx context.Context, db *sql.DB) (IngestReport, error) {
	var r IngestReport
	var err error
	steps := []func() error{
		func() error { return db.QueryRowContext(ctx, `SELECT count(*) FROM activities`).Scan(&r.Activities) },
		func() error {
			r.ByType, err = countBy(ctx, db, `SELECT activity_type, count(*) FROM activities GROUP BY 1`)
			return err
		},
		func() error {
			return db.QueryRowContext(ctx, `SELECT count(*) FROM activities WHERE account_id IS NULL`).Scan(&r.Unresolved)
		},
		func() error {
			r.UnresolvedByReason, err = countBy(ctx, db, `SELECT reason, count(*) FROM unresolved_activities GROUP BY 1`)
			return err
		},
		func() error {
			r.PerAccount, err = distribution(ctx, db, `SELECT count(x.id) FROM accounts a LEFT JOIN activities x ON x.account_id = a.id GROUP BY a.id`)
			return err
		},
		func() error {
			r.PerDeal, err = distribution(ctx, db, `SELECT count(x.id) FROM opportunities o LEFT JOIN activities x ON x.opportunity_id = o.id GROUP BY o.id`)
			return err
		},
		func() error {
			r.OppHintNoOpp, err = countBy(ctx, db, `SELECT activity_type, count(*) FROM activities WHERE opportunity_hint IS NOT NULL AND opportunity_id IS NULL GROUP BY 1`)
			return err
		},
		func() error {
			r.Edges, err = countBy(ctx, db, `SELECT rel_type || '/' || standing, count(*) FROM relationships WHERE valid_to IS NULL GROUP BY 1`)
			return err
		},
		func() error { r.Entities, err = scalars(ctx, db, reportQueries["entities"]); return err },
		func() error { r.Anomalies, err = scalars(ctx, db, reportQueries["anomalies"]); return err },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return IngestReport{}, fmt.Errorf("ghostctl: report: %w", err)
		}
	}
	return r, nil
}

func countBy(ctx context.Context, db *sql.DB, query string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

func scalars(ctx context.Context, db *sql.DB, queries map[string]string) (map[string]int, error) {
	out := map[string]int{}
	for name, q := range queries {
		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = n
	}
	return out, nil
}

// distribution summarizes one count per row of the query.
func distribution(ctx context.Context, db *sql.DB, query string) (Distribution, error) {
	const q = `SELECT count(*), coalesce(min(n), 0), coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY n), 0),
	coalesce(percentile_cont(0.9) WITHIN GROUP (ORDER BY n), 0), coalesce(max(n), 0), coalesce(avg(n), 0)::float8 FROM (`
	var d Distribution
	err := db.QueryRowContext(ctx, q+query+`) AS per(n)`).Scan(&d.Entities, &d.Min, &d.Median, &d.P90, &d.Max, &d.Mean)
	return d, err
}
