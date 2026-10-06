package demorun

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// cursorParker is the part of a real core client (CoreClient, and LiveCore that embeds it) that parks the replay cursor; the Play-only
// fakes of the flow tests do not have it.
type cursorParker interface {
	ParkCursorAtEventN(ctx context.Context, manifestID, heldOutEventID string) (ReplayCursor, error)
}

// ReplayCursor is the part of GET /replay/manifests/{id}/episodes that says what the next Play releases.
type ReplayCursor struct {
	Released int // episode: how many events are released (0 until the first advance)
	Total    int // N: the position of the held-out event
	NextID   string
	NextPos  int
}

// ReplayCursor reads where the replay cursor stands.
func (c CoreClient) ReplayCursor(ctx context.Context, manifestID string) (ReplayCursor, error) {
	var v struct {
		Episode   int `json:"episode"`
		Total     int `json:"total"`
		NextEvent *struct {
			Position int    `json:"position"`
			EventID  string `json:"event_id"`
		} `json:"next_event"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/replay/manifests/"+url.PathEscape(manifestID)+"/episodes", nil, &v); err != nil {
		return ReplayCursor{}, err
	}
	out := ReplayCursor{Released: v.Episode, Total: v.Total}
	if v.NextEvent != nil {
		out.NextID, out.NextPos = v.NextEvent.EventID, v.NextEvent.Position
	}
	return out, nil
}

// NextEpisode is POST /replay/manifests/{id}/episodes/next: the path of the web Play button.
func (c CoreClient) NextEpisode(ctx context.Context, manifestID string) error {
	_, err := c.do(ctx, http.MethodPost, "/replay/manifests/"+url.PathEscape(manifestID)+"/episodes/next", map[string]string{}, nil)
	return err
}

// ParkCursorAtEventN releases the history events through N-1 (the web Play path, one at a time) so the first Play releases exactly
// Event N, then checks that the next releasable event is the held-out one. The freeze leaves the cursor at 0, where the first Play
// would release history event 1. A cursor already past N-1 (Event N played) is an error: the baseline must be sealed before Play.
func (c CoreClient) ParkCursorAtEventN(ctx context.Context, manifestID, heldOutEventID string) (ReplayCursor, error) {
	cur, err := c.ReplayCursor(ctx, manifestID)
	if err != nil {
		return cur, fmt.Errorf("read the replay cursor: %w", err)
	}
	for cur.Released < cur.Total-1 {
		if err := c.NextEpisode(ctx, manifestID); err != nil {
			return cur, fmt.Errorf("release history event %d: %w", cur.Released+1, err)
		}
		if cur, err = c.ReplayCursor(ctx, manifestID); err != nil {
			return cur, fmt.Errorf("read the replay cursor: %w", err)
		}
	}
	return cur, CheckNextIsEventN(cur, heldOutEventID)
}

// CheckNextIsEventN fails unless the next releasable event is Event N, at position N, with the cursor at N-1.
func CheckNextIsEventN(cur ReplayCursor, heldOutEventID string) error {
	if cur.Total < 1 || cur.Released != cur.Total-1 || cur.NextPos != cur.Total || cur.NextID != heldOutEventID {
		return fmt.Errorf("the replay cursor is at %d of %d and the next releasable event is %q (position %d); the first Play must release Event N (%s) at position N-1+1",
			cur.Released, cur.Total, cur.NextID, cur.NextPos, heldOutEventID)
	}
	return nil
}
