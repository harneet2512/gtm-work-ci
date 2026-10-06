// Package signalbench measures the WP8 (HAR-106) rules against the gold checkpoints of fixtures/gold:
// signal precision and recall, material-diff-field precision and recall, and trigger decision accuracy.
// It replays each account's checkpoints in order: the previous checkpoint's gold state is the previous
// AccountState, this checkpoint's gold state the next one, and the events between the two are the
// activities folded in. Upstream extraction is therefore an oracle (the gold state), so the numbers
// isolate the diff, signal and trigger rules; end-to-end extraction quality is measured by WP6/WP11.
package signalbench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/harneet2512/gtm-work/core-go/internal/ingest"
	"github.com/harneet2512/gtm-work/core-go/internal/normalize"
	"github.com/harneet2512/gtm-work/core-go/internal/reducer"
	"github.com/harneet2512/gtm-work/core-go/internal/signals"
)

// OwnDomain is the seed world's own email domain.
const OwnDomain = "ghostvendor.com"

// Checkpoint is one gold checkpoint with the activities folded in since the previous one.
type Checkpoint struct {
	Account    string
	Number     int
	AsOf       time.Time
	State      reducer.AccountState
	Activities []signals.ActivityFact
	// Expected is the gold: distinct signal types, material diff fields and the trigger decision.
	Expected Expected
}

// Expected is the gold of one checkpoint.
type Expected struct {
	Signals            []string `json:"signals"`
	MaterialDiffFields []string `json:"material_diff_fields"`
	Trigger            struct {
		Eligible    bool     `json:"eligible"`
		ReasonCodes []string `json:"reason_codes"`
	} `json:"trigger"`
}

type goldFile struct {
	Account        string    `json:"account"`
	Checkpoint     int       `json:"checkpoint"`
	AfterEventFile string    `json:"after_event_file"`
	LiveFiles      []string  `json:"live_files"`
	AsOf           time.Time `json:"as_of"`
	Expected       struct {
		Expected
		StateFields  map[string]json.RawMessage `json:"state_fields"`
		BuyingGroup  []goldMember               `json:"buying_group"`
		CoverageGaps []string                   `json:"coverage_gaps"`
	} `json:"expected"`
}

type goldMember struct {
	PersonKey string   `json:"person_key"`
	Roles     []string `json:"roles"`
	Status    string   `json:"status"`
}

// Load reads every account's checkpoints in order (account, then checkpoint number) from root, the repository.
func Load(root string) ([]Checkpoint, error) {
	files, err := filepath.Glob(filepath.Join(root, "fixtures", "gold", "*", "cp*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("signalbench: no gold checkpoints under %s (%v)", root, err)
	}
	byAccount := map[string][]goldFile{}
	for _, f := range files {
		var g goldFile
		if err := readJSON(f, &g); err != nil {
			return nil, err
		}
		byAccount[g.Account] = append(byAccount[g.Account], g)
	}
	accounts := make([]string, 0, len(byAccount))
	for a := range byAccount {
		accounts = append(accounts, a)
	}
	sort.Strings(accounts)
	var out []Checkpoint
	for _, a := range accounts {
		cps, err := loadAccount(root, a, byAccount[a])
		if err != nil {
			return nil, err
		}
		out = append(out, cps...)
	}
	return out, nil
}

func loadAccount(root, account string, golds []goldFile) ([]Checkpoint, error) {
	sort.Slice(golds, func(i, j int) bool { return golds[i].Checkpoint < golds[j].Checkpoint })
	order, err := ingestOrder(root, account, golds)
	if err != nil {
		return nil, err
	}
	var out []Checkpoint
	prevEnd := -1
	for _, g := range golds {
		end := indexOf(order, g.AfterEventFile)
		if end < 0 {
			return nil, fmt.Errorf("signalbench: %s cp%d: %s is not among the account's events", account, g.Checkpoint, g.AfterEventFile)
		}
		acts, err := activitiesBetween(root, order[prevEnd+1:end+1])
		if err != nil {
			return nil, err
		}
		st, err := stateOf(g)
		if err != nil {
			return nil, fmt.Errorf("signalbench: %s cp%d: %w", account, g.Checkpoint, err)
		}
		out = append(out, Checkpoint{Account: account, Number: g.Checkpoint, AsOf: g.AsOf, State: st, Activities: acts, Expected: g.Expected.Expected})
		prevEnd = end
	}
	return out, nil
}

// ingestOrder is the world files in file-name order, then the live files in the order the gold declares.
func ingestOrder(root, account string, golds []goldFile) ([]string, error) {
	world, err := filepath.Glob(filepath.Join(root, "fixtures", "world", "accounts", account, "events", "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(world)
	var order []string
	for _, w := range world {
		rel, _ := filepath.Rel(filepath.Join(root, "fixtures"), w)
		order = append(order, filepath.ToSlash(rel))
	}
	for _, g := range golds {
		for _, l := range g.LiveFiles {
			if indexOf(order, l) < 0 {
				order = append(order, l)
			}
		}
	}
	return order, nil
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// activitiesBetween normalizes the event files into activity facts, dropping duplicate deliveries.
func activitiesBetween(root string, rels []string) ([]signals.ActivityFact, error) {
	var out []signals.ActivityFact
	seen := map[string]bool{}
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(root, "fixtures", filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("signalbench: %w", err)
		}
		events, err := ingest.DecodeEvents(raw)
		if err != nil {
			return nil, fmt.Errorf("signalbench: %s: %w", rel, err)
		}
		for i, ev := range events {
			act, err := normalize.Normalize(ev)
			if err != nil {
				return nil, fmt.Errorf("signalbench: %s: %w", rel, err)
			}
			if seen[act.IdempotencyKey()] {
				continue
			}
			seen[act.IdempotencyKey()] = true
			out = append(out, factOf(fmt.Sprintf("%s#%d", rel, i), act))
		}
	}
	return out, nil
}

func factOf(id string, act normalize.Activity) signals.ActivityFact {
	f := signals.ActivityFact{ID: id, Type: act.Type(), OccurredAt: act.OccurredAt().UTC()}
	for _, p := range act.Participants() {
		if p.Role != "from" {
			continue
		}
		if signals.IsCustomerIdentity(p.RawIdentity, OwnDomain) {
			f.FromCustomer = true
		} else if strings.Contains(p.RawIdentity, "@") {
			f.FromRep = true
		}
	}
	return f
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("signalbench: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("signalbench: %s: %w", path, err)
	}
	return nil
}
