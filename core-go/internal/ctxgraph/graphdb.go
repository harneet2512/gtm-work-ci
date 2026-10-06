package ctxgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Config locates the Neo4j projection. It comes from the environment (.env.example); the password is
// never logged and never part of an error message.
type Config struct {
	URI      string
	User     string
	Password string
	Database string
}

// ConfigFromEnv reads NEO4J_URI, NEO4J_USER, NEO4J_PASSWORD and NEO4J_DATABASE. ok is false when
// NEO4J_URI is empty: the graph projection is then not configured (core runs without it).
func ConfigFromEnv() (cfg Config, ok bool, err error) {
	cfg = Config{URI: os.Getenv("NEO4J_URI"), User: os.Getenv("NEO4J_USER"), Password: os.Getenv("NEO4J_PASSWORD"), Database: os.Getenv("NEO4J_DATABASE")}
	if cfg.URI == "" {
		return Config{}, false, nil
	}
	if cfg.Database == "" {
		cfg.Database = "neo4j"
	}
	if cfg.User == "" && cfg.Password == "" { // no credentials at all: a throwaway server with auth disabled (CI)
		return cfg, true, nil
	}
	if cfg.User == "" {
		cfg.User = "neo4j"
	}
	if cfg.Password == "" {
		return Config{}, false, errors.New("ctxgraph: NEO4J_USER is set but NEO4J_PASSWORD is empty")
	}
	return cfg, true, nil
}

// Graph is a connection to the Neo4j projection. All Cypher it runs is parameterised; the only text
// spliced into a query are labels and relationship types, which are checked against the ontology first.
type Graph struct {
	drv neo4j.DriverWithContext
	db  string
}

// Open connects and verifies connectivity.
func Open(ctx context.Context, cfg Config) (*Graph, error) {
	auth := neo4j.BasicAuth(cfg.User, cfg.Password, "")
	if cfg.User == "" && cfg.Password == "" { // a throwaway server with auth disabled (CI)
		auth = neo4j.NoAuth()
	}
	drv, err := neo4j.NewDriverWithContext(cfg.URI, auth)
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: create neo4j driver: %w", err)
	}
	vctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := drv.VerifyConnectivity(vctx); err != nil {
		_ = drv.Close(ctx)
		return nil, fmt.Errorf("ctxgraph: neo4j at %s is not reachable: %w", cfg.URI, err)
	}
	return &Graph{drv: drv, db: cfg.Database}, nil
}

// Close releases the driver.
func (g *Graph) Close(ctx context.Context) error { return g.drv.Close(ctx) }

type record = map[string]any

// write runs one parameterised write query and returns its records.
func (g *Graph) write(ctx context.Context, cypher string, params map[string]any) ([]record, error) {
	res, err := neo4j.ExecuteQuery(ctx, g.drv, cypher, params, neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase(g.db), neo4j.ExecuteQueryWithWritersRouting())
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: neo4j write: %w", err)
	}
	return toRecords(res.Records), nil
}

// inTx runs fn in ONE write transaction: a projection's upserts and deletes commit together or not at all,
// so a reader never sees half of an account's change. The driver may retry fn on a transient error; every
// statement in it is idempotent.
func (g *Graph) inTx(ctx context.Context, fn func(run exec) error) error {
	sess := g.drv.NewSession(ctx, neo4j.SessionConfig{DatabaseName: g.db})
	defer sess.Close(ctx)
	_, err := sess.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		return nil, fn(func(ctx context.Context, cypher string, params map[string]any) ([]record, error) {
			res, err := tx.Run(ctx, cypher, params)
			if err != nil {
				return nil, err
			}
			recs, err := res.Collect(ctx)
			if err != nil {
				return nil, err
			}
			return toRecords(recs), nil
		})
	})
	if err != nil {
		return fmt.Errorf("ctxgraph: neo4j write transaction: %w", err)
	}
	return nil
}

// read runs one parameterised read query.
func (g *Graph) read(ctx context.Context, cypher string, params map[string]any) ([]record, error) {
	res, err := neo4j.ExecuteQuery(ctx, g.drv, cypher, params, neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase(g.db), neo4j.ExecuteQueryWithReadersRouting())
	if err != nil {
		return nil, fmt.Errorf("ctxgraph: neo4j read: %w", err)
	}
	return toRecords(res.Records), nil
}

