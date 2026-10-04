package abcrun

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/harneet2512/gtm-work/core-go/internal/claims"
)

// TruthModel names the ground-truth extractor in the claims it stores.
const TruthModel = "abc-truth-extractor"

// TruthExtractor is a claims.Extractor that answers from a table instead of a model: for each customer email of the
// pack, the planted facts a perfect extractor would report. It is the one place the experiment replaces the
// worker's extraction, so the state every arm sees is the generator's ground truth and extraction error is not part
// of what is measured (the noisy-mode caveat of the synthetic layer says what that leaves out). Emails the table
// does not name yield no claims.
type TruthExtractor struct {
	// Claims maps a source_object_id (the email's message id) to its candidates.
	Claims map[string][]claims.Candidate
}

// Extract implements claims.Extractor.
func (t TruthExtractor) Extract(_ context.Context, req claims.ExtractRequest) (claims.ExtractResponse, error) {
	return claims.ExtractResponse{Claims: t.Claims[req.Activity.SourceObjectID], Model: TruthModel, ExtractorVersion: req.ExtractorVersion}, nil
}

// The three objection templates of the generator's customer replies (the wording the facts are read from).
var (
	pricingObjection     = regexp.MustCompile(`the pricing for .+? is higher than what we had planned for this year`)
	integrationObjection = regexp.MustCompile(`we need to understand how it would fit alongside the tools we already rely on`)
	securityObjection    = regexp.MustCompile(`security and compliance`)
)

// TruthFromBody reads the planted facts out of a rendered customer email: a pricing objection becomes an open
// objection, an integration concern an open objection, a security and compliance concern an open blocker. The
// quote is a verbatim fragment of the body, as every claim's quote must be.
func TruthFromBody(body string) []claims.Candidate {
	var out []claims.Candidate
	add := func(field claims.FieldPath, text string, re *regexp.Regexp) {
		if q := re.FindString(body); q != "" {
			v, _ := json.Marshal(text)
			out = append(out, claims.Candidate{FieldPath: field, Value: v, Confidence: 0.95, EvidenceQuote: q})
		}
	}
	add(claims.FieldObjections, "Pricing is higher than planned for this year and hard to justify without a clearer picture of the return", pricingObjection)
	add(claims.FieldObjections, "Concerned about fit with the tools already in use and the effort of moving existing work", integrationObjection)
	add(claims.FieldBlockers, "Security and compliance questions need written answers", securityObjection)
	return out
}
