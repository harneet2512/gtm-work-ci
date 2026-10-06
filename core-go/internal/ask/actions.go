package ask

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// PlayNextPath is the route web Play posts (contracts/demo/boundary.v1.json visible_trigger.core_endpoint).
const PlayNextPath = "/replay/manifests/%s/episodes/next"

const progressPath = "/replay/manifests/%s/progress"

// stageWords are the pipeline stages in product words.
var stageWords = map[string]string{"ingest": "Event received", "resolve": "Account matched", "graph": "Graph updated",
	"state": "State recomputed", "decide": "Strategies drafted", "evals": "Evals", "cliff": "Cliff messages"}

func refused(reason, msg string) ActionResult {
	return ActionResult{Status: "refused", MessageMarkdown: msg, Reason: reason}
}

// RunAction runs one confirmed demo action. The adapter calls it only after a human pressed Run.
func (s *Service) RunAction(ctx context.Context, req ActionRequest) (ActionResult, error) {
	if !validKind(req.Kind) || req.User == "" || len(req.User) > MaxUserChars {
		return ActionResult{}, fmt.Errorf("%w: kind and user are required", ErrBadRequest)
	}
	if req.Kind == ActionPlayNext {
		return s.playNext(ctx)
	}
	return s.demoStatus(ctx)
}

func (s *Service) manifestPath(format string) (string, bool) {
	if s.manifestID == "" {
		return "", false
	}
	return fmt.Sprintf(format, url.PathEscape(s.manifestID)), true
}

// playNext posts the route web Play posts, so the recorded answers replay exactly the same way.
func (s *Service) playNext(ctx context.Context) (ActionResult, error) {
	path, ok := s.manifestPath(PlayNextPath)
	if !ok {
		return refused("not_configured", "The demo case is not set up here, so I cannot play the next event."), nil
	}
	status, body, err := s.backend.Post(ctx, path)
	if err != nil {
		return ActionResult{}, err
	}
	switch {
	case status == 200:
		var r struct {
			Episode int `json:"episode"`
			Total   int `json:"total"`
		}
		_ = json.Unmarshal(body, &r)
		msg := "The next event is released."
		if r.Episode > 0 && r.Total > 0 {
			msg = fmt.Sprintf("Released event %d of %d. The control plane and the channel update as the pipeline finishes.", r.Episode, r.Total)
		}
		if l := s.links.Replay(s.manifestID); l != "" {
			msg += " [Open the replay](" + l + ")"
		}
		return ActionResult{Status: "done", MessageMarkdown: msg}, nil
	case status == 409:
		return refused(errCode(body), conflictText(errCode(body))), nil
	case status == 404, status == 422, status == 503:
		return refused(errCode(body), "The next event could not be played ("+strings.ReplaceAll(errCode(body), "_", " ")+")."), nil
	}
	return ActionResult{}, fmt.Errorf("ask: core answered %d to play next", status)
}

func conflictText(code string) string {
	switch code {
	case "replay_complete":
		return "Every event of this case is already released. Press Play in the control plane to continue to the next case."
	case "play_in_progress":
		return "Another release is still running. Try again in a moment."
	}
	return "The next event could not be played right now."
}

func errCode(body []byte) string {
	var env struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Error.Code == "" {
		return "unknown"
	}
	return env.Error.Code
}

func (s *Service) demoStatus(ctx context.Context) (ActionResult, error) {
	path, ok := s.manifestPath(progressPath)
	if !ok {
		return refused("not_configured", "The demo case is not set up here, so there is no replay status."), nil
	}
	status, body, err := s.backend.Get(ctx, path)
	if err != nil {
		return ActionResult{}, err
	}
	if status != 200 {
		return refused(errCode(body), "I could not read the replay status."), nil
	}
	var p struct {
		Overall string `json:"overall"`
		Stages  []struct {
			Stage  string `json:"stage"`
			Status string `json:"status"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return ActionResult{}, fmt.Errorf("ask: replay progress: %w", err)
	}
	lines := []string{"Replay status: " + strings.ReplaceAll(p.Overall, "_", " ") + "."}
	for _, st := range p.Stages {
		name := stageWords[st.Stage]
		if name == "" {
			name = strings.ReplaceAll(st.Stage, "_", " ")
		}
		lines = append(lines, "• "+name+": "+strings.ReplaceAll(st.Status, "_", " "))
	}
	if l := s.links.Replay(s.manifestID); l != "" {
		lines = append(lines, "[Open the replay]("+l+")")
	}
	return ActionResult{Status: "done", MessageMarkdown: strings.Join(lines, "\n")}, nil
}
