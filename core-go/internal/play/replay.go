package play

import (
	"errors"
)

// Episode Replay errors the API maps to statuses (HAR-129 §B/§C).
var (
	// ErrReplayComplete: every event of the manifest's sequence is already released (409 replay_complete).
	ErrReplayComplete = errors.New("play: the replay is complete")
	// ErrInvalidEpisode: a cursor or reset target outside the released range (422 invalid_episode).
	ErrInvalidEpisode = errors.New("play: episode out of range")
	// ErrEpisodeTrail: a history event of the manifest left no trail in the stores — the frozen world never
	// materialized it, so the episode cannot be released honestly (422 episode_not_materialized).
	ErrEpisodeTrail = errors.New("play: the episode's event left no trail in the world")
	// ErrKnowledgeUnavailable: the knowledge lifecycle rules the as-of read needs are not configured (503).
	ErrKnowledgeUnavailable = errors.New("play: the knowledge lifecycle rules are not configured")
)

// checkReplay validates the Episode Replay options of an Options.
func (o Options) checkReplay() error {
	if o.HistoricalEnd != nil && *o.HistoricalEnd < 0 {
		return errors.New("play: the historical window end cannot be negative")
	}
	return nil
}
