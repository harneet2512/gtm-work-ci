package codespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func samplePaths() []HumanPath {
	return []HumanPath{
		{Case: "MedTech Advances", ChoiceButton: "Select B", ChoiceTitle: "Loop in the champion", ChoiceRank: 2,
			To: "Marco <marco@x.test>", CC: "", Subject: "Re: security review (confirm timing)", Body: "Hi Marco,\n\nCould we confirm the next step?",
			Interpretation: "You wanted a softer ask.", Correction: "I kept the champion in the loop."},
		{Case: "EcoLite Innovations", ChoiceButton: "Select A", ChoiceTitle: "Call", ChoiceRank: 2, To: "Ana <ana@x.test>", CC: "Bo <bo@x.test>",
			Subject: "S2", Body: "B2", Interpretation: "I2", Correction: "C2", Note: "a note"},
	}
}

func TestRunSheetListsExactlyWhatWasRecorded(t *testing.T) {
	out := RunSheet{Paths: samplePaths()}.Render()
	for _, want := range []string{
		"MEDTECH ADVANCES", "press \"Select B\"", "Re: security review (confirm timing)", "     Hi Marco,", "     Could we confirm the next step?",
		"To: Marco <marco@x.test>", "CC: (empty)", "I kept the champion in the loop.", "Leave the Note field empty",
		"EcoLite Innovations"[:0] + "ECOLITE INNOVATIONS", "Bo <bo@x.test>", "Note (optional field), exactly:", "     a note",
		"gtm_ai Demo - Start", "gtm_ai Demo - Stop", "gtm_ai Demo - Reset", "Press Play again",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the run sheet lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "http") || strings.Contains(out, "xoxb") {
		t.Fatalf("no link or secret belongs on the sheet:\n%s", out)
	}
}

func TestRunSheetIsWrittenAtomicallyUnderTheDemoHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "ghost-demo")
	path := RunSheetPath(home)
	if filepath.Base(path) != "RUNSHEET.txt" || filepath.Dir(path) != home {
		t.Fatalf("path = %s", path)
	}
	for i := 0; i < 2; i++ { // the second write replaces the first
		if err := WriteRunSheet(path, RunSheet{Paths: samplePaths()}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "Select B") {
		t.Fatalf("%v %s", err, b)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("no temp file may be left behind")
	}
	if err := WriteRunSheet(filepath.Join(path, "x", "y"), RunSheet{}); err == nil {
		t.Fatal("a path under a file cannot be written")
	}
}
