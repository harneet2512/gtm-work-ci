package slacksurface

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/slack-go/slack"
)

// Display caps keep every message inside Slack's block and text limits however much core returns.
const (
	maxChanges       = 10
	maxEvidence      = 10
	maxClaimEvidence = 3
	maxEvalSections  = 6
	rationaleChars   = 500
	reasonChars      = 300
	previewChars     = 450
	moreInWeb        = "more in the web view"
)

// mrkdwn escapes the three characters Slack treats as control characters, so user-controlled text
// cannot form links, mentions or channel broadcasts.
func mrkdwn(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func plain(s string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.PlainTextType, s, false, false)
}

func md(s string) *slack.TextBlockObject {
	// Verbatim stops Slack auto-linking URLs and parsing @here/@channel/#channel in text that
	// comes from drafts, CRM records or people.
	return slack.NewTextBlockObject(slack.MarkdownType, s, false, true)
}

func headerBlock(blockID, s string) slack.Block {
	b := slack.NewHeaderBlock(plain(truncate(s, maxHeaderText)))
	b.BlockID = blockID
	return b
}

func section(blockID, text string) slack.Block {
	b := slack.NewSectionBlock(md(truncate(text, maxSectionText)), nil, nil)
	b.BlockID = blockID
	return b
}

func fieldsSection(blockID string, pairs [][2]string) slack.Block {
	fields := make([]*slack.TextBlockObject, 0, len(pairs))
	for _, p := range pairs {
		fields = append(fields, md(truncate("*"+p[0]+"*\n"+mrkdwn(orDash(p[1])), maxFieldText)))
	}
	b := slack.NewSectionBlock(nil, fields, nil)
	b.BlockID = blockID
	return b
}

func contextBlock(blockID, text string) slack.Block {
	return slack.NewContextBlock(blockID, md(truncate(text, maxSectionText)))
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func button(actionID, text string, t Target) *slack.ButtonBlockElement {
	return slack.NewButtonBlockElement(actionID, t.Encode(), plain(truncate(text, maxButtonText)))
}

func actions(blockID string, els ...slack.BlockElement) slack.Block {
	return slack.NewActionBlock(blockID, els...)
}

// bullets renders lines as "• line", capped, with a pointer to the web view when cut.
func bullets(lines []string, max int) string {
	var sb strings.Builder
	for i, l := range lines {
		if i == max {
			fmt.Fprintf(&sb, "_+%d %s_", len(lines)-max, moreInWeb)
			break
		}
		sb.WriteString("• " + l + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// textSections renders a titled body as one or more sections, splitting long text.
func textSections(blockID, title, body string, maxSections int) []slack.Block {
	chunks := splitText(body, textChunk)
	var out []slack.Block
	for i, c := range chunks {
		if i == maxSections {
			out = append(out, contextBlock(fmt.Sprintf("%s.more", blockID), "_"+moreInWeb+"_"))
			break
		}
		text := c
		if i == 0 && title != "" {
			text = "*" + title + "*\n" + c
		}
		id := blockID
		if i > 0 {
			id = fmt.Sprintf("%s.%d", blockID, i)
		}
		out = append(out, section(id, text))
	}
	return out
}

// evidenceLine renders one evidence ref: the quote and when it was said.
func evidenceLine(r EvidenceRef) string {
	q := strings.TrimSpace(r.Quote)
	if q == "" {
		q = "a recorded activity (no quote available)"
	}
	line := "“" + mrkdwn(truncate(q, 180)) + "”"
	if !r.OccurredAt.IsZero() {
		line += " (" + r.OccurredAt.UTC().Format("2006-01-02 15:04 MST") + ")"
	}
	return line
}

func evidenceLines(refs []EvidenceRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, evidenceLine(r))
	}
	return out
}

func shortID(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

// knowledgeLine names the knowledge applied. The contract carries ids only and an id means nothing to a
// reader, so the line says how many items were applied; titles need a core read that does not exist yet.
func knowledgeLine(refs []string) string {
	switch len(refs) {
	case 0:
		return ""
	case 1:
		return "1 knowledge item"
	}
	return fmt.Sprintf("%d knowledge items", len(refs))
}

// fieldWords turns a state field name like "buying_group" into the words "buying group".
func fieldWords(names []string) string {
	words := make([]string, 0, len(names))
	for _, n := range names {
		words = append(words, strings.NewReplacer("_", " ", ".", " ").Replace(n))
	}
	return strings.Join(words, ", ")
}

// humanize turns an eval_type like "cta_calibration" into "Cta calibration".
func humanize(snake string) string {
	s := strings.ReplaceAll(snake, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func joinAddrs(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return strings.Join(a, ", ")
}

// ArtView is what the human currently sees as the email: display recipients, subject and body.
type ArtView struct {
	To, CC  []string
	Subject string
	Body    string
}

// display renders a recipient as "Name <email>", "Name" or a short id when the person is unknown.
func (d Directory) display(r Recipient) string {
	p, ok := d[r.PersonID]
	switch {
	case !ok:
		return "a person not on this account"
	case p.Email != "" && p.Name != "":
		return p.Name + " <" + p.Email + ">"
	case p.Email != "":
		return p.Email
	}
	return p.Name
}

func (d Directory) displayAll(rs []Recipient) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, d.display(r))
	}
	return out
}

// viewOf is the artifact the human currently sees: the final one once a decision selects this
// candidate and has one, else the candidate's own.
func viewOf(c StrategyCandidate, dec *HumanStrategyDecision, dir Directory) ArtView {
	to, cc, art := c.To, c.CC, c.FullActionArtifact
	if dec != nil && dec.SelectedCandidateID == c.CandidateID {
		if dec.FinalTo != nil {
			to = dec.FinalTo
		}
		if dec.FinalCC != nil {
			cc = dec.FinalCC
		}
		if dec.FinalArtifact != nil {
			art = *dec.FinalArtifact
		}
	}
	return ArtView{To: dir.displayAll(to), CC: dir.displayAll(cc), Subject: art.SubjectText(), Body: art.Body}
}

func artifactFields(blockID string, a ArtView) slack.Block {
	return fieldsSection(blockID, [][2]string{
		{"To", joinAddrs(a.To)},
		{"CC", joinAddrs(a.CC)},
		{"Subject", a.Subject},
	})
}

// letter labels candidates A, B, C... by position.
func letter(i int) string { return string(rune('A' + i%26)) }

// safeURL returns u only when it is an absolute http(s) URL.
func safeURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" || len(u) > maxURL {
		return ""
	}
	return u
}
