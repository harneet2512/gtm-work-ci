package slacksurface

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func blockAction(actionID, triggerID, channel, ts string, t Target) map[string]any {
	return map[string]any{
		"type": "block_actions", "trigger_id": triggerID,
		"user":      map[string]any{"id": "U1", "name": "alex"},
		"channel":   map[string]any{"id": channel},
		"container": map[string]any{"type": "message", "channel_id": channel, "message_ts": ts},
		"actions":   []any{map[string]any{"action_id": actionID, "block_id": "b", "type": "button", "value": t.Encode()}},
	}
}

func viewSubmit(viewID, callback string, t Target, values map[string]string) map[string]any {
	state := map[string]any{}
	for block, v := range values {
		state[block] = map[string]any{inputAction: map[string]any{"type": "plain_text_input", "value": v}}
	}
	return map[string]any{
		"type": "view_submission", "user": map[string]any{"id": "U1", "name": "alex"},
		"view": map[string]any{
			"id": viewID, "callback_id": callback, "private_metadata": t.Encode(),
			"state": map[string]any{"values": state},
		},
	}
}

func contains(raw json.RawMessage, sub string) bool { return strings.Contains(string(raw), sub) }

// TestFullSlackFlow drives the whole demo through the real slack-go client and Socket Mode
// library against a fake Slack server and a fake core API over real HTTP:
// post M1, post M2, Choose, update in place, Edit, Send, post M3, Needs correction, modal submit.
func TestFullSlackFlow(t *testing.T) {
	fs := newFakeSlack(t)
	core := newMemCore()
	coreClient := NewCoreHTTP(httpCore(t, core).URL, testToken, nil)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := Config{AppToken: "xapp-test", BotToken: "xoxb-test", ChannelID: "C0TEST"}
	api, smc := NewSlackClients(cfg, fs.apiURL())
	h := NewHandler(coreClient, NewSlackPoster(api), cfg.ChannelID, "https://ghost.example.test", quiet)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, smc, h, quiet) }()
	defer func() { cancel(); <-served }()
	fs.waitConnected()

	fx := core.fx
	titleB, _ := fx.Strategies.Candidate(fixtureCandB)
	pub := h.Publisher()
	// 1-2. Message 1 and Message 2; reposting in one process does not post again.
	var chooserRef MessageRef
	for i := 0; i < 2; i++ {
		if _, err := pub.PostBI(ctx, FixtureAccountID); err != nil {
			t.Fatal(err)
		}
		ref, err := pub.PostChooser(ctx, FixtureRunID)
		if err != nil {
			t.Fatal(err)
		}
		chooserRef = ref
	}
	posts := fs.callsOf("chat.postMessage")
	if len(posts) != 2 {
		t.Fatalf("posted %d messages after M1+M2 (reposted once), want 2", len(posts))
	}
	// The repeat found both message refs in core and refreshed the two messages in place (HAR-136): two updates, no post.
	refreshed := len(fs.callsOf("chat.update"))
	if refreshed != 2 {
		t.Fatalf("the repeated delivery made %d chat.update calls, want 2 (M1 and M2 refreshed in place)", refreshed)
	}
	if !contains(posts[0].Blocks, "View account map") || strings.Count(string(posts[1].Blocks), `"action_id":"`+ActionStrategyChoose+`"`) != 3 {
		t.Fatal("M1 must carry View account map and M2 three Choose buttons")
	}

	// 3-4. Choose B (Ghost preferred A): the same message is updated in place.
	tgtB := Target{RunID: FixtureRunID, CandidateID: fixtureCandB}
	choose := blockAction(ActionStrategyChoose, "T1", "C0TEST", chooserRef.TS, tgtB)
	fs.push(choose)
	upd := fs.waitCalls("chat.update", refreshed+1)
	if upd[refreshed+0].TS != chooserRef.TS || upd[refreshed+0].Channel != "C0TEST" {
		t.Fatalf("update must target the chooser message: %+v", upd[refreshed+0])
	}
	if !contains(upd[refreshed+0].Blocks, "Selected strategy: "+titleB.Title) || !contains(upd[refreshed+0].Blocks, ActionSelectedSend) || !contains(upd[refreshed+0].Blocks, ActionSelectedEdit) {
		t.Fatalf("updated message is not the selected action with Edit/Send: %s", upd[refreshed+0].Blocks)
	}
	if len(fs.callsOf("chat.postMessage")) != 2 {
		t.Fatal("choosing must not post a new channel message")
	}
	fs.push(choose) // Slack redelivers the same interaction: ignored

	// 5. Edit opens a modal prefilled from the current artifact.
	fs.push(blockAction(ActionSelectedEdit, "T2", "C0TEST", chooserRef.TS, Target{RunID: FixtureRunID}))
	opened := fs.waitCalls("views.open", 1)
	if opened[0].Trigger != "T2" {
		t.Fatalf("modal must be opened with the click's trigger_id, got %q", opened[0].Trigger)
	}
	views := fs.waitCalls("views.update", 1) // the loading modal is replaced by the filled-in one
	if !contains(views[0].View, CallbackEditModal) || !contains(views[0].View, "Marco ") || !contains(views[0].View, "marco@acme.example.test") {
		t.Fatalf("edit modal wrong: %s", views[0].View)
	}

	// An unknown recipient is answered inline in the modal; nothing is written.
	meta := Target{RunID: FixtureRunID, CandidateID: fixtureCandB, ChannelID: "C0TEST", MessageTS: chooserRef.TS}
	ack := fs.push(viewSubmit("V1", CallbackEditModal, meta, map[string]string{
		inputTo: "nobody@nowhere.test", inputCC: "", inputSubject: "S", inputBody: "B"}))
	if p, _ := json.Marshal(ack["payload"]); !strings.Contains(string(p), "No person on this account") {
		t.Fatalf("unknown recipient must be answered with a view error, got %s", p)
	}

	// Valid edit: message 2 is updated in place again.
	fs.push(viewSubmit("V2", CallbackEditModal, meta, map[string]string{
		inputTo: "Marco <marco@acme.example.test>", inputCC: "priya@acme.example.test",
		inputSubject: "Edited subject", inputBody: "Edited body"}))
	upd = fs.waitCalls("chat.update", refreshed+2)
	if upd[refreshed+1].TS != chooserRef.TS || !contains(upd[refreshed+1].Blocks, "Edited subject") || !contains(upd[refreshed+1].Blocks, "Edited before send") {
		t.Fatalf("edit did not update message 2 in place: %s", upd[refreshed+1].Blocks)
	}

	// 6-7. Explicit Send updates message 2 and posts message 3 exactly once.
	fs.push(blockAction(ActionSelectedSend, "T3", "C0TEST", chooserRef.TS, Target{RunID: FixtureRunID}))
	upd = fs.waitCalls("chat.update", refreshed+3)
	if !contains(upd[refreshed+2].Blocks, "Sent by slack:U1 (alex)") || contains(upd[refreshed+2].Blocks, ActionSelectedSend) {
		t.Fatalf("sent state wrong: %s", upd[refreshed+2].Blocks)
	}
	posts = fs.waitCalls("chat.postMessage", 3)
	if !contains(posts[2].Blocks, "Ghost originally preferred") || !contains(posts[2].Blocks, ActionJudgmentCorrect) {
		t.Fatalf("message 3 wrong: %s", posts[2].Blocks)
	}
	judgmentTS := posts[2].TS
	fs.push(blockAction(ActionSelectedSend, "T3b", "C0TEST", chooserRef.TS, Target{RunID: FixtureRunID})) // double click: 409 already_decided
	fs.waitCalls("chat.update", refreshed+4)

	// 8-9. Needs correction opens a modal; its submission updates message 3 in place.
	tgtJ := Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID}
	fs.push(blockAction(ActionJudgmentCorrect, "T4", "C0TEST", judgmentTS, tgtJ))
	fs.waitCalls("views.open", 2)
	views = fs.waitCalls("views.update", 2)
	if !contains(views[1].View, CallbackCorrectionModal) {
		t.Fatalf("correction modal wrong: %s", views[1].View)
	}
	metaJ := Target{EpisodeID: FixtureEpisodeID, RunID: FixtureRunID, ChannelID: "C0TEST", MessageTS: judgmentTS}
	fs.push(viewSubmit("V3", CallbackCorrectionModal, metaJ, map[string]string{inputCorrection: "The sponsor owns it", inputNote: "n"}))
	upd = fs.waitCalls("chat.update", refreshed+5)
	if upd[refreshed+4].TS != judgmentTS || !contains(upd[refreshed+4].Blocks, "Corrected:") || !contains(upd[refreshed+4].Blocks, "The sponsor owns it") {
		t.Fatalf("correction did not update message 3 in place: %s", upd[refreshed+4].Blocks)
	}

	// The channel never gets more than three messages; every write came from the slack surface.
	if n := len(fs.callsOf("chat.postMessage")); n != 3 {
		t.Fatalf("channel messages = %d, want exactly 3", n)
	}
	var decisions, sends int
	for _, w := range core.writes() {
		if !strings.Contains(w, `"surface":"slack"`) || !strings.Contains(w, `"actor_label":"slack:U1 (alex)"`) {
			t.Fatalf("core write without slack surface/actor: %s", w)
		}
		switch {
		case strings.HasPrefix(w, "strategy-decision"):
			decisions++
		case strings.HasPrefix(w, "send"):
			sends++
		}
	}
	if decisions != 2 || core.sends != 1 || sends != 2 {
		t.Fatalf("decisions=%d (want choose+edit=2, retry deduped) sends=%d/%d (two attempts, one real send)", decisions, sends, core.sends)
	}
}
