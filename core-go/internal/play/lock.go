package play

import (
	"context"
	"database/sql/driver"
	"fmt"
	"hash/fnv"
)

// lockKey is the advisory-lock key of one manifest's Play.
func lockKey(manifestID string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("ghost.play:" + manifestID))
	return int64(h.Sum64())
}

// lock takes the manifest's session advisory lock on a dedicated connection, without waiting: a Play that
// finds another one running answers ErrPlayInProgress at once. A waiting lock would park each concurrent Play
// on a pooled connection until the first finished, so enough of them would starve the pool for everyone.
func (s *Service) lock(ctx context.Context, manifestID string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("play: connection for the play lock: %w", err)
	}
	key := lockKey(manifestID)
	var got bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("play: lock manifest %s: %w", manifestID, err)
	}
	if !got {
		_ = conn.Close()
		return nil, ErrPlayInProgress
	}
	return func() {
		if _, err := conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, key); err != nil {
			// The lock is still held by this session: never hand the connection back to the pool with it.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = conn.Close()
	}, nil
}
