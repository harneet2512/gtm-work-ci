package slacksurface

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"strings"
)

// The shared eval wording (contracts/evals/eval_wording.json, copied here; a test keeps it identical): verdict
// order, labels and emoji, plain eval names, evidence-class tags and phrases. Slack renders evals from it so it
// says exactly what the web says (eval design spec 4b-3: same vocabulary, order and icons; only depth differs).
//
//go:embed eval_wording.json
var wordingJSON []byte

type verdictWord struct {
	Order      int    `json:"order"`
	Label      string `json:"label"`
	SlackEmoji string `json:"slack_emoji"`
}

type wordingTable struct {
	Verdicts        map[string]verdictWord `json:"verdicts"`
	NotChecked      verdictWord            `json:"not_checked"`
	EvidenceClasses map[string]struct {
		Tag string `json:"tag"`
	} `json:"evidence_classes"`
	EvalTypes map[string]struct {
		Name string `json:"name"`
	} `json:"eval_types"`
	Phrases map[string]string `json:"phrases"`
}

var wording = mustWording()

func mustWording() wordingTable {
	var w wordingTable
	if err := json.Unmarshal(wordingJSON, &w); err != nil {
		panic("slacksurface: embedded eval_wording.json: " + err.Error())
	}
	return w
}

func verdictWordOf(v Verdict) verdictWord {
	if w, ok := wording.Verdicts[string(v)]; ok {
		return w
	}
	return wording.NotChecked
}

func verdictLabel(v Verdict) string { return verdictWordOf(v).Label }
func verdictEmoji(v Verdict) string { return verdictWordOf(v).SlackEmoji }
func verdictOrder(v Verdict) int    { return verdictWordOf(v).Order }

// evalName is the plain name of an eval type; a type the table lacks still never shows its raw code.
func evalName(evalType string) string {
	if t, ok := wording.EvalTypes[evalType]; ok && t.Name != "" {
		return t.Name
	}
	return humanize(evalType)
}

func evidenceClassTag(class string) string { return wording.EvidenceClasses[class].Tag }

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// phrase fills a shared phrase's {placeholders}; a missing value stays visible rather than vanishing.
func phrase(key string, vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(wording.Phrases[key], func(m string) string {
		if v, ok := vars[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// firstSentence is the reason's first sentence (a period followed by a space, or the end), capped.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	return truncate(s, reasonChars)
}
