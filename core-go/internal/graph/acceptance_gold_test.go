package graph_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/clock"
	"github.com/harneet2512/gtm-work/core-go/internal/graph"
	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
)

const (
	fixturesDir = "../../../fixtures"
	// minScore is the HAR-103 acceptance bar for identity clustering and edge precision/recall.
	minScore = 0.95
)

// scoredRels are the relationship types WP5 owns and gold lists completely, scored against gold.
func scoredRels() map[string]bool {
	return map[string]bool{"belongs_to": true, "owns": true, "supports": true, "works_at": true, "shared_with": true, "economic_buyer_for": true}
}

// crmRoleRels is the test's own copy of the CRM Role__c -> edge table (independent of the code under test).
func crmRoleRels() map[string]string {
	return map[string]string{
		"Economic buyer": "economic_buyer_for", "Champion": "champion_for",
		"Security approver": "influences", "Technical approver": "influences", "Evaluation owner": "influences",
	}
}

type goldEntity struct {
	Key              string   `json:"key"`
	Kind             string   `json:"kind"`
	SourceIdentities []string `json:"source_identities"`
}

type goldEdge struct {
	Src     string `json:"src"`
	RelType string `json:"rel_type"`
	Dst     string `json:"dst"`
}

type goldFile struct {
	Expected struct {
		Entities []goldEntity `json:"entities"`
		Edges    []goldEdge   `json:"edges"`
	} `json:"expected"`
}

type gold struct {
	identities map[string][]string // gold key -> source identities (union over accounts)
	kinds      map[string]string
	edges      []goldEdge
	keyOfID    map[string]string // source identity -> gold key
}

func fixtureEvents(t *testing.T) []normalize.SourceEvent {
	t.Helper()
	var out []normalize.SourceEvent
	for _, dir := range []string{filepath.Join(fixturesDir, "world", "accounts"), filepath.Join(fixturesDir, "live")} {
		var paths []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
				paths = append(paths, p)
			}
			return err
		})
		if err != nil {
			if os.Getenv("CI") == "true" {
				t.Fatalf("fixtures missing: %v", err)
			}
			t.Skipf("fixtures not present: %v", err)
		}
		sort.Strings(paths)
		for _, p := range paths {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			evs, err := ingest.DecodeEvents(raw)
			if err != nil {
				t.Fatalf("%s: %v", p, err)
			}
			out = append(out, evs...)
		}
	}
	return out
}

func loadGold(t *testing.T) gold {
	t.Helper()
	g := gold{identities: map[string][]string{}, kinds: map[string]string{}, keyOfID: map[string]string{}}
	for _, acct := range []string{"acme", "beta", "northstar"} {
		raw, err := os.ReadFile(filepath.Join(fixturesDir, "gold", acct, "cp4.json"))
		if err != nil {
			t.Fatal(err)
		}
		var f goldFile
		if err := json.Unmarshal(raw, &f); err != nil {
			t.Fatal(err)
		}
		for _, e := range f.Expected.Entities {
			g.kinds[e.Key] = e.Kind
			for _, id := range e.SourceIdentities {
				if _, seen := g.keyOfID[id]; !seen {
					g.keyOfID[id] = e.Key
					g.identities[e.Key] = append(g.identities[e.Key], id)
				}
			}
		}
		g.edges = append(g.edges, f.Expected.Edges...)
	}
	return g
}

// entityOf resolves a source identity ("email:a@b.com", "crm:contact:817", "docs:gdrive:x") to
// the "<type>:<id>" of the entity the database maps it to; "" if it is not mapped.
func entityOf(t *testing.T, identity string) string {
	t.Helper()
	system, rest, _ := strings.Cut(identity, ":")
	if system == "docs" {
		return "document:" + graph.DocumentID(rest)
	}
	m, ok, err := graph.CurrentMapping(ctx, env.DB, graph.SourceKey{System: system, Key: rest})
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return string(m.EntityType) + ":" + m.EntityID
}

type score struct{ tp, predicted, gold int }

// String renders the scores; with nothing expected and nothing predicted they are not defined.
func (s score) String() string {
	if s.gold == 0 && s.predicted == 0 {
		return "precision n/a, recall n/a (nothing expected, nothing predicted)"
	}
	return fmt.Sprintf("precision %.4f, recall %.4f", s.precision(), s.recall())
}

func (s score) precision() float64 { return ratio(s.tp, s.predicted) }
func (s score) recall() float64    { return ratio(s.tp, s.gold) }

func ratio(a, b int) float64 {
	if b == 0 {
		return 1
	}
	return float64(a) / float64(b)
}

func scoreSets[K comparable](want, got map[K]bool) score {
	s := score{gold: len(want), predicted: len(got)}
	for k := range got {
		if want[k] {
			s.tp++
		}
	}
	return s
}

