package play

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Reset is POST /replay/manifests/{id}/reset: move the released cursor back to `episode`, so the released
// events are exactly 1..episode (empty at 0). The bookkeeping rows stay — they are the record of what
// happened — and the cursor is what the view and the next advance read. Deterministic: two resets to the
// same episode answer the same released events and the same digest over them and their state digests.
func (s *Service) Reset(ctx context.Context, manifestID string, episode int) ([]byte, error) {
	seq, err := LoadSequence(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	if episode < 0 || episode > seq.N() {
		return nil, fmt.Errorf("%w: %d is not a position of %s", ErrInvalidEpisode, episode, manifestID)
	}
	unlock, err := s.lock(ctx, seq.ManifestID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	maxPos, err := maxReleasedPosition(ctx, s.db, manifestID)
	if err != nil {
		return nil, err
	}
	if episode > maxPos {
		return nil, fmt.Errorf("%w: episode %d was never released (only 0..%d were)", ErrInvalidEpisode, episode, maxPos)
	}
	at := s.clk.Now().UTC()
	if err := setCursor(ctx, s.db, manifestID, episode, at); err != nil {
		return nil, err
	}
	releasedEvents := make([]string, 0, episode)
	for pos := 1; pos <= episode; pos++ {
		ev, _ := seq.At(pos)
		releasedEvents = append(releasedEvents, ev.EventID)
	}
	digest, err := s.resetDigest(ctx, seq, episode)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"manifest_id":     seq.ManifestID,
		"account_id":      seq.AccountID,
		"episode":         episode,
		"released_events": releasedEvents,
		"reset_at":        at,
		"digest":          digest,
	})
}

// resetDigest is the SHA-256 of the released event ids paired with the digest of the state each ended in —
// the fingerprint two resets to the same episode must share (episode_replay.v1.json resetResult.digest).
func (s *Service) resetDigest(ctx context.Context, seq Sequence, k int) (string, error) {
	h := sha256.New()
	for pos := 1; pos <= k; pos++ {
		ev, _ := seq.At(pos)
		var digest string
		st, err := s.stateAt(ctx, seq.AccountID, pos, episodeBound(seq, pos))
		if err != nil {
			return "", err
		}
		if st != nil {
			digest = st.Digest
		}
		if _, err := fmt.Fprintf(h, "%s %s\n", ev.EventID, digest); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
