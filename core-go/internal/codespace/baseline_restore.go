package codespace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RestoreStats says what a restore copied and how long each phase took (summed over the components, which run side by side,
// so the phases can add up to more than Duration).
type RestoreStats struct {
	Files    int
	Bytes    int64
	Duration time.Duration
	Copy     time.Duration
	Swap     time.Duration
	Delete   time.Duration
	// Leftover lists old copies that could not be deleted (a lock); the next restore removes them first.
	Leftover []string
}

// Restore makes the live state equal to the baseline: for each sealed path it copies the baseline beside the live one, then
// swaps the directories by rename and deletes the old one. The components are restored side by side. The baseline is verified
// first, so a missing or corrupt one fails before anything is touched. The stores must be stopped. Nothing is rebuilt, migrated or
// computed; the model-call cache is not among the paths and is never touched.
func (b Baseline) Restore(ctx context.Context) (RestoreStats, error) {
	started := time.Now()
	m, err := b.Manifest()
	if err != nil {
		return RestoreStats{}, err
	}
	if err := b.Verify(); err != nil {
		return RestoreStats{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		st   RestoreStats
		errs []error
	)
	for _, c := range m.Components {
		wg.Add(1)
		go func(c Component) {
			defer wg.Done()
			r, err := b.restoreOne(ctx, c)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("codespace: restore %s: %w", c.Path, err))
				cancel() // the others stop at their next step
				return
			}
			st.Files += c.Files
			st.Bytes += c.Bytes
			st.Copy += r.copy
			st.Swap += r.swap
			st.Delete += r.delete
			st.Leftover = append(st.Leftover, r.leftover...)
		}(c)
	}
	wg.Wait()
	st.Duration = time.Since(started)
	return st, errors.Join(errs...)
}

type restored struct {
	copy, swap, delete time.Duration
	leftover           []string
}

func (b Baseline) restoreOne(ctx context.Context, c Component) (r restored, err error) {
	src, live := filepath.Join(b.Dir(), filepath.FromSlash(c.Path)), b.abs(c.Path)
	fresh, old := live+".restore", live+".old"
	for _, stale := range []string{fresh, old} { // a crashed or locked earlier restore
		if err := b.removeAll(ctx, stale); err != nil {
			return r, err
		}
	}
	t := time.Now()
	p, err := copyTree(ctx, src, fresh, false)
	r.copy = time.Since(t)
	if err != nil {
		_ = os.RemoveAll(fresh)
		return r, err
	}
	if p.Files != c.Files || p.Bytes != c.Bytes || p.Layout != c.Layout {
		_ = os.RemoveAll(fresh)
		return r, bad("the copy of %s does not match the manifest", c.Path)
	}
	t = time.Now()
	_, statErr := os.Stat(live)
	hadLive := statErr == nil
	if hadLive {
		if err := b.retry(ctx, func() error { return b.ren(live, old) }); err != nil {
			_ = os.RemoveAll(fresh)
			return r, err
		}
	}
	if err := b.retry(ctx, func() error { return b.ren(fresh, live) }); err != nil {
		if hadLive {
			_ = b.ren(old, live) // put the previous state back
		}
		_ = os.RemoveAll(fresh)
		return r, err
	}
	r.swap = time.Since(t)
	if hadLive {
		t = time.Now()
		if err := b.removeAll(ctx, old); err != nil {
			r.leftover = append(r.leftover, old)
		}
		r.delete = time.Since(t)
	}
	return r, nil
}

func (b Baseline) retries() int {
	if b.Retries > 0 {
		return b.Retries
	}
	return 10
}

func (b Baseline) backoff() time.Duration {
	if b.Backoff > 0 {
		return b.Backoff
	}
	return 300 * time.Millisecond
}

// retry runs op until it succeeds, waiting a little longer after each failure: Windows refuses to rename or delete a directory
// while a process (the antivirus scanner, a service still shutting down) holds a file in it.
func (b Baseline) retry(ctx context.Context, op func() error) error {
	var err error
	for attempt := 1; attempt <= b.retries(); attempt++ {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = op(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * b.backoff()):
		}
	}
	return fmt.Errorf("after %d attempts: %w", b.retries(), err)
}

// removeAll deletes a path (a missing one is fine), retrying file locks.
func (b Baseline) removeAll(ctx context.Context, p string) error {
	return b.retry(ctx, func() error { return os.RemoveAll(p) })
}
