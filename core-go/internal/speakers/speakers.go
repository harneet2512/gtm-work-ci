// Package speakers resolves unlabelled call speakers (speaker_NN with no email and no name)
// to one of the meeting's attendees, deterministically and without an LLM.
//
// Evidence is textual: a speaker who introduces themselves ("Priya here", "I'm Tom", "Tom
// Becker, I manage ...") or who is addressed by name and answers next ("Priya, what does
// success look like?"), or who is thanked by name right after speaking. A possessive or a
// relation ("it's Priya's turn", "I'm Priya's manager") is not a cue. A label is mapped only
// when its cues point at exactly one attendee and no other label claims that attendee;
// anything ambiguous (two attendees named Tom, conflicting cues) is left unresolved. Every
// match carries the cue that produced it, so the mapping can be inspected later.
package speakers

import (
	"regexp"
	"sort"
	"strings"
)

// Candidate is an attendee of the call's calendar event who could be the speaker.
type Candidate struct {
	PersonID string
	Name     string
}

// Segment is one transcript turn.
type Segment struct {
	Label string
	Text  string
}

// Input describes one call. Candidates must already exclude attendees that other speakers of
// the same call are known to be.
type Input struct {
	Unlabelled []string
	Candidates []Candidate
	Segments   []Segment
}

// Cue kinds.
const (
	CueIntro    = "intro"    // the speaker names themselves
	CueVocative = "vocative" // the previous speaker addresses them by name
	CueThanks   = "thanks"   // the next speaker thanks them by name
)

// Cue is the transcript evidence behind a match.
type Cue struct {
	Kind    string // CueIntro, CueVocative or CueThanks
	Pattern string // stable pattern id, e.g. "intro:name-here"
	Span    string // the matched text
}

// Match maps a speaker label to a person.
type Match struct {
	Label    string
	PersonID string
	Cue      Cue
}

const (
	// pre and post bound a name: not inside a longer word, and not a possessive ("Priya's").
	pre  = `(?:^|[^\p{L}\p{N}_])`
	post = `(?:$|[^\p{L}\p{N}_'’]|['’](?:[^sS]|$))`
)

type pattern struct {
	id   string
	kind string
	tmpl string // %s is the quoted name form
}

// patterns are the cue patterns, strongest evidence first.
func patterns() []pattern {
	return []pattern{
		{"intro:name-here", CueIntro, `(?i)` + pre + `%s\s+here` + post},
		{"intro:im-name", CueIntro, `(?i)` + pre + `(?:i['’]?m|i am|this is|my name is|it['’]?s)\s+%s` + post},
		{"intro:name-comma-i", CueIntro, `(?i)^\s*(?:(?:sure|yes|hi|hello|hey|okay|ok|so|well)[\s,.!;-]+)*%s\s*[,.;-]\s*(?:i|and i|from)\b`},
		{"vocative:name-comma", CueVocative, `(?i)(?:^|[.!?]\s+)(?:(?:hi|hello|hey|okay|ok|so|and|welcome)[\s,]+)?%s\s*[,:?!.]`},
		{"thanks:thanks-name", CueThanks, `(?i)` + pre + `(?:thanks|thank you)[\s,]+%s` + post},
	}
}

type compiled struct {
	pattern
	full, first *regexp.Regexp
}

type matcher struct {
	personID string
	rules    []compiled
}

func newMatcher(c Candidate) (matcher, bool) {
	fields := strings.Fields(strings.ToLower(c.Name))
	if c.PersonID == "" || len(fields) == 0 {
		return matcher{}, false
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = regexp.QuoteMeta(f)
	}
	fullName, firstName := strings.Join(quoted, `\s+`), quoted[0]
	m := matcher{personID: c.PersonID}
	for _, p := range patterns() {
		m.rules = append(m.rules, compiled{
			pattern: p,
			full:    regexp.MustCompile(strings.ReplaceAll(p.tmpl, "%s", fullName)),
			first:   regexp.MustCompile(strings.ReplaceAll(p.tmpl, "%s", firstName)),
		})
	}
	return m, true
}

