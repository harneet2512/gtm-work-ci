package codespace

import (
	"strings"
	"testing"
)

func TestAssembleDefaultsTheModelOnlyWhenNoneIsConfiguredAndListsEveryServiceToStop(t *testing.T) {
	r := newAssembleRig(t)
	rt := Assemble(r.flow, r.options(slackOK))
	if got := rt.Cfg.Merged("GHOST_MODEL"); got != "openrouter/qwen/qwen3.8-flash" {
		t.Fatalf("default model = %q", got)
	}
	r.flow.Cfg.DotEnv["GHOST_MODEL"] = "openrouter/other/model"
	if got := Assemble(r.flow, r.options(slackOK)).Cfg.Merged("GHOST_MODEL"); got != "openrouter/other/model" {
		t.Fatalf(".env must win: %q", got)
	}
	var names []string
	for _, s := range rt.StopSpecs() {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "neo4j,neo4j-case2,postgres,worker,core,slackbot,web,control" {
		t.Fatalf("stop specs = %s", got)
	}
}
