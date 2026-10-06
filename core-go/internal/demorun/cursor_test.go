package demorun

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// cursorCore fakes the replay endpoints: n events, the cursor starts at `released`; each episodes/next moves it by one.
func cursorCore(t *testing.T, n, released int, heldOut string, nexts *int) CoreClient {
	t.Helper()
	var mu sync.Mutex
	return fakeCoreServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/episodes/next"):
			released++
			*nexts++
			writeJSON(w, 200, map[string]any{})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/episodes"):
			v := map[string]any{"episode": released, "total": n}
			if released < n {
				id := "history-" + string(rune('a'+released))
				if released == n-1 {
					id = heldOut
				}
				v["next_event"] = map[string]any{"position": released + 1, "event_id": id}
			}
			writeJSON(w, 200, v)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "not_found", "message": "x"}})
		}
	})
}

// The freeze leaves the cursor at 0: the first Play of the web (episodes/next) would release history event 1.
func TestParkCursorReleasesTheHistoryThroughNMinusOneSoTheFirstPlayIsEventN(t *testing.T) {
	nexts := 0
	c := cursorCore(t, 13, 0, "held-out", &nexts)
	cur, err := c.ParkCursorAtEventN(context.Background(), "m-1", "held-out")
	if err != nil || cur.Released != 12 || cur.NextPos != 13 || cur.NextID != "held-out" || nexts != 12 {
		t.Fatalf("cursor %+v nexts %d err %v, want 12 history events released and Event N next", cur, nexts, err)
	}
}

func TestParkCursorIsIdempotentWhenAlreadyAtNMinusOne(t *testing.T) {
	nexts := 0
	c := cursorCore(t, 13, 12, "held-out", &nexts)
	if _, err := c.ParkCursorAtEventN(context.Background(), "m-1", "held-out"); err != nil || nexts != 0 {
		t.Fatalf("err %v nexts %d, want no further release", err, nexts)
	}
}

func TestParkCursorRefusesWhenTheNextEventIsNotTheHeldOutOneOrEventNWasPlayed(t *testing.T) {
	nexts := 0
	c := cursorCore(t, 13, 0, "something-else", &nexts)
	if _, err := c.ParkCursorAtEventN(context.Background(), "m-1", "held-out"); err == nil {
		t.Fatal("the next releasable event is not the manifest's Event N: must refuse")
	}
	played := cursorCore(t, 13, 13, "held-out", new(int))
	if _, err := played.ParkCursorAtEventN(context.Background(), "m-1", "held-out"); err == nil {
		t.Fatal("a cursor past N-1 means Event N was released: the baseline must not be sealed")
	}
}

func TestCheckNextIsEventN(t *testing.T) {
	ok := ReplayCursor{Released: 12, Total: 13, NextID: "e", NextPos: 13}
	if err := CheckNextIsEventN(ok, "e"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ReplayCursor{{Released: 0, Total: 13, NextID: "h1", NextPos: 1}, {Released: 12, Total: 13, NextID: "x", NextPos: 13}, {}} {
		if CheckNextIsEventN(bad, "e") == nil {
			t.Errorf("%+v must fail", bad)
		}
	}
}
