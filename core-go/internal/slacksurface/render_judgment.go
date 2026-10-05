package slacksurface

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// RenderJudgment renders Message 3, Cliff's learning interpretation: "I noticed you changed <semantic thing>. My
// interpretation: <scoped proposal>", why I think so, and the question "Should I learn this?" with Correct, Edit
// interpretation and Don't learn this. After an answer the same message is re-rendered with the outcome and no
// buttons but the link. runID is carried in the button values so a later click can read the run's strategies;
// with a web URL every state links to the chosen option's evals.
func RenderJudgment(inf JudgmentInference, rs RunStrategies, runID, web string) Message {
	id := "ghost.judgment"
	pref, chosen := rs.Title(inf.AgentPreference), rs.Title(inf.HumanChoice)
	blocks := []slack.Block{
		headerBlock(id+".header", "What I noticed"),
		section(id+".choice", noticed(inf, pref, chosen)+"\n"+interpretation(inf)),
	}
	blocks = append(blocks, textSections(id+".evidence", "Why I think so", bullets(evidenceBullets(inf.Evidence, rs), maxEvidence+6), 2)...)
	evals := linkButton(ActionJudgmentViewEvals, "View evals", EvalsURL(web, runID, inf.HumanChoice))
	blocks = append(blocks, verdictBlocks(id, inf, Target{EpisodeID: inf.DecisionEpisodeID, RunID: runID}, evals)...)
	return Message{Text: fmt.Sprintf("What I noticed: you chose %s over %s", chosen, pref), Blocks: blocks}
}

// noticed is the first sentence: what the human changed, named from the semantic labels, else from the two options.
func noticed(inf JudgmentInference, pref, chosen string) string {
	if inf.Agreement == AgreementAgreed {
		return "I noticed you went with my recommendation, " + mrkdwn(chosen) + "."
	}
	if len(inf.InferredSemanticDelta.SemanticLabels) > 0 {
		return "I noticed you changed " + mrkdwn(changedThing(inf.InferredSemanticDelta.SemanticLabels)) + "."
	}
	return "I noticed you changed my recommendation, " + mrkdwn(pref) + ", to " + mrkdwn(chosen) + "."
}

// interpretation is the second sentence: the scoped proposal, which is exactly what Correct confirms and Edit
// interpretation lets the human rewrite.
func interpretation(inf JudgmentInference) string {
	return "My interpretation: " + mrkdwn(truncate(inf.InferredSemanticDelta.Statement, 1500))
}

// labelWords is the plain wording of the shared semantic-label vocabulary (human_delta.v1.json), as the object
// of "I noticed you changed ...". A test keeps it complete.
var labelWords = map[string]string{
	"reduced_pressure":                 "the message to put less pressure on the buyer",
	"increased_pressure":               "the message to put more pressure on the buyer",
	"kept_champion_involved":           "the recipients to keep the champion involved",
	"removed_unnecessary_stakeholders": "the recipients to drop people who did not need it",
	"added_missing_stakeholder":        "the recipients to add a missing stakeholder",
	"delayed_cta":                      "the timing of the ask",
	"removed_cta":                      "the message to remove the ask",
	"smaller_ask":                      "the ask to a smaller one",
	"larger_ask":                       "the ask to a larger one",
	"changed_channel":                  "the channel",
	"corrected_fact":                   "a fact in the message",
	"deferred_to_buyer_timing":         "the timing to follow the buyer's schedule",
	"style_only":                       "the style only, not the substance",
}

// changedThing joins the labels' plain words ("a, b and c"); a label the table lacks is humanized, never shown raw.
func changedThing(labels []string) string {
	words := make([]string, 0, len(labels))
	for _, l := range labels {
		w, ok := labelWords[l]
		if !ok {
			w = humanize(l)
		}
		words = append(words, w)
	}
	switch len(words) {
	case 0:
		return "something"
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// evidenceBullets: candidate differences, account-state evidence, eval differences, knowledge.
func evidenceBullets(ev InferenceEvidence, rs RunStrategies) []string {
	var out []string
	for _, d := range ev.CandidateDifferences {
		out = append(out, mrkdwn(d))
	}
	for _, e := range evidenceLines(ev.EvidenceRefs) {
		out = append(out, "State: "+e)
	}
	for _, d := range ev.EvalDifferences {
		line := fmt.Sprintf("%s: my pick %s %s, your choice %s %s", mrkdwn(evalName(d.EvalType)),
			verdictEmoji(d.AgentPreferenceVerdict), verdictLabel(d.AgentPreferenceVerdict), verdictEmoji(d.HumanChoiceVerdict), verdictLabel(d.HumanChoiceVerdict))
		if d.Note != "" {
			line += " (" + mrkdwn(truncate(d.Note, 200)) + ")"
		}
		out = append(out, line)
	}
	if ev.NoApplicableKnowledge || len(ev.KnowledgeRefs) == 0 {
		out = append(out, "No applicable company knowledge")
	} else {
		out = append(out, "Knowledge applied: "+knowledgeLine(ev.KnowledgeRefs))
	}
	return out
}

func verdictBlocks(id string, inf JudgmentInference, t Target, evals *slack.ButtonBlockElement) []slack.Block {
	var outcome string
	switch inf.HumanVerdict {
	case VerdictConfirmed:
		outcome = "*Confirmed:* I read your judgment correctly."
	case VerdictCorrected:
		corrected := ""
		if inf.CorrectedStatement != nil {
			corrected = *inf.CorrectedStatement
		}
		outcome = "*Corrected:* " + mrkdwn(truncate(corrected, 1000))
	case VerdictNoLearning:
		outcome = "*Not learning from this.* I won't use this decision to form knowledge."
	default:
		return []slack.Block{
			section(id+".ask", "*Should I learn this?*"),
			actions(id+".actions", withLink([]slack.BlockElement{
				button(ActionJudgmentConfirm, "Correct", t).WithStyle(slack.StylePrimary),
				button(ActionJudgmentCorrect, "Edit interpretation", t),
				button(ActionJudgmentNoLearn, "Don't learn this", t),
			}, evals)...),
		}
	}
	out := []slack.Block{section(id+".verdict", outcome)}
	if inf.HumanNote != nil && strings.TrimSpace(*inf.HumanNote) != "" {
		out = append(out, contextBlock(id+".note", "*Note:* "+mrkdwn(truncate(*inf.HumanNote, 1000))))
	}
	if evals != nil {
		out = append(out, actions(id+".actions", evals))
	}
	return out
}
