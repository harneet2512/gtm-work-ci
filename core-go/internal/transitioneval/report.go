package transitioneval

import "github.com/harneet2512/gtm-work/core-go/internal/transitions"

// Report is the committed measurement (bench/reports/transitions-gold-<date>.json).
type Report struct {
	ReportVersion int      `json:"report_version"`
	Date          string   `json:"date"`
	RuleSet       string   `json:"rule_set_version"`
	GoldFiles     []string `json:"gold_files"`
	DataBasis     string   `json:"data_basis"`
	Caveat        string   `json:"caveat"`
	// Headline scores every uncontested step of both gold sets. Authored is the set written by the engineer who
	// wrote the detector; Blind was written by a separate author from the product spec alone. Contested steps
	// (the spec can be read two ways) are scored apart and left out of the headline.
	Headline  Metrics            `json:"headline_uncontested"`
	Authored  Metrics            `json:"authored_uncontested"`
	Blind     Metrics            `json:"blind_uncontested"`
	Contested Metrics            `json:"contested_only"`
	PerSlice  map[string]Metrics `json:"per_slice"`
	Support   SupportReport      `json:"state_transition_support_check"`
	Command   string             `json:"command"`
}

// DataBasis and Caveat say plainly what this measurement is.
const (
	DataBasis = "spec_derived_transition_gold"
	Caveat    = "Spec-derived gold on synthetic account states (no company or person names), labelled from the HAR-97 pitch before the " +
		"detector ran: one set by the detector's author, one by a separate author who saw only the pitch. It measures whether the detector " +
		"reproduces the product spec on the cases we could think of; it is not evidence about real accounts. The independent gold the " +
		"metrics registry names (HAR-130 CRMArena deals plus the HAR-131 labelled synthetic layer) does not exist on main: CRMArena has no " +
		"people changes and HAR-131 is an open PR."
)

func only(origin string, contested bool) func(StepResult) bool {
	return func(r StepResult) bool { return r.Contested == contested && (origin == "" || r.Origin == origin) }
}

// Build scores every step: the headline, each author's uncontested set, the contested steps and each slice.
func Build(rules transitions.RuleSet, gold Gold, goldFiles []string, date, command string) (Report, error) {
	results, err := Run(rules, gold)
	if err != nil {
		return Report{}, err
	}
	rep := Report{ReportVersion: 1, Date: date, RuleSet: rules.Version, GoldFiles: goldFiles, DataBasis: DataBasis, Caveat: Caveat, Command: command,
		Headline: Compute(results, only("", false)), Authored: Compute(results, only(OriginAuthored, false)),
		Blind: Compute(results, only(OriginBlind, false)), Contested: Compute(results, func(r StepResult) bool { return r.Contested }),
		PerSlice: map[string]Metrics{}, Support: EvaluateSupport(rules, results)}
	for _, r := range results {
		if _, done := rep.PerSlice[r.Slice]; done {
			continue
		}
		slice := r.Slice
		rep.PerSlice[slice] = Compute(results, func(x StepResult) bool { return x.Slice == slice && !x.Contested })
	}
	return rep, nil
}
