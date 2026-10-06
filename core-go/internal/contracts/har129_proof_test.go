package contracts

import (
	"path/filepath"
	"regexp"
	"testing"
)

// The HAR-129 confirmation matrix is generated from one requirement source of truth
// (contracts/har129/requirements.v1.json). These tests pin that source: it must conform to its
// schema and it must contain every listed live proof check, with no duplicate ids and no
// requirement pointing at a section that does not exist.

const (
	har129RequirementsPath = "har129/requirements.v1.json"
	har129GChecks          = 26  // HAR-129 §G bullets, quoted verbatim
	har129RequirementsAll  = 157 // G 26 + §16 36 + §13 9 + DoD 14 + checklist 15 + §A-F/§H/§I 26 + §5-§12 23 + boundary 8
)

func har129Requirements(t *testing.T) map[string]any {
	t.Helper()
	root := contractsDir(t)
	instance := readJSON(t, filepath.Join(root, har129RequirementsPath))
	if err := schemaFor(t, compiler(t, root), "har129_requirements").Validate(instance); err != nil {
		t.Fatalf("%s does not conform to har129_requirements: %v", har129RequirementsPath, err)
	}
	return instance.(map[string]any)
}

func TestHar129RequirementsSourceIsComplete(t *testing.T) {
	doc := har129Requirements(t)

	sectionIDs := map[string]bool{}
	for _, s := range doc["sections"].([]any) {
		id, _ := s.(map[string]any)["id"].(string)
		if sectionIDs[id] {
			t.Errorf("duplicate section id %q", id)
		}
		sectionIDs[id] = true
	}

	byID := map[string]bool{}
	perSection := map[string]int{}
	idPattern := regexp.MustCompile(`^HAR129-[A-Z0-9]+-[0-9]{2}$`)
	for _, r := range doc["requirements"].([]any) {
		m := r.(map[string]any)
		id := m["id"].(string)
		if !idPattern.MatchString(id) {
			t.Errorf("requirement id %q does not match HAR129-<SECTION>-<NN>", id)
		}
		if byID[id] {
			t.Errorf("duplicate requirement id %q", id)
		}
		byID[id] = true
		section := m["section"].(string)
		if !sectionIDs[section] {
			t.Errorf("%s names unknown section %q", id, section)
		}
		perSection[section]++
		if m["requirement"].(string) == "" || m["required_behavior"].(string) == "" || m["reference"].(string) == "" {
			t.Errorf("%s must quote the requirement and state the required behavior and reference", id)
		}
	}

	var total int
	for _, n := range perSection {
		total += n
	}
	if total != har129RequirementsAll {
		t.Errorf("requirement source has %d rows, want %d", total, har129RequirementsAll)
	}
	if perSection["G"] != har129GChecks {
		t.Errorf("HAR-129 §G has %d rows, want the %d listed live proof checks", perSection["G"], har129GChecks)
	}
}

func TestHar129RequirementsRejectsIncompleteRows(t *testing.T) {
	root := contractsDir(t)
	schema := schemaFor(t, compiler(t, root), "har129_requirements")
	doc := readJSON(t, filepath.Join(root, har129RequirementsPath)).(map[string]any)
	rows := doc["requirements"].([]any)
	delete(rows[0].(map[string]any), "required_behavior")
	if err := schema.Validate(doc); err == nil {
		t.Fatal("a requirement without required_behavior must be rejected")
	}
}
