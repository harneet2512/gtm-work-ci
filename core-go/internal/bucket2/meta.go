package bucket2

// GateMeta is a gate's question and what it protects, as contracts/evals/eval_registry.json states them (a test
// keeps the two in step).
type GateMeta struct {
	Name, Question, Improves string
}

// Gates lists D1 to D10 in flow order.
var Gates = []string{"D1", "D2", "D3", "D4", "D5", "D6", "D7", "D8", "D9", "D10"}

var gateMeta = map[string]GateMeta{
	"D1":  {"Intent and action-policy fit", "Given the current state, what class of reaction is appropriate: act, wait, ask, escalate or do nothing?", "Evaluates the decision before the wording of an email."},
	"D2":  {"Three-candidate action quality", "Are the three complete actions each a plausible execution of a defensible strategy, and do they differ in substance?", "Makes the person's choice a real judgment among meaningful options."},
	"D3":  {"Ranking and recommendation quality", "Did gtm_ai prefer the most defensible option for this state?", "Separates generating options from having good judgment about which to recommend."},
	"D4":  {"Human selection interpretation", "What does the person's choice tell us about gtm_ai's decision?", "Turns a click into structured supervision without treating the person as automatically right."},
	"D5":  {"Human edit interpretation", "What meaning is contained in the person's edit of the chosen action?", "Turns text differences into semantic evidence, so a copy edit is not read as a strategic correction."},
	"D6":  {"Eval-gap detection", "Did the existing checks already identify the reason for the person's correction?", "Lets real use improve the eval system itself, not only produce another feedback row."},
	"D7":  {"Override propagation and selective re-evaluation", "After the person changed something, did everything that depends on it recompute correctly?", "Makes a correction change the current workflow, not only future memory."},
	"D8":  {"Final-artifact correctness", "Is the exact message about to be sent correct after all edits?", "Prevents an evaluated draft from becoming an unevaluated edited send."},
	"D9":  {"Execution and workflow completion", "Did gtm_ai carry out the final approved intent correctly in the real environment?", "Verifies the job was done, not merely that gtm_ai said it was."},
	"D10": {"Feedback emission back to Bucket 1", "What new evidence did this decision and action produce for organizational intelligence?", "Closes the loop: what the person and the customer do becomes the next context."},
}

// Meta returns the gate's metadata (zero value for an unknown gate).
func Meta(gate string) GateMeta { return gateMeta[gate] }
