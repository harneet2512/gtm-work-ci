package demoboundary

// AskAgent is how Cliff behaves as an agent (owner request, 2026-10-06). None of it widens what Cliff may do.
type AskAgent struct {
	Memory struct {
		VerbatimTurns int    `json:"verbatim_turns"`
		OlderTurns    string `json:"older_turns"`
		StoredIn      string `json:"stored_in"`
	} `json:"memory"`
	MaxToolCalls               int    `json:"max_tool_calls"`
	DeadlineS                  int    `json:"deadline_s"`
	Progress                   string `json:"progress"`
	ConfirmationPerStateChange bool   `json:"confirmation_per_state_change"`
	ContinuesAfterConfirmation bool   `json:"continues_after_confirmation"`
	Trace                      string `json:"trace"`
}

// problems lists every way the agent section differs from what the owner approved.
func (g AskAgent) problems() []string {
	var p []string
	if g.Memory.VerbatimTurns != 8 || g.Memory.OlderTurns != "summarised" || g.Memory.StoredIn != "core" {
		p = append(p, "Ask Cliff's memory is the last 8 turns verbatim and older turns summarised, stored in core")
	}
	if g.MaxToolCalls != 12 || g.DeadlineS != 120 {
		p = append(p, "Ask Cliff may use at most 12 tool calls in 120 seconds")
	}
	if g.Progress != "updates_the_thinking_message_in_place" {
		p = append(p, "Ask Cliff shows progress by updating its thinking message in place, never by a new channel message")
	}
	if !g.ConfirmationPerStateChange || !g.ContinuesAfterConfirmation {
		p = append(p, "every state-changing step needs its own confirmation and the task continues after it")
	}
	if g.Trace != "every_answer_links_its_trace" {
		p = append(p, "every Ask Cliff answer must link its trace")
	}
	return p
}
