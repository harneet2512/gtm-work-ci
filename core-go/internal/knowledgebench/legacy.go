// Package knowledgebench measures the knowledge applicability matcher (HAR-97 L4 "Knowledge
// applicability": applicability precision, applicability recall, exception recall) over labelled
// (knowledge, situation) pairs. The only labels today are the legacy fixture gold of fixtures/evals:
// invented demo accounts that CRMArena-based gold will replace. The matcher is never tuned to them.
package knowledgebench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/knowledge"
	"github.com/harneet2512/gtm-work/core-go/internal/signalbench"
	"github.com/harneet2512/gtm-work/core-go/internal/signalhistory"
)

// Pair is one labelled (knowledge, situation) pair.
type Pair struct {
	CaseID    string
	Knowledge knowledge.Knowledge
	Situation knowledge.Situation
	Gold      string // APPLIES | DOES_NOT_APPLY | EXCEPTION_TRIGGERED
	Source    string // the eval type the label comes from
	Debatable bool
}

// legacyCase is the part of a fixtures/evals case the benchmark reads.
type legacyCase struct {
	ID      string                   `json:"id"`
	BasedOn struct{ Account string } `json:"based_on"`
	Context struct {
		Now           time.Time                  `json:"now"`
		State         legacyState                `json:"state"`
		RecentChanges struct{ Signals []string } `json:"recent_changes"`
		Offered       []knowledge.Knowledge      `json:"offered_knowledge"`
	} `json:"context"`
	Expected []struct {
		EvalType      string   `json:"eval_type"`
		Label         string   `json:"label"`
		KnowledgeRefs []string `json:"knowledge_refs"`
		Debatable     bool     `json:"debatable"`
	} `json:"expected"`
}

type legacyState struct {
	AccountID     string                     `json:"account_id"`
	OpportunityID string                     `json:"opportunity_id"`
	Fields        map[string]json.RawMessage `json:"fields"`
	BuyingGroup   []struct {
		PersonID string   `json:"person_id"`
		Roles    []string `json:"roles"`
		Status   string   `json:"status"`
	} `json:"buying_group"`
	CoverageGaps []string `json:"coverage_gaps"`
	Conflicts    []struct {
		Field string `json:"field"`
	} `json:"conflicts"`
}

// checkpointSignals are the signals a gold checkpoint lists, timed at its as_of.
type checkpointSignals struct {
	AsOf    time.Time
	Signals []string
}

// Signal modes: where a legacy situation's signals come from.
const (
	// ModeCorrectUpstream adds the account's gold-checkpoint signals (timed at each checkpoint's as_of) to
	// the case's own diff: the matcher is given correct upstream signals. It measures the matcher alone.
	ModeCorrectUpstream = "matcher_given_correct_upstream_signals"
	// ModeOwnDiff keeps only the signals the case's own diff lists, timed at now: what a matcher sees when
	// no signal history is available. It measures the matcher plus missing signal history.
	ModeOwnDiff = "own_diff_signals_only"
	// ModeWP8InMemoryHistory is NOT end-to-end: the WP8 diff and signal rules are run over the GOLD STATE of
	// each checkpoint (an oracle for extraction), the signals they emit are kept in an in-memory history with
	// occurred_at and a window (no Postgres, no recompute), and the matcher is given the signals open as of the
	// case's time (signalhistory.OpenAsOf, the function the Postgres query also uses) plus the case's own
	// diff. No gold signal label is read: the signals come from the rules. It shows what persisted history
	// buys the matcher; the Postgres path is covered by the pipeline tests.
	ModeWP8InMemoryHistory = "wp8_rules_on_gold_state_in_memory_history"
)

// Modes lists the signal modes in report order.
var Modes = []string{ModeCorrectUpstream, ModeOwnDiff, ModeWP8InMemoryHistory}

// persistedHistory replays the WP8 rules over the gold checkpoints and returns the signal history they
// would have persisted, per account.
func persistedHistory(root string) (map[string][]signalhistory.Record, error) {
	cps, err := signalbench.Load(root)
	if err != nil {
		return nil, err
	}
	return signalbench.HistoryRecords(signalbench.Replay(cps)), nil
}

