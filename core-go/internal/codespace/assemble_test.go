package codespace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

type assembleRig struct {
	flow     demorun.Flow
	core     *httptest.Server
	prepared []bool // noSlack of each Prepare call
	built    []string
}

func newAssembleRig(t *testing.T) *assembleRig {
	t.Helper()
	r := &assembleRig{}
	r.core = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer t0k" || !strings.HasSuffix(req.URL.Path, "/invisibility") {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"manifest_id":"man-1","status":"withheld"}`))
	}))
	t.Cleanup(r.core.Close)
	cfg := testBase()
	cfg.DotEnv = demorun.Env{"OPENROUTER_API_KEY": "or-key", "SLACK_CHANNEL_ID": "C0DEMO"}
	r.flow = demorun.Flow{
		Cfg:  cfg,
		Core: &demorun.LiveCore{CoreClient: demorun.CoreClient{Base: r.core.URL, Token: "t0k"}},
		Prepare: func(_ context.Context, noSlack, noWeb bool) (demorun.Tooling, error) {
			r.prepared = append(r.prepared, noSlack)
			if noWeb {
				t.Error("the codespace always needs the web app")
			}
			return demorun.Tooling{Host: "/bin/host", Npm: "/bin/npm"}, nil
		},
		RunTool:      func([]string) error { return nil },
		ApplyToolEnv: func(demorun.Env) error { return nil },
	}
	return r
}

func slackOK(demorun.Env, demorun.Env) (demorun.Env, error) {
	return demorun.Env{"SLACK_BOT_TOKEN": "xoxb-1", "SLACK_APP_TOKEN": "xapp-1"}, nil
}

func slackMissing(demorun.Env, demorun.Env) (demorun.Env, error) {
	return nil, errors.New("missing SLACK_BOT_TOKEN, SLACK_APP_TOKEN")
}

func (r *assembleRig) options(resolve func(demorun.Env, demorun.Env) (demorun.Env, error)) Options {
	return Options{ResolveSlack: resolve, BuildWeb: func(_ context.Context, npm string) error { r.built = append(r.built, npm); return nil }}
}

func TestAssembleServesTheProductionWebAppAndTellsItWhereControlIs(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	if !rt.Cfg.WebProd || rt.Cfg.WebExtraEnv["GHOST_DEMO_CONTROL_URL"] != "http://127.0.0.1:8099" {
		t.Fatalf("web config = %v %v", rt.Cfg.WebProd, rt.Cfg.WebExtraEnv.Names())
	}
	if rt.ControlURL() != "http://127.0.0.1:8099" || rt.ListenAddress() != "127.0.0.1:8099" {
		t.Fatalf("control = %s %s", rt.ControlURL(), rt.ListenAddress())
	}
	if rt.Control.Token == "" || rt.Control.Token == "t0k" {
		t.Fatal("the control service has its own per-boot token, not the core API token")
	}
	if rt.Status.Job == nil {
		t.Fatal("the status must show the control service's running job")
	}
	if len(rt.Ops.Cases) != 2 || rt.Seeder.BaseDSN != rt.Cfg.DSN() {
		t.Fatalf("ops = %+v", rt.Ops)
	}
}

func TestAssembleReadinessCoversSlackOnlyWhenItsTokensArePresent(t *testing.T) {
	r := newAssembleRig(t)
	with := Assemble(r.flow, r.options(slackOK))
	if with.Cfg.NoSlack || !contains(with.Status.Required, "slackbot") {
		t.Fatalf("with tokens: NoSlack=%v required=%v", with.Cfg.NoSlack, with.Status.Required)
	}
	if missing := missingOf(with); len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	without := Assemble(r.flow, r.options(slackMissing))
	if !without.Cfg.NoSlack || contains(without.Status.Required, "slackbot") {
		t.Fatalf("without tokens: NoSlack=%v required=%v", without.Cfg.NoSlack, without.Status.Required)
	}
	if got := strings.Join(missingOf(without), ","); got != "SLACK_BOT_TOKEN,SLACK_APP_TOKEN" {
		t.Fatalf("missing = %s, want the tokens named so the header can say so", got)
	}
}

func missingOf(rt Runtime) []string {
	var out []string
	for _, n := range rt.Status.Secrets {
		if !rt.Status.Present(n) {
			out = append(out, n)
		}
	}
	return out
}

func TestAssembleRequiresTheModelKeyOnlyInLiveMode(t *testing.T) {
	r := newAssembleRig(t)
	if got := requiredSecrets(Assemble(r.flow, r.options(slackOK)).Cfg); got[0] != "OPENROUTER_API_KEY" || len(got) != 4 {
		t.Fatalf("live secrets = %v", got)
	}
	r.flow.Cfg.Process = demorun.Env{"GHOST_DEMO_LLM_MODE": "replay"}
	if got := requiredSecrets(Assemble(r.flow, r.options(slackOK)).Cfg); contains(got, "OPENROUTER_API_KEY") {
		t.Fatalf("replay secrets = %v", got)
	}
	r.flow.Cfg.Process = demorun.Env{}
	r.flow.Cfg.DotEnv = demorun.Env{}
	rt := Assemble(r.flow, r.options(slackOK))
	if got := strings.Join(missingOf(rt), ","); got != "OPENROUTER_API_KEY,SLACK_CHANNEL_ID" {
		t.Fatalf("missing = %s", got)
	}
}

