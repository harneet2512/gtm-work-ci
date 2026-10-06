package deterministic

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// discount is a discount or concession the draft offers in one clause. Sure is false when the
// wording cannot be classified with high precision ("25% off your first year"): such a finding
// does not block and the semantic judges decide.
type discount struct {
	Percent float64 // 0 for a concession such as "two months free"
	Free    string  // the concession wording, empty for a percentage
	Text    string
	Sure    bool
	Unclear bool // a refusal follows the figure: flagged without blocking
}

// A percentage is a discount only when a discount term sits next to it ("15% off", "a discount of
// 15%", "discount the renewal by 15%", "15% below list", "0.15 reduction"). What it is taken off
// decides the rest: OUR price, plan, licence, seats, renewal or a catalog product, or nothing at
// all ("15% off if you sign"), is an offer; a customer's own metric ("20% off their handling
// time", "a 30% reduction in support tickets") is a result; anything else is unclear and does not
// block. Negation applies to the discount phrase itself, not the clause around it.
var (
	percentSpanRE  = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s?(?:%|percent\b)|\b(` + numPhrase + `)\s+percent\b|\b0?\.(\d{1,2})\s+(?:reduction|discount|off)\b`)
	termAfterRE    = regexp.MustCompile(`(?i)^\s*(?:(?:reduction|discount\w*|rebate|markdown|concession|off|below|lower|cheaper|less)\b)`)
	termBeforeRE   = regexp.MustCompile(`(?i)(?:\b(?:discount|rebate|markdown|reduction|concession)\s+of\s+$|\b(?:discount|reduce|lower|cut)\w*\s+(?:(?:the|your|our)\s+)?(?:[a-z-]+\s+){0,2}by\s+$)`)
	resultMarkerRE = regexp.MustCompile(`(?i)\b(see|sees|saw|seen|achiev\w*|typical\w*|average\w*|report\w*|cut|gain\w*|experienc\w*|enjoy\w*|realiz\w*)\b`)
	negatedRE      = regexp.MustCompile(`(?i)(?:(?:can't|cannot|can not|won't|will not|unable to|not able to|don't|do not|not going to|can never)\s+(?:(?:just|only|merely|simply)\s+)?(?:\S+\s+){0,2}$|\b(?:is|are|was|were)\s+not\s+$|\b(?:isn't|aren't|wasn't|weren't)\s+$|\bnot\s+$)`)
	// requestRE: the customer asks, asks about or hopes for the figure ("you asked for 25% off",
	// "thanks for asking about 25%", "you were hoping for 25%", "could you do 25% off").
	requestRE = regexp.MustCompile(`(?i)(?:\b(?:asked|asking|ask|requested|request|wanted|want|hoped|hoping|expected|expecting|expect|mentioned|looking)\s+(?:for|about|at)?\s*(?:\S+\s+){0,2}$|\b(?:could|can|would|will)\s+you\s+(?:\S+\s+){0,2}$)`)
	// offerQuestionRE: a question that offers it ("would you like 25% off") is not a request.
	offerQuestionRE = regexp.MustCompile(`(?i)(?:would you like|do you want|would you want|how about|what about|shall we do)\s*$`)
	// requestAfterRE: "the 25% you asked about".
	requestAfterRE = regexp.MustCompile(`(?i)^\W*(?:that\s+)?(?:you|they)\s+(?:asked|were asking|mentioned|requested|raised|had asked)\b`)
	// postNegRE: a refusal after the figure in the same clause ("a 25% discount isn't possible").
	postNegRE     = regexp.MustCompile(`(?i)\b(?:isn't|aren't|wasn't|is not|are not|not possible|not something|cannot|can't|can not|won't|will not|unable|not able|no way)\b`)
	justRE        = regexp.MustCompile(`(?i)(?:don't|do not|doesn't|does not)\s+(?:just|only|merely|simply)\s+(?:\S+\s+){0,2}$`)
	freeConcessRE = regexp.MustCompile(`(?i)\b(?:a\s+free\s+(?:month|week)s?|free\s+(?:month|week)s?)\b`)
	wordSplitRE   = regexp.MustCompile(`[^\p{L}\p{N}'-]+`)
)

var priceNouns = toSet(strings.Fields(`price prices pricing plan plans licence licences license licenses seat seats subscription
contract renewal quote invoice list package total fee fees order deal bundle annual term`))

var objectSkip = toSet(strings.Fields(`in on to of from off the a an this that`))

var objectEnders = toSet(strings.Fields(`if when for as and but so once with by today now week month year which that who because though`))

var thirdPossessives = toSet(strings.Fields(`their its his her`))

var ourPossessives = toSet(strings.Fields(`your our`))

// discounts finds the percentages and free-time concessions the draft offers. products are the
// catalog's product names: a discount taken "off Alpha" is an offer on our product.
func discounts(text string, products []string) []discount {
	var out []discount
	for _, c := range clauses(text) {
		for _, re := range []*regexp.Regexp{freeRE, freeConcessRE} {
			for _, loc := range re.FindAllStringIndex(c, -1) {
				if !negatedRE.MatchString(c[:loc[0]]) {
					out = append(out, discount{Free: c[loc[0]:loc[1]], Text: c, Sure: true})
				}
			}
		}
		for _, loc := range percentSpanRE.FindAllStringSubmatchIndex(c, -1) {
			if d, ok := discountAt(c, loc, products); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// isNegated: the discount phrase is refused ("we cannot do", "is not 15% off list"), unless the
// refusal is only a "don't just offer ..." that goes on to offer it.
func isNegated(before string) bool {
	return negatedRE.MatchString(before) && !justRE.MatchString(before)
}

type pctKind int

const (
	kindPlain   pctKind = iota // not a discount: "80% of the team", or a reported result
	kindRequest                // the customer's request, echoed or asked about
	kindNegated                // refused: "we cannot do 25%", "not 25% off", "25% isn't possible"
	kindOffer                  // an offer (Sure) or possibly one (not Sure)
)

// classifyPercent says what one percentage mention in a clause is. A request, a refusal (before
// or after the figure) and a result are not offers.
func classifyPercent(c string, loc []int, products []string) (pctKind, discount) {
	value, ok := percentValue(c, loc)
	if !ok {
		return kindPlain, discount{}
	}
	before, after := c[:loc[0]], c[loc[1]:]
	if (requestRE.MatchString(before) && !offerQuestionRE.MatchString(before)) || requestAfterRE.MatchString(after) {
		return kindRequest, discount{Percent: value, Text: c}
	}
	termAfter := termAfterRE.MatchString(after) || loc[6] >= 0 // "0.15 reduction" carries its term
	if !termAfter && !termBeforeRE.MatchString(before) {
		return kindPlain, discount{Percent: value, Text: c} // a percentage that is not a discount
	}
	if isNegated(before) {
		return kindNegated, discount{Percent: value, Text: c}
	}
	if postNegRE.MatchString(after) {
		// A refusal after the figure: the wording is unclear, so it is flagged without blocking.
		return kindNegated, discount{Percent: value, Text: c, Unclear: true}
	}
	switch objectKind(after, products) {
	case objectResult:
		return kindPlain, discount{Percent: value, Text: c}
	case objectBare:
		if resultMarkerRE.MatchString(lastWords(before, 6)) {
			return kindPlain, discount{Percent: value, Text: c}
		}
		return kindOffer, discount{Percent: value, Text: c, Sure: true}
	case objectOffer:
		return kindOffer, discount{Percent: value, Text: c, Sure: true}
	}
	if resultMarkerRE.MatchString(lastWords(before, 6)) {
		return kindPlain, discount{Percent: value, Text: c}
	}
	return kindOffer, discount{Percent: value, Text: c} // unclear: flag without blocking
}

// discountAt is the discount a mention offers, if it offers one (or may, in unclear wording).
func discountAt(c string, loc []int, products []string) (discount, bool) {
	kind, d := classifyPercent(c, loc, products)
	return d, kind == kindOffer || (kind == kindNegated && d.Unclear)
}

func percentValue(c string, loc []int) (float64, bool) {
	switch {
	case loc[2] >= 0:
		v, err := strconv.ParseFloat(c[loc[2]:loc[3]], 64)
		return v, err == nil
	case loc[4] >= 0:
		v := numberOf(c[loc[4]:loc[5]])
		return v, !math.IsNaN(v)
	case loc[6] >= 0:
		v, err := strconv.ParseFloat("0."+c[loc[6]:loc[7]], 64)
		return math.Round(v * 100), err == nil
	}
	return 0, false
}

type objectClass int

const (
	objectBare   objectClass = iota // nothing is named: "15% off if you sign"
	objectOffer                     // our price, plan, licence, seats, renewal or product
	objectMaybe                     // cannot be classified
	objectResult                    // a customer's metric
)

// objectKind classifies what a discount term is applied to, from the words after the term.
func objectKind(after string, products []string) objectClass {
	var rest []string
	for _, w := range wordSplitRE.Split(strings.TrimSpace(after), -1) {
		if w != "" {
			rest = append(rest, w)
		}
	}
	term := ""
	if len(rest) > 0 && termAfterRE.MatchString(after) {
		term, rest = strings.ToLower(rest[0]), rest[1:]
	}
	for len(rest) > 0 && objectSkip[strings.ToLower(rest[0])] {
		rest = rest[1:]
	}
	if len(rest) == 0 || objectEnders[strings.ToLower(rest[0])] {
		return objectBare
	}
	first := strings.ToLower(rest[0])
	switch {
	case thirdPossessives[first]:
		return objectResult
	case ourPossessives[first]:
		// "your" is an offer object only directly before a price noun ("your renewal"); "your
		// licensed seats" or "your first year" is a metric or unclear.
		if len(rest) > 1 && (priceNouns[strings.ToLower(rest[1])] || isProduct(rest[1], products)) {
			return objectOffer
		}
		return objectMaybe
	}
	for i, w := range rest {
		if i >= 3 {
			break
		}
		if priceNouns[strings.ToLower(w)] || isProduct(w, products) || (i == 0 && startsUpper(w)) {
			return objectOffer
		}
	}
	if term == "reduction" || term == "lower" || term == "less" {
		return objectResult // "a 30% reduction in support tickets"
	}
	return objectMaybe
}

func isProduct(w string, products []string) bool {
	for _, p := range products {
		if strings.EqualFold(p, w) {
			return true
		}
	}
	return false
}

func startsUpper(w string) bool {
	return w != "" && w[0] >= 'A' && w[0] <= 'Z'
}

func lastWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) > n {
		w = w[len(w)-n:]
	}
	return strings.Join(w, " ")
}

// percentMention is one percentage of a text with what it is.
type percentMention struct {
	Value float64
	Kind  pctKind
	Sure  bool
}

func percentMentions(text string, products []string) []percentMention {
	var out []percentMention
	for _, c := range clauses(text) {
		for _, loc := range percentSpanRE.FindAllStringSubmatchIndex(c, -1) {
			if _, ok := percentValue(c, loc); !ok {
				continue
			}
			kind, d := classifyPercent(c, loc, products)
			out = append(out, percentMention{Value: d.Percent, Kind: kind, Sure: d.Sure})
		}
	}
	return out
}

// draftPercentChecks are the draft's percentages that need support, split by what supports them.
// A request echoed, a refusal and a reported result assert no offer: they need only be mentioned
// somewhere in the evidence (a result) or nothing at all (a request or refusal). An offer needs
// support that is not a request: a discount the customer asked for is not approval.
func draftPercentChecks(text string, products []string) (plain, offers []float64) {
	for _, m := range percentMentions(text, products) {
		switch m.Kind {
		case kindPlain:
			plain = append(plain, m.Value)
		case kindOffer:
			offers = append(offers, m.Value)
		}
	}
	return plain, offers
}

// supportPercents are the percentages of cited evidence that can support an offer in the draft:
// everything except the customer's requests and the evidence's own offers.
func supportPercents(text string, products []string) []float64 {
	var out []float64
	for _, m := range percentMentions(text, products) {
		if m.Kind == kindPlain || m.Kind == kindNegated || (m.Kind == kindOffer && !m.Sure) {
			out = append(out, m.Value)
		}
	}
	return out
}
