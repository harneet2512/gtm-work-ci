package codespace

import (
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

var hexToken = regexp.MustCompile(`^[0-9a-f]{64}$`)

// The handoff endpoint releases the next episode, so only the web server this boot started may call it: it listens on
// loopback only, and it wants a token made for this boot, not the long-lived core API token.
func TestTheControlTokenIsMadeForEachBootAndIsNotTheCoreAPIToken(t *testing.T) {
	r := newAssembleRig(t)
	a, b := Assemble(r.flow, r.options(slackOK)), Assemble(r.flow, r.options(slackOK))
	if !hexToken.MatchString(a.ControlToken) || !hexToken.MatchString(b.ControlToken) {
		t.Fatalf("tokens must be 32 random bytes in hex: %q %q", a.ControlToken, b.ControlToken)
	}
	if a.ControlToken == b.ControlToken {
		t.Fatal("every boot makes a new token")
	}
	if a.ControlToken == "t0k" || a.Control.Token != a.ControlToken {
		t.Fatalf("the control service uses its own token, not the core API token: %q", a.Control.Token)
	}
	if a.Cfg.WebExtraEnv[ControlTokenEnv] != a.ControlToken {
		t.Fatal("the web server (and nothing else) is told the token")
	}
	spec := a.controlSpec(demorun.Tooling{Host: "host"})
	if spec.Env[ControlTokenEnv] != a.ControlToken {
		t.Fatal("the control process is started with the same token")
	}
}

func TestTheControlProcessKeepsTheTokenItWasStartedWith(t *testing.T) {
	r := newAssembleRig(t)
	given := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	r.flow.Cfg.Process = demorun.Merge(r.flow.Cfg.Process, demorun.Env{ControlTokenEnv: given})
	rt := Assemble(r.flow, r.options(slackOK))
	if rt.ControlToken != given {
		t.Fatalf("the serve process must use the token its boot gave it: %q", rt.ControlToken)
	}
	r.flow.Cfg.Process = demorun.Merge(r.flow.Cfg.Process, demorun.Env{ControlTokenEnv: "short"})
	if weak := Assemble(r.flow, r.options(slackOK)); weak.ControlToken == "short" {
		t.Fatal("a weak token is never accepted")
	}
}

func TestTheControlServiceRefusesTheCoreAPITokenAndAnOldBootsToken(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	srv := httptest.NewServer(rt.Control.Handler())
	defer srv.Close()
	for name, token := range map[string]string{"core API token": "t0k", "old boot": "0123456789abcdef", "none": ""} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/handoff", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/status", nil)
	req.Header.Set("Authorization", "Bearer "+rt.ControlToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("this boot's token must be accepted: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

func TestTheControlServiceListensOnLoopbackOnly(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	for _, addr := range []string{rt.ListenAddress(), rt.Server().Addr} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || host != "127.0.0.1" {
			t.Fatalf("%q must bind 127.0.0.1 explicitly (not all interfaces, not a LAN address): %v", addr, err)
		}
	}
	r.flow.Cfg.Process = demorun.Merge(r.flow.Cfg.Process, demorun.Env{"GHOST_DEMO_PORT_CONTROL": "9123"})
	other := Assemble(r.flow, r.options(slackOK))
	if other.ListenAddress() != "127.0.0.1:9123" {
		t.Fatalf("a configured port keeps the loopback host: %s", other.ListenAddress())
	}
}