func TestAssembleControlPortCanBeOverriddenButNotToNonsense(t *testing.T) {
	r := newAssembleRig(t)
	r.flow.Cfg.Process = demorun.Env{"GHOST_DEMO_PORT_CONTROL": "18099"}
	if rt := Assemble(r.flow, r.options(slackOK)); rt.Port != 18099 || rt.Cfg.WebExtraEnv["GHOST_DEMO_CONTROL_URL"] != "http://127.0.0.1:18099" {
		t.Fatalf("port = %d", rt.Port)
	}
	for _, bad := range []string{"0", "70000", "abc", ""} {
		r.flow.Cfg.Process = demorun.Env{"GHOST_DEMO_PORT_CONTROL": bad}
		if rt := Assemble(r.flow, r.options(slackOK)); rt.Port != DefaultControlPort {
			t.Errorf("%q gave port %d", bad, rt.Port)
		}
	}
}

func TestAssemblePrepareBuildsTheRunnerToolingThenTheWebBuild(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackMissing))
	tools, err := rt.Boot.Prepare(context.Background())
	if err != nil || tools.Host != "/bin/host" {
		t.Fatalf("Prepare = %+v, %v", tools, err)
	}
	if len(r.prepared) != 1 || !r.prepared[0] || len(r.built) != 1 || r.built[0] != "/bin/npm" {
		t.Fatalf("prepared=%v built=%v: the bot is not built without tokens, the web build gets npm", r.prepared, r.built)
	}
	failing := Assemble(r.flow, Options{ResolveSlack: slackOK, BuildWeb: func(context.Context, string) error { return errors.New("next build failed") }})
	if _, err := failing.Boot.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "next build failed") {
		t.Fatalf("err = %v", err)
	}
	r.flow.Prepare = func(context.Context, bool, bool) (demorun.Tooling, error) {
		return demorun.Tooling{}, errors.New("go build failed")
	}
	if _, err := Assemble(r.flow, r.options(slackOK)).Boot.Prepare(context.Background()); err == nil {
		t.Fatal("a tooling failure must surface")
	}
	noWeb := Assemble(newAssembleRig(t).flow, Options{ResolveSlack: slackOK})
	if _, err := noWeb.Boot.Prepare(context.Background()); err != nil {
		t.Fatalf("no web builder configured: %v", err)
	}
}

func TestAssembleControlSpecRunsTheHostCopyAndIsHealthCheckedOverHTTP(t *testing.T) {
	rt := Assemble(newAssembleRig(t).flow, Options{ResolveSlack: slackOK})
	spec := rt.Boot.ControlSpec(demorun.Tooling{Host: "/bin/host"})
	if spec.Name != "control" || spec.Path != "/bin/host" || strings.Join(spec.Args, " ") != "codespace serve" || spec.Port != 8099 {
		t.Fatalf("spec = %+v", spec)
	}
	srv := httptest.NewServer(rt.Control.Handler())
	defer srv.Close()
	rt.Port = 0
	spec.Health = demorun.HTTPCheck(srv.URL+"/healthz", nil)
	if err := spec.Health(context.Background()); err != nil {
		t.Fatalf("the control health check must pass against /healthz: %v", err)
	}
}

func TestAssembleStatusAsksCoreForTheInvisibilityWithItsToken(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	status, err := rt.Status.Invisibility(context.Background(), "man-1")
	if err != nil || status != "withheld" {
		t.Fatalf("invisibility = %q, %v", status, err)
	}
	r.core.Close()
	if _, err := rt.Status.Invisibility(context.Background(), "man-1"); err == nil {
		t.Fatal("an unreachable core must be an error, not a status")
	}
}

func TestAssembleServerBindsLoopbackOnly(t *testing.T) {
	rt := Assemble(newAssembleRig(t).flow, Options{ResolveSlack: slackOK})
	srv := rt.Server()
	if !strings.HasPrefix(srv.Addr, "127.0.0.1:") || srv.Handler == nil || srv.ReadHeaderTimeout == 0 {
		t.Fatalf("server = %+v", srv)
	}
}

func TestWebBuildNeeded(t *testing.T) {
	for _, tc := range []struct {
		name       string
		exists     bool
		stamp, now string
		want       bool
	}{
		{"no build", false, "", "abc", true},
		{"stale build", true, "old", "new", true},
		{"current build", true, "abc", "abc", false},
		{"unknown sources trust the build", true, "abc", "", false},
		{"build without a stamp", true, "", "abc", true},
	} {
		if got := WebBuildNeeded(tc.exists, tc.stamp, tc.now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := WebBuildStampFile("/r"); !strings.HasSuffix(strings.ReplaceAll(got, "\\", "/"), "/r/web/.next/.ghost-build-stamp") {
		t.Errorf("stamp file = %s", got)
	}
}
