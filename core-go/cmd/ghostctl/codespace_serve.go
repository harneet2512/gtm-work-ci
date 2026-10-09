package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/codespace"
)

// keepServed is the served count to record: this run's, unless it served nothing (a re-check of seeded cases freezes nothing) and the
// previous baseline was built under the same allowlist file, in which case the freeze's own count stays.
func keepServed(prev *codespace.KnownMissNote, manifestSHA256 string, served int) int {
	if prev != nil && served == 0 && prev.ManifestSHA256 == manifestSHA256 {
		return prev.Served
	}
	return served
}

// replayKnownMisses reads the replay worker's counters: the size of its known-miss allowlist and how many allowlisted prompts it served.
func replayKnownMisses(ctx context.Context, workerURL string) (allowed, served int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, workerURL+"/replay-stats", nil)
	if err != nil {
		return 0, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("ghostctl: read the replay worker's counters: %w", err)
	}
	defer resp.Body.Close()
	var st struct {
		Served  int `json:"known_miss_hits"`
		Allowed int `json:"known_misses"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&st) != nil {
		return 0, 0, errors.New("ghostctl: the worker is not the replay worker (no /replay-stats)")
	}
	return st.Allowed, st.Served, nil
}

// codespaceServe runs the control service until the process is told to stop.
func codespaceServe(ctx context.Context, rt codespace.Runtime, out io.Writer) error {
	srv := rt.Server()
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	fmt.Fprintf(out, "control service listening on %s\n", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ghostctl: control service: %w", err)
	}
	<-done
	return nil
}
