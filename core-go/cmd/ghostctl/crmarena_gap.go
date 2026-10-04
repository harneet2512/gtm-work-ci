package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/crmarena"
	"github.com/harneet2512/gtm-work/core-go/internal/store"
)

const storeCheckTimeout = time.Minute

// checkNoGap refuses a replay that starts later than the store was loaded to. An `import-crmarena
// --until` earlier than the replay's --from leaves the events in between neither in the store nor in the
// replay: the agent would silently miss them. The check counts the stored source events dated before
// from and compares them with the export's own count; fewer in the store is a gap. When no database
// is configured the check cannot run and says so on warn; skip turns it off, loudly.
func checkNoGap(events []crmarena.Event, from time.Time, skip bool, warn io.Writer) error {
	if skip {
		fmt.Fprintln(warn, "ghostctl: WARNING: --skip-store-check: the store is not verified to hold every event before --from")
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(warn, "ghostctl: WARNING: no database configured (%v): not verified that the store was imported up to %s\n", err, from.Format(time.RFC3339))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), storeCheckTimeout)
	defer cancel()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("ghostctl: open the store to verify the cutoff (or pass --skip-store-check): %w", err)
	}
	defer db.Close()
	want, _, err := crmarena.AsOf(events, from)
	if err != nil {
		return err
	}
	var have int
	// Only the loader's own events count: other sources in the same database must not mask a gap.
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM source_events WHERE occurred_at < $1 AND connector = $2`,
		from, crmarena.Connector).Scan(&have); err != nil {
		return fmt.Errorf("ghostctl: count stored events: %w", err)
	}
	if have < len(want) {
		return fmt.Errorf("ghostctl: the store holds %d events dated before %s but the export has %d: it was imported with an earlier "+
			"--until, so replaying from %s would silently skip %d events; import with --until %s first (or pass --skip-store-check)",
			have, from.Format(time.RFC3339), len(want), from.Format(time.RFC3339), len(want)-have, from.Format(time.RFC3339))
	}
	return nil
}