func ingestWorld(t *testing.T) {
	t.Helper()
	events := fixtureEvents(t)
	resetDB(t)
	company, err := graph.LoadCompany(filepath.Join(fixturesDir, "world", "org.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.SeedCompany(ctx, env.DB, company, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	svc, err := ingest.NewService(env.DB, ingest.Options{Extension: graph.NewExtension(), Clock: clock.NewFixed(t0),
		Debounce: time.Second, MaxWait: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ingestAll(t, svc, events...)
	if n := num(t, `SELECT count(*) FROM unresolved_activities`); n != 0 {
		t.Fatalf("%d fixture activities stayed unresolved", n)
	}
}

// world holds the ingested database seen through gold: which gold entity each database entity is.
type world struct {
	g         gold
	cluster   map[string]string // "<type>:<uuid>" -> gold key
	extras    []string          // current mappings that gold does not list, merged into a gold entity
	unmapped  []string
	collapsed []string // gold keys that share one database entity
}

func buildWorld(t *testing.T) world {
	t.Helper()
	w := world{g: loadGold(t), cluster: map[string]string{}}
	owner := map[string]string{} // db entity -> first gold key seen
	ids := make([]string, 0, len(w.g.keyOfID))
	for id := range w.g.keyOfID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := entityOf(t, id)
		if e == "" {
			w.unmapped = append(w.unmapped, id)
			continue
		}
		key := w.g.keyOfID[id]
		if first, ok := owner[e]; ok && first != key {
			w.collapsed = append(w.collapsed, first+"+"+key)
		}
		if _, ok := owner[e]; !ok {
			owner[e] = key
		}
		w.cluster[e] = owner[e]
	}
	w.extras = extraIdentities(t, w)
	return w
}

// extraIdentities lists current mappings (identities gold does not list) that sit in a gold
// entity's cluster. Slack deal channels (#deal-<slug>) are conversation aliases of an account,
// not source identities in the gold schema (it only lists slack:<user id>); they are checked
// separately and excluded here.
func extraIdentities(t *testing.T, w world) []string {
	t.Helper()
	rows, err := env.DB.Query(`SELECT entity_type, entity_id::text, source_system, source_key FROM entity_source_mappings WHERE valid_to IS NULL ORDER BY 3, 4`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, id, system, key string
		if err := rows.Scan(&typ, &id, &system, &key); err != nil {
			t.Fatal(err)
		}
		identity := system + ":" + key
		if _, listed := w.g.keyOfID[identity]; listed || (system == "slack" && strings.HasPrefix(key, "#")) {
			continue
		}
		if _, inGold := w.cluster[typ+":"+id]; inGold {
			out = append(out, identity)
		}
	}
	return out
}

func (w world) keyOf(typ, id string) string {
	if k, ok := w.cluster[typ+":"+id]; ok {
		return k
	}
	return "unknown:" + typ + ":" + id
}

// Acceptance (HAR-103): ingest the world and live fixtures into a fresh database with the org
// seeded, then compare identity clustering and the edges with the gold checkpoints.
func TestWorldIdentityClusteringMatchesGold(t *testing.T) {
	ingestWorld(t)
	w := buildWorld(t)

	// Every gold identity resolves, and no two gold entities collapse into one.
	if len(w.unmapped) > 0 {
		t.Errorf("gold identities with no mapping: %v", w.unmapped)
	}
	if len(w.collapsed) > 0 {
		t.Errorf("distinct gold entities merged into one database entity: %v", w.collapsed)
	}
	single, singleOK := 0, 0
	for key, ids := range w.g.identities {
		if len(ids) != 1 {
			continue
		}
		single++
		if entityOf(t, ids[0]) != "" {
			singleOK++
		} else {
			t.Errorf("single-identity entity %s (%s) was not resolved", key, ids[0])
		}
	}
	t.Logf("single-identity entities (opportunities, documents): %d/%d resolved", singleOK, single)

	// Pairwise clustering over every gold identity plus every non-gold identity merged into a
	// gold entity: a pair counts as predicted when both sit in one database entity, and as
	// wrong unless both are the same gold entity.
	type ident struct{ id, gold, pred string }
	var all []ident
	for id, key := range w.g.keyOfID {
		all = append(all, ident{id, key, entityOf(t, id)})
	}
	for _, id := range w.extras {
		all = append(all, ident{id, "", entityOf(t, id)})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].id < all[j].id })
	var cluster score
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			a, b := all[i], all[j]
			if a.gold == "" && b.gold == "" {
				continue // no ground truth for two non-gold identities
			}
			same, pred := a.gold == b.gold && a.gold != "", a.pred != "" && a.pred == b.pred
			if same {
				cluster.gold++
			}
			if pred {
				cluster.predicted++
			}
			if same && pred {
				cluster.tp++
			}
		}
	}
	t.Logf("identity clustering: %d gold identities + %d non-gold identities merged into gold entities %v; gold pairs %d, predicted pairs %d, precision %.4f, recall %.4f",
		len(w.g.keyOfID), len(w.extras), w.extras, cluster.gold, cluster.predicted, cluster.precision(), cluster.recall())
	if cluster.precision() < minScore || cluster.recall() < minScore {
		t.Errorf("identity clustering precision %.4f / recall %.4f below %.2f", cluster.precision(), cluster.recall(), minScore)
	}

	// Slack deal channels resolve to the account whose name starts with the channel slug.
	rows, err := env.DB.Query(`SELECT m.source_key, a.name FROM entity_source_mappings m JOIN accounts a ON a.id = m.entity_id
		WHERE m.source_system = 'slack' AND m.source_key LIKE '#deal-%' AND m.valid_to IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	channels := 0
	for rows.Next() {
		var channel, name string
		if err := rows.Scan(&channel, &name); err != nil {
			t.Fatal(err)
		}
		channels++
		if !strings.HasPrefix(strings.ToLower(name), strings.TrimPrefix(channel, "#deal-")) {
			t.Errorf("channel %s maps to account %q", channel, name)
		}
	}
	if channels != 3 {
		t.Errorf("%d deal channels mapped, want 3", channels)
	}
}
