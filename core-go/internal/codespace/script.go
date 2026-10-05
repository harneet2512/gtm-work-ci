package codespace

import (
	"context"
	"errors"
	"sort"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// Script is the human path the record run plays through Cliff's Slack handlers (HAR-129 demo loop): the choice, the edit
// and the correction. The presenter types exactly this on demo day (RUNSHEET.txt lists it); whatever a human does
// differently costs one recorded call, once.
type Script struct {
	// ChoiceRank is the candidate the human picks by Ghost's ranking (1 = Ghost's best). 2 disagrees with Ghost.
	ChoiceRank int
	// EditSubjectSuffix and EditBodyAppend are what the human adds to the subject and the body in the Edit email modal.
	EditSubjectSuffix string
	EditBodyAppend    string
	// Correction is what the human types into Edit interpretation on Message 3. Note is the optional note beside it
	// (empty: the field is left blank).
	Correction string
	Note       string
	// UserID and UserName are the Slack member the fake transport acts as.
	UserID, UserName string
}

// DefaultScript disagrees with Ghost on the choice, edits subject and body, and corrects the inference.
func DefaultScript() Script {
	return Script{ChoiceRank: 2, EditSubjectSuffix: " (confirm timing)",
		EditBodyAppend: "\n\nCould we confirm the next step and a date this week?",
		Correction:     "I chose this one because it keeps the champion in the loop; the stated reason misses that.",
		UserID:         "U0DEMOPRESENTER", UserName: "presenter"}
}

// HumanPath is what the human did for one case, exactly as typed: the run sheet is made from it.
type HumanPath struct {
	Case string
	// ChoiceButton is the label of the button pressed on Message 2 ("Select B"); ChoiceTitle the strategy's title.
	ChoiceButton string
	ChoiceTitle  string
	ChoiceRank   int
	// To and CC are the recipients as the Edit email modal shows them (left as they were).
	To, CC string
	// Subject and Body are the full text of the edited email.
	Subject, Body string
	// Interpretation is what Cliff's Message 3 said before the correction; Correction and Note are what was typed.
	Interpretation string
	Correction     string
	Note           string
}

// Human plays the human's whole path for one played case.
type Human interface {
	Act(ctx context.Context, c Case, st demorun.DemoState, s Script) (HumanPath, error)
}

// Player plays Event N of a case through the real pipeline and waits for the strategies (demorun.RunPlay).
type Player interface {
	Play(ctx context.Context, st demorun.DemoState) (demorun.DemoState, error)
}

// pickCandidate is the candidate of the given Ghost ranking (the best when the rank does not exist), and its position
// in the strategy set, which is the letter of its Select button.
func pickCandidate(cands []slacksurface.StrategyCandidate, rank int) (slacksurface.StrategyCandidate, int, error) {
	if len(cands) == 0 {
		return slacksurface.StrategyCandidate{}, 0, errors.New("the run published no candidates")
	}
	sorted := append([]slacksurface.StrategyCandidate(nil), cands...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ranking < sorted[j].Ranking })
	pick := sorted[0]
	if rank >= 1 && rank <= len(sorted) {
		pick = sorted[rank-1]
	}
	for i, c := range cands {
		if c.CandidateID == pick.CandidateID {
			return pick, i, nil
		}
	}
	return pick, 0, nil
}
