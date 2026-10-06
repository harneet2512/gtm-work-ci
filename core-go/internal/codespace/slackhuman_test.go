package codespace

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

const fakeCoreToken = "slack-human-test-token-0123456789"

// humanRig is the production SlackHuman over the contract-valid fake core: every call is a real HTTP call to core's API.
type humanRig struct {
	core  *slacksurface.CoreHTTP
	human *SlackHuman
	state demorun.DemoState
}

func newHumanRig(t *testing.T) *humanRig {
	t.Helper()
	srv, err := fakecore.New(fakecore.Options{Token: fakeCoreToken})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	core := slacksurface.NewCoreHTTP(ts.URL, slacksurface.Secret(fakeCoreToken), nil)
	h := &SlackHuman{Core: core, WebURL: "http://127.0.0.1:3000",
		Wait: func(ctx context.Context, what string, ready func() (bool, error)) error {
			ok, err := ready()
			if err != nil || ok {
				return err
			}
			return errors.New(what)
		}}
	return &humanRig{core: core, human: h, state: demorun.DemoState{ManifestID: "man", AccountID: slacksurface.FixtureAccountID,
		RunID: slacksurface.FixtureRunID, EpisodeID: slacksurface.FixtureEpisodeID}}
}

func TestSlackHumanPlaysTheDemoPathThroughTheRealCliffHandlers(t *testing.T) {
	r := newHumanRig(t)
	var said []string
	r.human.Say = func(format string, args ...any) { said = append(said, format) }
	s := DefaultScript()
	path, err := r.human.Act(context.Background(), Case{Label: "Acme"}, r.state, s)
	if err != nil {
		t.Fatalf("Act: %v", err)
	}
	if path.ChoiceButton != "Select B" || path.ChoiceRank != 2 || path.ChoiceTitle == "" {
		t.Fatalf("the script disagrees with Ghost: rank 2 is the second button: %+v", path)
	}
	if !strings.HasSuffix(path.Subject, s.EditSubjectSuffix) || !strings.HasSuffix(path.Body, strings.TrimSpace(s.EditBodyAppend)) {
		t.Fatalf("subject %q and body %q must carry the scripted edit", path.Subject, path.Body)
	}
	if path.To == "" || path.Interpretation == "" || path.Correction != s.Correction || path.Note != "" {
		t.Fatalf("path = %+v", path)
	}

	// What core holds is what the presenter's identical typing would produce: Slack surface, the edit, the dry-run send, the correction.
	ctx := context.Background()
	dec, err := r.core.GetStrategyDecision(ctx, r.state.RunID)
	if err != nil || dec.Surface != slacksurface.SurfaceSlack || dec.SendDecision != slacksurface.SendSend || dec.FinalArtifact == nil {
		t.Fatalf("decision = %+v %v", dec, err)
	}
	if dec.FinalArtifact.SubjectText() != path.Subject || strings.TrimSpace(dec.FinalArtifact.Body) != path.Body {
		t.Fatalf("core holds %q / %q, the run sheet says %q / %q", dec.FinalArtifact.SubjectText(), dec.FinalArtifact.Body, path.Subject, path.Body)
	}
	inf, err := r.core.GetJudgmentInference(ctx, r.state.EpisodeID)
	if err != nil || inf.HumanVerdict != slacksurface.VerdictCorrected || inf.CorrectedStatement == nil || *inf.CorrectedStatement != s.Correction {
		t.Fatalf("inference = %+v %v", inf, err)
	}
	if !strings.Contains(strings.Join(said, "|"), "presses") || !strings.Contains(strings.Join(said, "|"), "corrects") {
		t.Fatalf("progress lines = %v", said)
	}
}

func TestSlackHumanTypesTheNoteWhenTheScriptHasOne(t *testing.T) {
	r := newHumanRig(t)
	s := DefaultScript()
	s.Note = "  for the champion  "
	path, err := r.human.Act(context.Background(), Case{Label: "Acme"}, r.state, s)
	if err != nil || path.Note != "for the champion" {
		t.Fatalf("path = %+v %v", path, err)
	}
	inf, _ := r.core.GetJudgmentInference(context.Background(), r.state.EpisodeID)
	if inf.HumanNote == nil || *inf.HumanNote != "for the champion" {
		t.Fatalf("the note is typed beside the correction: %+v", inf.HumanNote)
	}
}

func TestSlackHumanFailsLoudlyWhenTheWorldIsNotAtTheStart(t *testing.T) {
	r := newHumanRig(t)
	if _, err := r.human.Act(context.Background(), Case{Label: "Acme"}, r.state, DefaultScript()); err != nil {
		t.Fatal(err)
	}
	// the run was already decided and sent: Message 2 no longer offers Select, which a recording must not paper over
	if _, err := r.human.Act(context.Background(), Case{Label: "Acme"}, r.state, DefaultScript()); err == nil {
		t.Fatal("a second pass over a played run must fail")
	}
}

func TestSlackHumanNamesTheStepThatCannotBeReached(t *testing.T) {
	r := newHumanRig(t)
	missing := r.state
	missing.AccountID = "0a0c0000-0000-4000-8000-0000000000ff"
	if _, err := r.human.Act(context.Background(), Case{Label: "Acme"}, missing, DefaultScript()); err == nil || !strings.Contains(err.Error(), "Message 1") {
		t.Fatalf("err = %v", err)
	}
	r = newHumanRig(t)
	r.human.Wait = func(_ context.Context, what string, _ func() (bool, error)) error {
		return errors.New(what + " within 15m0s")
	}
	_, err := r.human.Act(context.Background(), Case{Label: "Acme"}, r.state, DefaultScript())
	if err == nil || !strings.Contains(err.Error(), "judgment inference") {
		t.Fatalf("err = %v", err)
	}
	r = newHumanRig(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.human.Act(cancelled, Case{Label: "Acme"}, r.state, DefaultScript()); err == nil {
		t.Fatal("a cancelled context must stop the path")
	}
}
