package demorun

import (
	"path/filepath"
	"strings"
	"testing"
)

// One Neo4j per case: the demo's second Play moves core to the next case's graph, which was built when the demo was reset,
// instead of rebuilding a whole graph while the audience waits (a rebuild of the demo data takes about 45 seconds).
func TestEachExtraGraphInstanceGetsItsOwnServiceDirectoryAndPort(t *testing.T) {
	c := testConfig()
	c.GraphInstances = 2
	specs := c.Specs(testTooling())
	if got := specNames(specs); got != "neo4j,neo4j-case2,postgres,worker,core,slackbot,web" {
		t.Fatalf("order = %s", got)
	}
	first, second := c.GraphSlot(0), c.GraphSlot(1)
	if first.Service != "neo4j" || first.Dir != c.Layout.Neo4jDir() || first.Port != c.Ports.Bolt {
		t.Fatalf("slot 0 keeps the original instance: %+v", first)
	}
	if second.Service != "neo4j-case2" || second.Dir != filepath.Join(c.Layout.Dir(), "neo4j-case2") || second.Port != c.Ports.Bolt+1 {
		t.Fatalf("slot 1 = %+v", second)
	}
	for _, s := range specs {
		if s.Name != "neo4j-case2" {
			continue
		}
		if s.Args[1] != "neo4j" || s.Env["GHOST_DEMO_HOST_DIR"] != second.Dir || s.Env["GHOST_DEMO_HOST_PORT"] != "17688" ||
			!strings.HasSuffix(s.Env["GHOST_DEMO_HOST_STOPFILE"], "neo4j-case2.stop") || s.Env["NEO4J_PASSWORD"] != "neo-pass-1234" || s.Port != 17688 {
			t.Fatalf("the second instance is hosted by the same command with its own dir, port and stop file: %+v %v", s.Args, s.Env.Names())
		}
	}
	if len(c.GraphDirs()) != 2 {
		t.Fatalf("every instance's directory is known (the Stop sweep needs them): %v", c.GraphDirs())
	}
}

func TestOneGraphInstanceIsTheDefaultAndTheUnchangedPlan(t *testing.T) {
	c := testConfig()
	if got := specNames(c.Specs(testTooling())); got != "neo4j,postgres,worker,core,slackbot,web" {
		t.Fatalf("order = %s", got)
	}
	if len(c.GraphDirs()) != 1 || c.GraphSlot(0).Port != c.Ports.Bolt || c.GraphSlot(0).Service != SvcNeo4j {
		t.Fatalf("slot 0 = %+v", c.GraphSlot(0))
	}
}

func TestACaseConfigPointsCoreAndTheToolsAtItsOwnGraph(t *testing.T) {
	c := testConfig()
	c.GraphInstances = 2
	second := c.ForGraph(1)
	if second.Neo4jURI() != "bolt://127.0.0.1:17688" || second.CoreEnv()["NEO4J_URI"] != "bolt://127.0.0.1:17688" || second.ToolEnv()["NEO4J_URI"] != "bolt://127.0.0.1:17688" {
		t.Fatalf("uri = %s", second.Neo4jURI())
	}
	if c.Neo4jURI() != "bolt://127.0.0.1:17687" {
		t.Fatal("the base config is unchanged")
	}
}
