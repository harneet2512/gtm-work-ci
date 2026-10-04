package deterministic

import "regexp"

var (
	// pastRE marks wording that reports something that already happened ("thanks for the call on",
	// "as discussed on", "we met on").
	pastRE = regexp.MustCompile(`(?i)\b(thanks?(?: you)? for|thank you|as (?:we )?(?:discussed|agreed)|on (?:our|the) (?:last |previous )?(?:call|meeting|demo|sync|chat)|` +
		`(?:we|i|you|they) (?:met|spoke|talked|discussed|agreed|had|held|chatted)|` +
		`(?:met|spoke|talked|discussed|agreed|sent|shared|received|attended|joined|held|emailed|called)|` +
		`last (?:week|month|time)|following (?:our|the|your)|ago|earlier|previously|yesterday|on the call)\b`)
	// upcomingSuffixRE marks a date that is offered as a slot ("September 22 works for me").
	upcomingSuffixRE = regexp.MustCompile(`(?i)^[\s,]*(works|would work|is (?:fine|good|open|free|available)|suits)`)
)

// lastMatch is the start of the last match of re in s, or -1.
func lastMatch(re *regexp.Regexp, s string) int {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return -1
	}
	return locs[len(locs)-1][0]
}

// upcoming reports whether the draft presents the date as still to come: a forward-looking word
// before it that is later than any past-tense wording, or a slot-offering phrase after it.
func (d clauseDate) upcoming() bool {
	if upcomingSuffixRE.MatchString(d.Clause[d.End:]) {
		return true
	}
	f := lastMatch(futureRE, d.Prefix)
	return f >= 0 && f > lastMatch(pastRE, d.Prefix)
}

// pastContext reports whether the draft reports the date as something that already happened.
func (d clauseDate) pastContext() bool {
	p := lastMatch(pastRE, d.Prefix)
	return p >= 0 && p > lastMatch(futureRE, d.Prefix)
}
