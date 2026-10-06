package demorun

import (
	"context"
	"strings"
	"testing"
)

func TestSeedPassesTheReplayWorkerToTheFreezeOnlyWhenSet(t *testing.T) {
	r := newRig(t)
	r.flow.FreezeWorkerURL = "http://127.0.0.1:8090"
	if err := r.flow.Seed(context.Background(), r.seedOpts()); err != nil {
		t.Fatal(err)
	}
	if args := strings.Join(r.tools[0], " "); !strings.Contains(args, "--worker-url http://127.0.0.1:8090") {
		t.Fatalf("the freeze must use the replay worker the report was mined with: %s", args)
	}
	plain := newRig(t)
	if err := plain.flow.Seed(context.Background(), plain.seedOpts()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(plain.tools[0], " "), "--worker-url") {
		t.Fatal("no worker URL, no flag")
	}
}
