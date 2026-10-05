package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/config"
	"github.com/harneet2512/gtm-work/core-go/internal/providerbreaker"
)

const breakerUsage = "usage: ghostctl breaker [status | reset]"

const breakerTimeout = 15 * time.Second

// runBreaker shows or resets the provider circuit breaker of the running core (GET /provider-breaker,
// POST /provider-breaker/reset, authenticated with GHOST_API_TOKEN). It never touches the database.
func runBreaker(args []string, out io.Writer) error {
	reset := false
	switch {
	case len(args) == 0 || (len(args) == 1 && args[0] == "status"):
	case len(args) == 1 && args[0] == "reset":
		reset = true
	default:
		return errors.New(breakerUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.APIToken == "" {
		return errors.New("GHOST_API_TOKEN is required to reach the core's operator endpoints")
	}
	ctx, cancel := context.WithTimeout(context.Background(), breakerTimeout)
	defer cancel()
	st, err := callBreaker(ctx, coreBaseURL(cfg.CoreAddr), cfg.APIToken, reset)
	if err != nil {
		return err
	}
	if reset {
		fmt.Fprintln(out, "provider circuit breaker reset; model calls resume")
	}
	formatBreaker(out, st)
	return nil
}

func coreBaseURL(addr string) string {
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func callBreaker(ctx context.Context, base, token string, reset bool) (providerbreaker.Status, error) {
	method, path := http.MethodGet, "/provider-breaker"
	if reset {
		method, path = http.MethodPost, "/provider-breaker/reset"
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return providerbreaker.Status{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return providerbreaker.Status{}, fmt.Errorf("core unreachable at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return providerbreaker.Status{}, fmt.Errorf("core answered %d for %s %s", resp.StatusCode, method, path)
	}
	var st providerbreaker.Status
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&st); err != nil {
		return providerbreaker.Status{}, fmt.Errorf("decode breaker status: %w", err)
	}
	return st, nil
}

func formatBreaker(out io.Writer, st providerbreaker.Status) {
	fmt.Fprintf(out, "provider circuit breaker: %s (%d/%d consecutive failures, cool-down %.0fs, tripped %d times, %d calls refused)\n",
		st.State, st.ConsecutiveFailures, st.Threshold, st.CooldownSeconds, st.TripsTotal, st.RejectedTotal)
	if st.OpenedAt != nil {
		fmt.Fprintf(out, "opened at %s\n", st.OpenedAt.UTC().Format(time.RFC3339))
	}
	if st.LastReason != "" {
		fmt.Fprintf(out, "last failure: %s\n", st.LastReason)
	}
}
