package evalreport

import (
	"encoding/json"
	"strings"
	"time"
)

// decide stamps the episode's decision wall-clock and account.
func (f *fixture) decide(ep *episodeRow, at time.Time, account string) *episodeRow {
	ep.DecidedAt = at
	ep.AccountID = account
	return ep
}

// drafts stores the selected candidate (draft index 1) with the given attachments and, when final is
// non-nil, the human's stored final draft with its own attachments.
func (f *fixture) drafts(ep *episodeRow, candidate []string, final *[]string) {
	art := func(att []string) []byte {
		b, _ := json.Marshal(map[string]any{"channel": "email", "body": "hello", "attachments": att})
		return b
	}
	f.w.candidates[ep.RunID] = map[int]draftJSON{1: {ActionType: "send_email", To: []byte("[]"), CC: []byte("[]"), Artifact: art(candidate)}}
	h := &hsdRow{EpisodeID: ep.ID, SelectedDraft: 1}
	if final != nil {
		h.Final = draftJSON{ActionType: "send_email", To: []byte("[]"), CC: []byte("[]"), Artifact: art(*final)}
	}
	f.w.decisions[ep.ID] = h
}

// ran records that the version produced a row on the episode's run.
func (f *fixture) ran(ep *episodeRow, tag string) {
	ev := tag[:strings.LastIndex(tag, ":v")]
	f.w.allEvals = append(f.w.allEvals, &evalRow{ID: "ran-" + ep.ID, RunID: ep.RunID, DraftIndex: 1,
		Evaluator: ev, Tag: tag, Kind: "human_delta", Verdict: "pass"})
}

func strs(s ...string) *[]string { return &s }
