package orchestrator

import (
	"regexp"
	"slices"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/workerclient"
)

// Transition statuses the candidate policy restricts under: the move to the new state is not confirmed.
var openTransition = map[string]bool{"CANDIDATE": true, "UNRESOLVED": true}

// Policy reasons (eval_bundle.v1.json candidate_policy.reasons).
const (
	ReasonExpansionMotion = "expansion_motion"
	ReasonPricingPush     = "pricing_push"
	ReasonEscalation      = "commercial_escalation"
	ReasonBroadOutreach   = "broad_outreach"
	// ReasonLabelMismatch: the text of an external action is an expansion CTA but its action_class says otherwise.
	ReasonLabelMismatch = "expansion_label_mismatch"
)

// broadOutreachRecipients is the audience size from which an external message counts as broad outreach.
const broadOutreachRecipients = 4

var (
	pricingPush = regexp.MustCompile(`(?i)\b(pric(e|es|ing)|discount\w*|quote[sd]?|per[- ]seat|rate card)\b|[$€£]\s?\d|\d+\s?%\s?(off|discount)`)
	// escalation reads "sign" only as a commercial act ("you sign", "sign the order form", "signed", "signature"), never as the
	// noun in "a good sign".
	escalation = regexp.MustCompile(`(?i)\b(order form|signed|signing|signatures?|e-?sign\w*|sign[- ]off|(you|we|they|to|please|can|could|will|would)\s+sign|sign\s+(the|this|an?|our|your|it|off|up|before|by)|contract|purchase order|po|msa|master services agreement|close (the|this) deal|escalat\w*|executive sponsor|steering committee)\b`)
	// expansionCTA is the deterministic text detector for an expansion call to action in an external message: a rollout,
	// extra or additional seats, licences or users, a new team, org, region or department, an upgrade, expanding, or a
	// broader deployment. It runs on the text so a mislabelled candidate cannot bypass the policy.
	expansionCTA = regexp.MustCompile(`(?i)\b(` +
		`roll(ing|ed)?[- ]?outs?` +
		`|(extra|additional|more|further|new)\s+(seats?|licen[cs]es?|users?)` +
		`|(add|adding|buy|buying|purchase|purchasing)\s+(\d+\s+)?(more\s+)?(seats?|licen[cs]es?)` +
		`|(new|another|second|additional|other)\s+(teams?|orgs?|organi[sz]ations?|regions?|departments?|business units?|divisions?|subsidiar(y|ies)|offices?|geograph(y|ies))` +
		`|upgrad(e|es|ed|ing)` +
		`|expan(d|ds|ded|ding|sion|sions)` +
		`|(broader|wider|company[- ]wide|org(anization)?[- ]wide|enterprise[- ]wide|global|full)\s+(deployment|adoption|rollout|roll[- ]out)` +
		`)\b`)
	external = map[string]bool{"send_email": true, "schedule_meeting": true, "share_document": true}
)

// PolicyVerdict is eval_bundle.v1.json candidate_policy.
type PolicyVerdict struct {
	TransitionStatus    string   `json:"transition_status"`
	Status              string   `json:"status"` // allowed | restricted
	Reasons             []string `json:"reasons"`
	RequiresHumanReview bool     `json:"requires_human_review"`
}

// Restricted reports whether the verdict restricts the candidate.
func (v *PolicyVerdict) Restricted() bool { return v != nil && v.Status == "restricted" }

// CandidatePolicy applies the HAR-128 transition policy to one candidate, deterministically and after generation.
// While the transition is CANDIDATE (or UNRESOLVED, which routes to the same conservative suite) the allowed moves
// are research, an account brief, asking the internal owner, a low-pressure follow-up and waiting. An expansion
// motion (by its label or, for an external action, by the text of its CTA: a mislabelled candidate is restricted
// and the mismatch recorded), a pricing push, commercial escalation or broad outreach is restricted: it needs human review and can
// never be Ghost's preferred candidate. With no transition there is no verdict (nil); a CONFIRMED or REJECTED
// transition allows everything.
func CandidatePolicy(transitionStatus string, c workerclient.Candidate) *PolicyVerdict {
	if transitionStatus == "" {
		return nil
	}
	v := &PolicyVerdict{TransitionStatus: transitionStatus, Status: "allowed", Reasons: []string{}}
	if !openTransition[transitionStatus] {
		return v
	}
	if c.ActionClass == "EXPANSION_MOTION" {
		v.Reasons = append(v.Reasons, ReasonExpansionMotion)
	}
	if external[c.ActionType] {
		text := candidateText(c)
		if c.ActionClass != "EXPANSION_MOTION" && expansionCTA.MatchString(text) {
			v.Reasons = append(v.Reasons, ReasonExpansionMotion, ReasonLabelMismatch)
		}
		if pricingPush.MatchString(text) {
			v.Reasons = append(v.Reasons, ReasonPricingPush)
		}
		if escalation.MatchString(text) {
			v.Reasons = append(v.Reasons, ReasonEscalation)
		}
		if len(distinctPeople(c)) >= broadOutreachRecipients {
			v.Reasons = append(v.Reasons, ReasonBroadOutreach)
		}
	}
	if len(v.Reasons) > 0 {
		v.Status, v.RequiresHumanReview = "restricted", true
	}
	return v
}

// candidateText is what the recipient would read.
func candidateText(c workerclient.Candidate) string {
	return strings.Join([]string{derefString(c.Subject), c.FullActionArtifact.Body}, "\n")
}

// hasAllowed reports whether at least one candidate passes the transition policy. Under an open transition
// (CANDIDATE or UNRESOLVED) a set needs one; with a confirmed or rejected transition or none, every candidate is allowed.
func hasAllowed(transitionStatus string, cs []workerclient.Candidate) bool {
	if !openTransition[transitionStatus] {
		return len(cs) > 0
	}
	return slices.ContainsFunc(cs, func(c workerclient.Candidate) bool { return !CandidatePolicy(transitionStatus, c).Restricted() })
}

func distinctPeople(c workerclient.Candidate) []string {
	var ids []string
	for _, r := range slices.Concat(c.To, c.CC) {
		if !slices.Contains(ids, r.PersonID) {
			ids = append(ids, r.PersonID)
		}
	}
	return ids
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// effectiveActionClass is the class a candidate is judged and routed as: EXPANSION_MOTION when its label says so
// or, for an external action, when the text of its message is an expansion CTA; otherwise its own label.
func effectiveActionClass(c workerclient.Candidate) string {
	if c.ActionClass != "EXPANSION_MOTION" && external[c.ActionType] && expansionCTA.MatchString(candidateText(c)) {
		return "EXPANSION_MOTION"
	}
	return c.ActionClass
}
