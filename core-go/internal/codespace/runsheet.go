package codespace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunSheetName is the presenter's run sheet, written next to the demo's other files (GHOST_DEMO_HOME, default D:\ghost-demo).
// It is never part of the web app or of Slack: it is a plain text file for the person giving the demo.
const RunSheetName = "RUNSHEET.txt"

// RunSheetPath is where the run sheet of a demo home lives.
func RunSheetPath(home string) string { return filepath.Join(home, RunSheetName) }

// RunSheet is what the record run's human did: the exact choices and words the stored model answers belong to.
type RunSheet struct{ Paths []HumanPath }

// Render is the sheet's text. Every value is quoted from the recorded run, so what the presenter types is what was recorded.
func (s RunSheet) Render() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("GHOST DEMO - RUN SHEET (for the presenter only; it is not part of the product)")
	line("")
	line("Type exactly what is written here. Every model call for this path was recorded once and is replayed, so the rehearsal and the")
	line("demo cost nothing. A different button, or different words, make one new model call (recorded once, then replayed).")
	line("")
	line("1. Start the demo: double-click \"gtm_ai Demo - Start\". It resets everything and opens the control plane.")
	line("2. In the control plane press Play. Cliff posts in #ghost-demo; follow each case below in Slack.")
	for i, p := range s.Paths {
		line("")
		line("%s", sectionTitle(i, p))
		line("-----------------------------------------------------------------")
		line("a. Message 2 (the three strategies): press %q  (%s, gtm_ai's rank %d).", p.ChoiceButton, p.ChoiceTitle, p.ChoiceRank)
		line("b. The selected action appears in place. Press Edit. In the form leave the recipients as they are:")
		line("     To: %s", orNone(p.To))
		line("     CC: %s", orNone(p.CC))
		line("   Replace the Subject with exactly:")
		line("%s", indent(p.Subject, "     "))
		line("   Replace the Body with exactly:")
		line("%s", indent(p.Body, "     "))
		line("   Press Save.")
		line("c. Press Send (the send is a dry run; nothing leaves the building).")
		line("d. Message 3 (what Cliff understood): press \"Edit interpretation\". Cliff's interpretation reads:")
		line("%s", indent(p.Interpretation, "     "))
		line("   Replace it with exactly:")
		line("%s", indent(p.Correction, "     "))
		if p.Note != "" {
			line("   Note (optional field), exactly:")
			line("%s", indent(p.Note, "     "))
		} else {
			line("   Leave the Note field empty. Press Save.")
		}
	}
	line("")
	line("3. Back in the control plane, inspect what changed and the knowledge Cliff formed from the correction.")
	line("4. Press Play again: the demo continues into the next account. Inspect the knowledge it retrieved and used.")
	line("5. Stop the demo: double-click \"gtm_ai Demo - Stop\". To rehearse again: \"gtm_ai Demo - Reset\" (presenter only).")
	return b.String()
}

func sectionTitle(i int, p HumanPath) string {
	if i == 0 {
		return strings.ToUpper(p.Case) + " - the first account"
	}
	return strings.ToUpper(p.Case) + " - after the second Play (optional: the same steps, recorded too)"
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(empty)"
	}
	return s
}

func indent(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\r\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// WriteRunSheet writes the sheet atomically to path, creating its directory.
func WriteRunSheet(path string, s RunSheet) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(s.Render()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
