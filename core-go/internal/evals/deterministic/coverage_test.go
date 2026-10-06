package deterministic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// traceRow ties one HAR-97 line marked for HAR-115 to the code and the test that cover it.
type traceRow struct {
	Line     string // the line without its acknowledgement marker
	Function string
	Test     string
	Check    Check // sub-check id; empty for an eval heading
	Eval     EvalType
}

const ackMarker = " — ✅ acknowledged (Claude Code · HAR-115)"

var traceRows = []traceRow{
	{"Recipient correctness", "RecipientCorrectness", "TestRecipientCorrectnessResult", "", TypeRecipient},
	{"recipients exist", "RecipientsExist", "TestRecipientsExist", "recipient.exists", TypeRecipient},
	{"recipient belongs to intended account", "RecipientsBelongToAccount", "TestRecipientsBelongToAccount", "recipient.belongs_to_account", TypeRecipient},
	{"no duplicate recipients", "RecipientsNotDuplicated", "TestRecipientsNotDuplicated", "recipient.no_duplicates", TypeRecipient},
	{"no internal-only contact accidentally externalized", "InternalOnlyNotExternalized", "TestInternalOnlyNotExternalized", "recipient.internal_only_not_externalized", TypeRecipient},
	{"Date / commitment consistency", "DateCommitmentConsistency", "TestDateCommitmentConsistencyResult", "", TypeDate},
	{"dates match current state", "DatesMatchState", "TestDatesMatchState", "date.matches_current_state", TypeDate},
	{"meeting time is not stale", "MeetingTimeNotStale", "TestMeetingTimeNotStale", "date.meeting_not_stale", TypeDate},
	{"action does not contradict a commitment already made", "CommitmentNotContradicted", "TestCommitmentNotContradicted", "date.commitment_not_contradicted", TypeDate},
	{"promised asset exists if referenced", "PromisedAssetExists", "TestPromisedAssetExists", "date.promised_asset_exists", TypeDate},
	{"Number / product / pricing integrity", "PricingIntegrity", "TestPricingIntegrityResult", "", TypePricing},
	{"no unsupported price", "PriceSupported", "TestPriceSupported", "pricing.price_supported", TypePricing},
	{"no invented discount", "DiscountNotInvented", "TestDiscountNotInvented", "pricing.discount_not_invented", TypePricing},
	{"correct product / plan / region", "ProductPlanRegionCorrect", "TestProductPlanRegionCorrect", "pricing.product_plan_region", TypePricing},
	{"quantities match source evidence", "QuantitiesMatchEvidence", "TestQuantitiesMatchEvidence", "pricing.quantities_match_evidence", TypePricing},
	{"CRM/writeback consistency", "CRMWriteback", "TestCRMWritebackResult", "", TypeCRM},
	{"stage update is legal", "StageUpdateLegal", "TestStageUpdateLegal", "crm.stage_update_legal", TypeCRM},
	{"owner exists", "OwnerExists", "TestOwnerExists", "crm.owner_exists", TypeCRM},
	{"next step does not overwrite newer state", "NextStepDoesNotOverwrite", "TestNextStepDoesNotOverwrite", "crm.next_step_no_overwrite", TypeCRM},
	{"writeback references correct opportunity/account", "WritebackTargetCorrect", "TestWritebackTargetCorrect", "crm.writeback_target", TypeCRM},
	{"Duplicate-action detection", "DuplicateAction", "TestDuplicateActionResult", "", TypeDuplicate},
	{"same email was not already sent", "EmailNotAlreadySent", "TestEmailNotAlreadySent", "duplicate.email_not_already_sent", TypeDuplicate},
	{"meeting was not already scheduled", "MeetingNotAlreadyScheduled", "TestMeetingNotAlreadyScheduled", "duplicate.meeting_not_already_scheduled", TypeDuplicate},
	{"CRM action was not already completed", "CRMActionNotAlreadyCompleted", "TestCRMActionNotAlreadyCompleted", "duplicate.crm_action_not_completed", TypeDuplicate},
	{"Provenance / source coverage", "CriticalStatementsSupported", "TestCriticalStatementsSupported", "provenance.critical_statements_supported", TypeProvenance},
	{"Permission / tool policy", "PermissionPolicy", "TestPermissionPolicyResult", "", TypePermission},
	{"correct allowed tool", "ToolAllowed", "TestToolAllowed", "permission.tool_allowed", TypePermission},
	{"correct account/workspace", "AccountWorkspaceCorrect", "TestAccountWorkspaceCorrect", "permission.account_workspace", TypePermission},
	{"action is within permitted autonomy level", "WithinAutonomy", "TestWithinAutonomy", "permission.autonomy_level", TypePermission},
	{"test-mode runs cannot send or write", "DryRunCannotWrite", "TestDryRunCannotWrite", "permission.dry_run_cannot_write", TypePermission},
}