// LoadLegacyPairs reads every labelled pair from fixtures/evals/cases under root (the repository).
func LoadLegacyPairs(root, mode string) ([]Pair, error) {
	if mode != ModeCorrectUpstream && mode != ModeOwnDiff && mode != ModeWP8InMemoryHistory {
		return nil, fmt.Errorf("knowledgebench: unknown signal mode %q", mode)
	}
	history, err := loadGoldSignals(filepath.Join(root, "fixtures", "gold"))
	if err != nil {
		return nil, err
	}
	var persisted map[string][]signalhistory.Record
	if mode == ModeWP8InMemoryHistory {
		if persisted, err = persistedHistory(root); err != nil {
			return nil, err
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "fixtures", "evals", "cases", "*", "*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("knowledgebench: no eval cases under %s (%v)", root, err)
	}
	sort.Strings(files)
	var pairs []Pair
	for _, f := range files {
		var c legacyCase
		if err := readJSON(f, &c); err != nil {
			return nil, err
		}
		if len(c.Context.Offered) == 0 {
			continue
		}
		var prior []knowledge.Signal
		switch mode {
		case ModeCorrectUpstream:
			prior = goldSignals(c, history[c.BasedOn.Account])
		case ModeWP8InMemoryHistory:
			prior = signalhistory.OpenAsOf(persisted[c.BasedOn.Account], c.Context.State.OpportunityID, c.Context.Now)
		}
		s, err := situationOf(c, prior)
		if err != nil {
			return nil, fmt.Errorf("knowledgebench: case %s: %w", c.ID, err)
		}
		pairs = append(pairs, pairsOf(c, s)...)
	}
	return pairs, nil
}

// pairsOf labels each offered knowledge object: a knowledge_applicability judgment gives the label;
// an exception_awareness judgment that names the knowledge (EXCEPTION_HONORED or EXCEPTION_MISSED)
// implies EXCEPTION_TRIGGERED when no applicability judgment exists for it.
func pairsOf(c legacyCase, s knowledge.Situation) []Pair {
	byID := map[string]knowledge.Knowledge{}
	for _, k := range c.Context.Offered {
		byID[k.ID] = k
	}
	labelled := map[string]Pair{}
	for _, pass := range []string{"knowledge_applicability", "exception_awareness"} {
		for _, e := range c.Expected {
			if e.EvalType != pass {
				continue
			}
			for _, ref := range e.KnowledgeRefs { // a judgment that names no knowledge labels nothing
				k, ok := byID[ref]
				if _, done := labelled[ref]; !ok || done {
					continue
				}
				gold := e.Label
				if pass == "exception_awareness" {
					gold = knowledge.LabelExceptionTriggered
				}
				labelled[ref] = Pair{CaseID: c.ID, Knowledge: k, Situation: s, Gold: gold, Source: pass, Debatable: e.Debatable}
			}
		}
	}
	out := make([]Pair, 0, len(labelled))
	for _, k := range c.Context.Offered {
		if p, ok := labelled[k.ID]; ok {
			out = append(out, p)
		}
	}
	return out
}

func loadGoldSignals(dir string) (map[string][]checkpointSignals, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*", "cp*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("knowledgebench: no gold checkpoints under %s (%v)", dir, err)
	}
	out := map[string][]checkpointSignals{}
	for _, f := range files {
		var g struct {
			Account  string                     `json:"account"`
			AsOf     time.Time                  `json:"as_of"`
			Expected struct{ Signals []string } `json:"expected"`
		}
		if err := readJSON(f, &g); err != nil {
			return nil, err
		}
		out[g.Account] = append(out[g.Account], checkpointSignals{AsOf: g.AsOf, Signals: g.Expected.Signals})
	}
	return out, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("knowledgebench: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("knowledgebench: %s: %w", path, err)
	}
	return nil
}
