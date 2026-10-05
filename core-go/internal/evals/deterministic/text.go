package deterministic

import (
	"regexp"
	"strings"
)

// Fixed text rules (ADR-0014): clauses, content tokens and delivery/promise phrases. No model.
var (
	clauseSplitRE = regexp.MustCompile(`[!?;\n]+|[.:](?:\s+|$)|\s[-–—]\s`)
	tokenRE       = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}'.-]*[\p{L}\p{N}]|[\p{L}\p{N}]`)
	deliveryRE    = regexp.MustCompile(`(?i)\b(attached|attaching|enclosed|re-?attached|here (?:is|are)|here's|sharing|shared|please find)\b`)
	attachClaimRE = regexp.MustCompile(`(?i)\b(attached|attaching|enclosed|re-?attached|please find)\b`)
)

var stopwords = toSet(strings.Fields(`a an the and or but if of to in on at for with from by as is are was were be been
being this that these those it its it's i i'm i'll i've i'd we we're we'll we've you you're you'll you've your yours our
ours us me my he she they them their his her hi hello hey thanks thank best regards dear just so up out about into over
can could would should will shall may might do does did have has had not no yes all any some very also there here what
which who whom when where why how let's let know please re fw fwd cc via per`))

// deliveryWords are dropped before comparing delivered content.
var deliveryWords = toSet(strings.Fields(`attached attaching enclosed re-attached reattached sharing shared find
promised here's`))

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// normalize lower-cases, unifies quotes and dashes and collapses whitespace.
func normalize(s string) string {
	r := strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`, "–", "-", "—", "-", " ", " ")
	return strings.Join(strings.Fields(strings.ToLower(r.Replace(s))), " ")
}

// clauses splits text into sentence-like clauses.
func clauses(text string) []string {
	var out []string
	for _, c := range clauseSplitRE.Split(text, -1) {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// tokens are the lower-cased word tokens of s (trailing dots dropped).
func tokens(s string) []string {
	raw := tokenRE.FindAllString(normalize(s), -1)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		out = append(out, strings.TrimRight(t, "."))
	}
	return out
}

// contentTokens are tokens without stopwords and without the extra words given.
func contentTokens(s string, drop map[string]bool) []string {
	var out []string
	for _, t := range tokens(s) {
		if !stopwords[t] && !drop[t] {
			out = append(out, t)
		}
	}
	return out
}

// containment is the share of a's distinct tokens that also occur in b.
func containment(a, b []string) float64 {
	as, bs := toSet(a), toSet(b)
	if len(as) == 0 {
		return 0
	}
	n := 0
	for t := range as {
		if bs[t] {
			n++
		}
	}
	return float64(n) / float64(len(as))
}

// containsText reports whether needle occurs in haystack as a substring after normalization.
// It is for long verbatim phrases (quotes), not names.
func containsText(haystack, needle string) bool {
	n := normalize(needle)
	return n != "" && strings.Contains(normalize(haystack), n)
}

// minNameLen is the shortest alias or name that is matched at all.
const minNameLen = 2

// containsFold reports whether needle occurs in haystack as a whole word or phrase: bounded by
// non-letters and non-digits on both sides, so "SIG" is not found in "sign" or "design" and "SOC"
// not in "associate". A short all-caps needle ("SIG", "SOC") is matched case-sensitively, so the
// lowercase word "soc" or "sig" does not count either. Needles under minNameLen never match.
func containsFold(haystack, needle string) bool {
	needle = strings.TrimSpace(needle)
	if len([]rune(needle)) < minNameLen {
		return false
	}
	if isShortCaps(needle) {
		return wordRE(needle, false).MatchString(haystack)
	}
	return wordRE(normalize(needle), true).MatchString(normalize(haystack))
}

func isShortCaps(s string) bool {
	return len([]rune(s)) <= 5 && s == strings.ToUpper(s) && s != strings.ToLower(s)
}

func wordRE(needle string, fold bool) *regexp.Regexp {
	prefix := ""
	if fold {
		prefix = "(?i)"
	}
	return regexp.MustCompile(prefix + `(?:^|[^\p{L}\p{N}])` + regexp.QuoteMeta(needle) + `(?:$|[^\p{L}\p{N}])`)
}

// isDelivery reports whether a clause hands material over (as opposed to promising it).
func isDelivery(clause string) bool { return deliveryRE.MatchString(clause) }
