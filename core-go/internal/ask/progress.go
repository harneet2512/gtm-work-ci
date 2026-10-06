package ask

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Progress states (contracts/openapi/core.yaml AskProgress).
const (
	ProgressWorking = "working"
	ProgressDone    = "done"
	ProgressUnknown = "unknown"

	maxProgressLines   = 40
	maxProgressEntries = 200
	progressTTL        = 10 * time.Minute
	maxLineChars       = 200
)

var turnIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// Progress is the answer of GET /ask/turns/{turn_id}/progress.
type Progress struct {
	State string   `json:"state"`
	Lines []string `json:"lines"`
}

type progressEntry struct {
	lines []string
	done  bool
	at    time.Time
}

// progressBook keeps the step lines of the questions being answered, so an adapter can show them while it waits.
// In memory only: a line is worth something for seconds.
type progressBook struct {
	mu      sync.Mutex
	entries map[string]*progressEntry
	now     func() time.Time
}

func newProgressBook(now func() time.Time) *progressBook {
	return &progressBook{entries: map[string]*progressEntry{}, now: now}
}

// start opens the entry of a turn, or reopens it when work on it continues (the lines so far stay).
func (b *progressBook) start(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.entries[id]; e != nil {
		e.done, e.at = false, b.now()
		return
	}
	b.prune()
	b.entries[id] = &progressEntry{at: b.now()}
}

func (b *progressBook) add(id, line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.entries[id]; e != nil && len(e.lines) < maxProgressLines {
		e.lines = append(e.lines, clipRunes(line, maxLineChars))
		e.at = b.now()
	}
}

func (b *progressBook) finish(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.entries[id]; e != nil {
		e.done, e.at = true, b.now()
	}
}

func (b *progressBook) get(id string) Progress {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.entries[id]
	if e == nil || b.now().Sub(e.at) > progressTTL {
		return Progress{State: ProgressUnknown, Lines: []string{}}
	}
	state := ProgressWorking
	if e.done {
		state = ProgressDone
	}
	return Progress{State: state, Lines: append([]string{}, e.lines...)}
}

// prune forgets old entries and, past the cap, the oldest ones. The caller holds the lock.
func (b *progressBook) prune() {
	now := b.now()
	for id, e := range b.entries {
		if now.Sub(e.at) > progressTTL {
			delete(b.entries, id)
		}
	}
	for len(b.entries) >= maxProgressEntries {
		oldest, at := "", now
		for id, e := range b.entries {
			if oldest == "" || e.at.Before(at) {
				oldest, at = id, e.at
			}
		}
		delete(b.entries, oldest)
	}
}

// Progress returns the step lines of a turn.
func (s *Service) Progress(turnID string) (Progress, error) {
	if !turnIDRe.MatchString(turnID) {
		return Progress{}, fmt.Errorf("%w: turn_id must be 8 to 64 letters, digits, - or _", ErrBadRequest)
	}
	return s.progress.get(turnID), nil
}

// stepLine words one tool call for a person: what Cliff is looking at, never a tool name or an id.
func stepLine(tool string, args map[string]any) string {
	who := subject(str(args, "account"))
	when := ""
	if d := dateWords(str(args, "as_of")); d != "" {
		when = " as of " + d
	}
	switch tool {
	case "list_accounts":
		return "Listing the accounts…"
	case "account_state":
		return fmt.Sprintf("Reading %s state%s…", possessive(who), when)
	case "timeline":
		return fmt.Sprintf("Reading %s timeline…", possessive(who))
	case "graph_neighborhood":
		return fmt.Sprintf("Looking at %s relationships%s…", possessive(who), when)
	case "episode":
		return "Opening the decision episode…"
	case "gate_results":
		if g := clipRunes(str(args, "gate"), 8); g != "" {
			return "Checking the " + g + " evals…"
		}
		return "Checking the evals…"
	case "strategies":
		return "Reading the strategy options…"
	case "human_decision":
		return "Reading what the human chose…"
	case "judgment_inference":
		return "Reading how the judgment was interpreted…"
	case "knowledge":
		return "Reading the learned knowledge…"
	case "knowledge_attribution":
		return "Checking which knowledge was used…"
	case "search_activities":
		return fmt.Sprintf("Searching the activities for \"%s\"…", clipRunes(oneLine(str(args, "query")), 40))
	case "draft_followup":
		return "Preparing a draft for " + who + " (nothing is sent)…"
	case "crm_update_preview":
		return "Previewing a CRM change for " + who + " (nothing is written)…"
	}
	return "Looking something up…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// subject names an account for a person: the name as given, "the account" for an id or nothing.
func subject(name string) string {
	name = clipRunes(oneLine(name), 40)
	if name == "" || uuidRe.MatchString(strings.ToLower(name)) {
		return "the account"
	}
	return name
}

func possessive(who string) string {
	if who == "the account" {
		return "the account's"
	}
	if strings.HasSuffix(who, "s") {
		return who + "'"
	}
	return who + "'s"
}

// dateWords turns a date or an RFC3339 time into "Nov 9"; "" when it is neither.
func dateWords(raw string) string {
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return t.Format("Jan 2")
		}
	}
	return ""
}