func repoPath(rel ...string) string {
	return filepath.Join(append([]string{"..", "..", "..", ".."}, rel...)...)
}

func snapshotLines(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(repoPath("docs", "traceability", "wp17_har97_lines.txt"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if !strings.HasSuffix(l, ackMarker) {
			t.Fatalf("snapshot line without the acknowledgement marker: %q", l)
		}
		l = strings.TrimSuffix(l, ackMarker)
		out[strings.TrimPrefix(strings.TrimPrefix(l, "* "), "## ")] = true
	}
	return out
}

// declared lists the top-level function names of the package's files matching glob.
func declared(t *testing.T, glob string, wantTests bool) map[string]bool {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no files for %s: %v", glob, err)
	}
	out := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") != wantTests {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				out[fn.Name.Name] = true
			}
		}
	}
	return out
}

func TestEveryAcknowledgedHAR97LineIsCovered(t *testing.T) {
	snap := snapshotLines(t)
	if len(snap) != 30 {
		t.Fatalf("snapshot has %d lines, want the 30 lines marked for HAR-115", len(snap))
	}
	covered := map[string]bool{}
	for _, r := range traceRows {
		covered[r.Line] = true
		if !snap[r.Line] {
			t.Errorf("trace row %q is not a line of the snapshot", r.Line)
		}
	}
	for l := range snap {
		if !covered[l] {
			t.Errorf("HAR-97 line %q has no covering function and test", l)
		}
	}
	if len(traceRows) != len(snap) {
		t.Errorf("%d trace rows for %d lines", len(traceRows), len(snap))
	}
}

func TestTraceRowsPointAtRealFunctionsTestsAndChecks(t *testing.T) {
	funcs, tests := declared(t, "*.go", false), declared(t, "*.go", true)
	judged := map[EvalType][]Check{}
	for _, j := range Evaluate(baseInput()) {
		judged[j.Result.EvalType] = j.Checks
	}
	for _, r := range traceRows {
		if !funcs[r.Function] {
			t.Errorf("%q: function %s does not exist", r.Line, r.Function)
		}
		if !tests[r.Test] {
			t.Errorf("%q: test %s does not exist", r.Line, r.Test)
		}
		if r.Check == "" {
			continue
		}
		found := false
		for _, c := range judged[r.Eval] {
			found = found || c == r.Check
		}
		if !found {
			t.Errorf("%q: check %s is not reported by the %s eval", r.Line, r.Check, r.Eval)
		}
	}
}

func TestTraceabilityDocListsEveryLine(t *testing.T) {
	raw, err := os.ReadFile(repoPath("docs", "traceability", "wp17.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, r := range traceRows {
		for _, want := range []string{r.Line, r.Function, r.Test} {
			if !strings.Contains(doc, want) {
				t.Errorf("docs/traceability/wp17.md does not mention %q (row %q)", want, r.Line)
			}
		}
	}
}
