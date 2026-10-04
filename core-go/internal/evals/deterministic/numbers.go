package deterministic

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// numWords is a spelled-out number: "fifty", "twenty-five", "two hundred", "a dozen".
const numWords = `(?:a\s+dozen|dozen|zero|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|` +
	`sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|sixty|seventy|eighty|ninety|hundred|thousand)`

const numPhrase = numWords + `(?:[\s-]+(?:and\s+)?` + numWords + `)*`

const units = `(seats?|licen[cs]es?|users?|clinics?|sites?|locations?|teams?|warehouses?|employees?|agents?|stores?|offices?|` +
	`branch(?:es)?|hospitals?|instances?|workspaces?|departments?)`

// Fixed patterns for figures in text (ADR-0014).
var (
	moneyRE = regexp.MustCompile(`(?i)(?:([$€£])\s?(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d+))?(?:\s?(k|m|bn|million|thousand|billion)\b)?)` +
		`|(?:\b(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d+))?\s?(k|m|million|thousand)?\s?(usd|eur|gbp|dollars|euros|pounds)\b)`)
	currencyCodeRE = regexp.MustCompile(`(?i)\b(?:usd|eur|gbp)\s?(\d{1,3}(?:,\d{3})+|\d+)(?:\.(\d+))?`)
	wordMoneyRE    = regexp.MustCompile(`(?i)\b(` + numPhrase + `)\s+(?:dollars|euros|pounds|usd|bucks)\b`)
	percentRE      = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s?(?:%|percent\b)`)
	wordPercentRE  = regexp.MustCompile(`(?i)\b(` + numPhrase + `)\s+percent\b`)
	fractionRE     = regexp.MustCompile(`(?i)\b0?\.(\d{1,2})\s+(?:reduction|discount|off)\b`)
	quantityRE     = regexp.MustCompile(`(?i)\b(\d{1,3}(?:,\d{3})+|\d+|` + numPhrase + `)\s+(?:[a-z-]+\s+)?` + units + `\b`)
	freeRE         = regexp.MustCompile(`(?i)\b(?:(?:\d+|` + numPhrase + `)\s+(?:additional\s+|extra\s+)?(?:months?|weeks?)\s+(?:for\s+)?free|free\s+(?:\d+|` + numPhrase + `)\s+(?:months?|weeks?))\b`)
	perRE          = regexp.MustCompile(`(?i)\sper\s|\sa\s|\seach\s`)
	acronymRoleRE  = regexp.MustCompile(`\b(CFO|CEO|CTO|CIO|CISO|COO|CRO|CMO|CPO|CDO|SVP|EVP|VP)\b`)
	phraseRoleRE   = regexp.MustCompile(`(?i)\b(general counsel|chief [a-z]+ officer|head of [a-z]+|vice president)\b`)
)

var smallWords = map[string]float64{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
	"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40,
	"fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90}

var multipliers = map[string]float64{"k": 1e3, "thousand": 1e3, "m": 1e6, "million": 1e6, "bn": 1e9, "billion": 1e9}

// Quantity is a number of units ("120 seats").
type Quantity struct {
	Value float64
	Unit  string
	Text  string
}

// amount is a money figure.
type amount struct {
	Value float64
	Text  string
}

// spelledNumber reads "twenty-five", "two hundred", "a dozen"; ok is false when s is not a spelled number.
func spelledNumber(s string) (float64, bool) {
	var total, current float64
	seen := false
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == '-' || r == '\t' || r == '\n' }) {
		switch w {
		case "a", "and":
			continue
		case "dozen":
			current = math.Max(current, 1) * 12
		case "hundred":
			current = math.Max(current, 1) * 100
		case "thousand":
			total += math.Max(current, 1) * 1000
			current = 0
		default:
			v, ok := smallWords[w]
			if !ok {
				return 0, false
			}
			current += v
		}
		seen = true
	}
	return total + current, seen
}

func parseNumber(intPart, frac string) float64 {
	v, err := strconv.ParseFloat(strings.ReplaceAll(intPart, ",", "")+fracSuffix(frac), 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func fracSuffix(frac string) string {
	if frac == "" {
		return ""
	}
	return "." + frac
}

// numberOf reads digits or a spelled number.
func numberOf(s string) float64 {
	if v, ok := spelledNumber(s); ok {
		return v
	}
	return parseNumber(s, "")
}

// moneyAmounts finds money figures; "$98k", "$98,000", "USD 98000" and "ninety-eight thousand dollars" agree.
func moneyAmounts(text string) []amount {
	var out []amount
	add := func(v float64, text string) {
		if !math.IsNaN(v) {
			out = append(out, amount{Value: v, Text: strings.TrimSpace(text)})
		}
	}
	for _, m := range moneyRE.FindAllStringSubmatch(text, -1) {
		intPart, frac, mult := m[2], m[3], m[4]
		if m[1] == "" {
			intPart, frac, mult = m[5], m[6], m[7]
		}
		v := parseNumber(intPart, frac)
		if f, ok := multipliers[strings.ToLower(mult)]; ok {
			v *= f
		}
		add(v, m[0])
	}
	for _, m := range currencyCodeRE.FindAllStringSubmatch(text, -1) {
		add(parseNumber(m[1], m[2]), m[0])
	}
	for _, m := range wordMoneyRE.FindAllStringSubmatch(text, -1) {
		add(numberOf(m[1]), m[0])
	}
	return out
}

// percents finds percentages written as digits, as words ("fifteen percent") or as a fraction
// of a reduction ("0.15 reduction").
func percents(text string) []float64 {
	var out []float64
	for _, m := range percentRE.FindAllStringSubmatch(text, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			out = append(out, v)
		}
	}
	for _, m := range wordPercentRE.FindAllStringSubmatch(text, -1) {
		out = append(out, numberOf(m[1]))
	}
	for _, m := range fractionRE.FindAllStringSubmatch(text, -1) {
		if v, err := strconv.ParseFloat("0."+m[1], 64); err == nil {
			out = append(out, math.Round(v*100*100)/100)
		}
	}
	return out
}

// quantities finds "<n> <unit>" figures; units are singular and spelled numbers become numbers.
// A price per unit ("$40 per seat") is not a quantity.
func quantities(text string) []Quantity {
	var out []Quantity
	for _, idx := range quantityRE.FindAllStringSubmatchIndex(text, -1) {
		m := make([]string, len(idx)/2)
		for i := range m {
			if idx[2*i] >= 0 {
				m[i] = text[idx[2*i]:idx[2*i+1]]
			}
		}
		if followsCurrency(text, idx[0]) || perRE.MatchString(m[0]) {
			continue
		}
		if v := numberOf(m[1]); !math.IsNaN(v) {
			out = append(out, Quantity{Value: v, Unit: singular(m[2]), Text: m[0]})
		}
	}
	return out
}

// followsCurrency reports whether the rune just before byte offset i is a currency symbol.
func followsCurrency(text string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return r == '$' || r == '€' || r == '£'
}

func singular(unit string) string {
	u := strings.ToLower(unit)
	switch {
	case strings.HasPrefix(u, "licen"):
		return "license"
	case strings.HasPrefix(u, "branch"):
		return "branch"
	}
	return strings.TrimSuffix(u, "s")
}

// roles finds job-title assertions ("your CFO", "head of security").
func roles(text string) []string {
	var out []string
	out = append(out, acronymRoleRE.FindAllString(text, -1)...)
	for _, r := range phraseRoleRE.FindAllString(text, -1) {
		out = append(out, strings.ToLower(r))
	}
	return unique(out)
}

func sameValue(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
