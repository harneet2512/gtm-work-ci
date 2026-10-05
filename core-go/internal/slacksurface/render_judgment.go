package slacksurface

import (
	"fmt"
	"strings"

	"github.com/slack-go/slack"
)

// RenderJudgment renders Message 3: what Ghost preferred, what the human chose, Ghost's inference of
// the reason and its evidence, then Correct / Needs correction / Add note. After a verdict the same
// message is re-rendered with the outcome. runID is carried in the button values so a later click
// can read the run's strategies; with a web URL every state links to the chosen option's evals.
func RenderJudgment(inf JudgmentInference, rs RunStrategies, runID, web string) Message {
	id := "ghost.judgment"
	pref, chosen := rs.Title(inf.AgentPreference), rs.Title(inf.HumanChoice)
	blocks := []slack.Block{headerBlock(id+".header", "Human judgment")}
	if inf.Agreement == AgreementAgreed {
		blocks = append(blocks, section(id+".choice",
			"*You chose Ghost's preference*\n"+mrkdwn(chosen)+"\n_Your choice matches Ghost's preference; this confirms rather than contradicts it._"))
	} else {
		blocks = append(blocks, section(id+".choice",
			"*Ghost originally preferred*\n"+mrkdwn(pref)+"\n\n*You chose*\n"+mrkdwn(chosen)))
	}
	blocks = append(blocks, section(id+".inference", "*Ghost's inference*\n"+mrkdwn(truncate(inf.InferredSemanticDelta.Statement, 1500))))
	blocks = append(blocks, textSections(id+".evidence", "Why Ghost inferred this", bullets(evidenceBullets(inf.Evidence, rs), maxEvidence+6), 2)...)
	evals := linkButton(ActionJudgmentViewEvals, "View evals", EvalsURL(web, runID, inf.HumanChoice))
	blocks = append(blocks, verdictBlocks(id, inf, Target{EpisodeID: inf.DecisionEpisodeID, RunID: runID}, evals)...)
	return Message{Text: fmt.Sprintf("Human judgment: Ghost preferred %s, you chose %s", pref, chosen), Blocks: blocks}
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
		line := fmt.Sprintf("%s: Ghost's pick %s %s, your choice %s %s", mrkdwn(evalName(d.EvalType)),
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
	note := func() []slack.Block {
		if inf.HumanNote == nil || strings.TrimSpace(*inf.HumanNote) == "" {
			return nil
		}
		return []slack.Block{contextBlock(id+".note", "*Note:* "+mrkdwn(truncate(*inf.HumanNote, 1000)))}
	}
	addNote := actions(id+".actions", withLink([]slack.BlockElement{button(ActionJudgmentNote, "Add note", t)}, evals)...)
	switch inf.HumanVerdict {
	case VerdictConfirmed:
		out := []slack.Block{section(id+".verdict", "*Confirmed:* Ghost understood this judgment.")}
		return append(append(out, note()...), addNote)
	case VerdictCorrected:
		corrected := ""
		if inf.CorrectedStatement != nil {
			corrected = *inf.CorrectedStatement
		}
		out := []slack.Block{section(id+".verdict", "*Corrected:* "+mrkdwn(truncate(corrected, 1000)))}
		return append(append(out, note()...), addNote)
	}
	return []slack.Block{
		section(id+".ask", "*Did Ghost understand your judgment?*"),
		actions(id+".actions", withLink([]slack.BlockElement{
			button(ActionJudgmentConfirm, "Correct", t).WithStyle(slack.StylePrimary),
			button(ActionJudgmentCorrect, "Needs correction", t),
			button(ActionJudgmentNote, "Add note", t),
		}, evals)...),
	}
}