// hit is one candidate a cue in a text points at.
type hit struct {
	personID string
	cue      Cue
}

func firstCue(re *regexp.Regexp, p pattern, text string) (Cue, bool) {
	loc := re.FindStringIndex(text)
	if loc == nil {
		return Cue{}, false
	}
	span := strings.TrimLeft(strings.TrimSpace(text[loc[0]:loc[1]]), " ,.;:!?-")
	return Cue{Kind: p.kind, Pattern: p.id, Span: span}, true
}

// hits returns the candidates a cue of the given kind in text points at: those whose full name
// matches, or, when none does, those whose first name matches.
func hits(ms []matcher, kind, text string) []hit {
	var full, first []hit
	for _, m := range ms {
		for _, r := range m.rules {
			if r.kind != kind {
				continue
			}
			if c, ok := firstCue(r.full, r.pattern, text); ok {
				full = append(full, hit{m.personID, c})
				break
			}
		}
		for _, r := range m.rules {
			if r.kind != kind {
				continue
			}
			if c, ok := firstCue(r.first, r.pattern, text); ok {
				first = append(first, hit{m.personID, c})
				break
			}
		}
	}
	if len(full) > 0 {
		return full
	}
	return first
}

// Resolve returns the labels that map to exactly one attendee, ordered by label.
func Resolve(in Input) []Match {
	ms := matchers(in.Candidates)
	if len(ms) == 0 || len(in.Unlabelled) == 0 {
		return nil
	}
	unlabelled := map[string]bool{}
	for _, l := range in.Unlabelled {
		unlabelled[l] = true
	}
	cues := map[string]map[string]Cue{}
	add := func(label string, hs []hit) {
		if !unlabelled[label] {
			return
		}
		if cues[label] == nil {
			cues[label] = map[string]Cue{}
		}
		for _, h := range hs {
			if old, seen := cues[label][h.personID]; !seen || rank(h.cue.Kind) < rank(old.Kind) {
				cues[label][h.personID] = h.cue
			}
		}
	}
	for i, s := range in.Segments {
		add(s.Label, hits(ms, CueIntro, s.Text))
		if next, ok := otherLabel(in.Segments, i, +1); ok {
			add(next, hits(ms, CueVocative, s.Text))
		}
		if prev, ok := otherLabel(in.Segments, i, -1); ok {
			add(prev, hits(ms, CueThanks, s.Text))
		}
	}
	return unique(cues)
}

// rank orders cue kinds by strength: a self-introduction beats being addressed.
func rank(kind string) int {
	switch kind {
	case CueIntro:
		return 0
	case CueVocative:
		return 1
	default:
		return 2
	}
}

func matchers(cands []Candidate) []matcher {
	seen := map[string]bool{}
	var out []matcher
	for _, c := range cands {
		if seen[c.PersonID] {
			continue
		}
		m, ok := newMatcher(c)
		if !ok {
			continue
		}
		seen[c.PersonID] = true
		out = append(out, m)
	}
	return out
}

// otherLabel finds the nearest segment from index i, in direction dir, spoken by a different
// label than segment i.
func otherLabel(segs []Segment, i, dir int) (string, bool) {
	for j := i + dir; j >= 0 && j < len(segs); j += dir {
		if segs[j].Label != segs[i].Label {
			return segs[j].Label, true
		}
	}
	return "", false
}

// unique keeps labels with exactly one cued person who is cued for no other label.
func unique(cues map[string]map[string]Cue) []Match {
	owners := map[string]int{}
	for _, ids := range cues {
		for id := range ids {
			owners[id]++
		}
	}
	var out []Match
	for label, ids := range cues {
		if len(ids) != 1 {
			continue
		}
		for id, cue := range ids {
			if owners[id] == 1 {
				out = append(out, Match{Label: label, PersonID: id, Cue: cue})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