func toRecords(in []*neo4j.Record) []record {
	out := make([]record, len(in))
	for i, r := range in {
		m := make(record, len(r.Keys))
		for j, k := range r.Keys {
			m[k] = r.Values[j]
		}
		out[i] = m
	}
	return out
}

// ident quotes a label or relationship type after checking it is in the ontology; Cypher cannot
// parameterise either, so this is the only place text is spliced into a query.
func ident(name string) (string, error) {
	_, isLabel := Labels[name]
	_, isRel := Relationships[name]
	if !isLabel && !isRel {
		return "", fmt.Errorf("ctxgraph: %q is not an ontology label or relationship type", name)
	}
	return "`" + strings.ReplaceAll(name, "`", "") + "`", nil
}

// mergeLabels are the labels nodes are unique on (a Conversation is an Activity).
func mergeLabels() []string {
	var out []string
	for _, l := range sortedLabels() {
		if l != LabelConversation {
			out = append(out, l)
		}
	}
	return out
}

// ensureSchema creates the uniqueness constraints and lookup indexes the projector relies on. It is
// idempotent (IF NOT EXISTS).
func (g *Graph) ensureSchema(ctx context.Context) error {
	var stmts []string
	for _, l := range mergeLabels() {
		q, err := ident(l)
		if err != nil {
			return err
		}
		lower := strings.ToLower(l)
		stmts = append(stmts,
			fmt.Sprintf("CREATE CONSTRAINT ghost_%s_id IF NOT EXISTS FOR (n:%s) REQUIRE n.id IS UNIQUE", lower, q),
			fmt.Sprintf("CREATE INDEX ghost_%s_account IF NOT EXISTS FOR (n:%s) ON (n.account_id)", lower, q))
	}
	for _, r := range sortedRelTypes() {
		q, err := ident(r)
		if err != nil {
			return err
		}
		lower := strings.ToLower(r)
		stmts = append(stmts,
			fmt.Sprintf("CREATE INDEX ghost_rel_%s_account IF NOT EXISTS FOR ()-[r:%s]-() ON (r.account_id)", lower, q),
			fmt.Sprintf("CREATE INDEX ghost_rel_%s_id IF NOT EXISTS FOR ()-[r:%s]-() ON (r.id)", lower, q))
	}
	for _, s := range stmts {
		if _, err := g.write(ctx, s, nil); err != nil {
			return err
		}
	}
	return nil
}

// wipe deletes every node and relationship (the schema stays). It is unexported: only the Projector's rebuild may use it;
// no handler or agent can reach a write path of the projection.
func (g *Graph) wipe(ctx context.Context) error {
	sess := g.drv.NewSession(ctx, neo4j.SessionConfig{DatabaseName: g.db})
	defer sess.Close(ctx)
	res, err := sess.Run(ctx, "MATCH (n) CALL { WITH n DETACH DELETE n } IN TRANSACTIONS OF 5000 ROWS", nil)
	if err != nil {
		return fmt.Errorf("ctxgraph: wipe: %w", err)
	}
	if _, err := res.Consume(ctx); err != nil {
		return fmt.Errorf("ctxgraph: wipe: %w", err)
	}
	return nil
}

// Counts returns the number of nodes and relationships in the projection.
func (g *Graph) Counts(ctx context.Context) (nodes, edges int64, err error) {
	n, err := g.read(ctx, "MATCH (n) RETURN count(n) AS c", nil)
	if err != nil {
		return 0, 0, err
	}
	e, err := g.read(ctx, "MATCH ()-[r]->() RETURN count(r) AS c", nil)
	if err != nil {
		return 0, 0, err
	}
	return n[0]["c"].(int64), e[0]["c"].(int64), nil
}

// outsideOntology lists the labels and relationship types Neo4j holds that the ontology does not define.
func (g *Graph) outsideOntology(ctx context.Context) ([]string, error) {
	out := []string{}
	labels, err := g.read(ctx, "MATCH (n) UNWIND labels(n) AS l RETURN DISTINCT l AS name", nil)
	if err != nil {
		return nil, err
	}
	for _, r := range labels {
		if name, _ := r["name"].(string); Labels[name].Table == "" {
			out = append(out, "label:"+name)
		}
	}
	types, err := g.read(ctx, "MATCH ()-[r]->() RETURN DISTINCT type(r) AS name", nil)
	if err != nil {
		return nil, err
	}
	for _, r := range types {
		if name, _ := r["name"].(string); Relationships[name].From == nil {
			out = append(out, "relationship:"+name)
		}
	}
	sort.Strings(out)
	return out, nil
}
