// Package demosmoke is the Slack smoke test of HAR-137 section 12 against the fixture core (fakecore). It
// has two halves that share one set of assertions: a no-human driver that pushes the interactions a person
// would click through a fake Slack (RunNoHuman), and Verify, which checks the artefacts of a run, whether
// the clicks came from that driver or from a human in the real workspace: the fakecore request log, the
// audit log of the bot's own Slack writes, and every other log file of the run.
package demosmoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/harneet2512/gtm-work/core-go/internal/fakecore"
	"github.com/harneet2512/gtm-work/core-go/internal/slacksurface"
)

// Artefact file names inside a run directory.
const (
	CoreLogFile    = "fakecore-log.json"
	SlackAuditFile = "slack-audit.jsonl"
	binDir         = "bin" // where scripts/slack/demo_smoke.py puts the compiled commands
	maxVisible     = 3
	minUpdatesOnM2 = 3 // choose, edit, send
)

// Check is one assertion and its outcome.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// VerifyOptions configure Verify.
type VerifyOptions struct {
	Dir string
	// AllowPartial accepts a run that stopped before every message was posted (assertions on the missing
	// steps are skipped); the default demands the complete flow.
	AllowPartial bool
	// SecretEnv names environment variables whose values must not appear in any file of the run.
	SecretEnv []string
	Getenv    func(string) string
}

var tokenLike = regexp.MustCompile(`xox[a-z]-[A-Za-z0-9-]{8,}|xapp-[A-Za-z0-9-]{8,}`)

// Verify reads the run directory and returns every check. It errors only when an artefact is unreadable.
func Verify(o VerifyOptions) ([]Check, error) {
	var core fakecore.Log
	if err := readJSON(filepath.Join(o.Dir, CoreLogFile), &core); err != nil {
		return nil, err
	}
	audit, err := readAudit(filepath.Join(o.Dir, SlackAuditFile))
	if err != nil {
		return nil, err
	}
	var posts, updates []slacksurface.AuditRecord
	failedWrites := 0
	for _, r := range audit {
		if !r.OK {
			failedWrites++
		}
		switch {
		case r.Method == "chat.postMessage" && r.OK:
			posts = append(posts, r)
		case r.Method == "chat.update" && r.OK:
			updates = append(updates, r)
		}
	}
	checks := append(slackChecks(o, posts, updates, failedWrites), coreChecks(o, core)...)
	secrets, err := noSecrets(o)
	if err != nil {
		return nil, err
	}
	return append(checks, secrets), nil
}

// Failed returns the checks that did not pass.
func Failed(cs []Check) []Check {
	var out []Check
	for _, c := range cs {
		if !c.OK {
			out = append(out, c)
		}
	}
	return out
}

func check(name string, ok bool, format string, a ...any) Check {
	return Check{Name: name, OK: ok, Detail: fmt.Sprintf(format, a...)}
}

func slackChecks(o VerifyOptions, posts, updates []slacksurface.AuditRecord, failedWrites int) []Check {
	cs := []Check{
		check("visible bot messages <= 3", len(posts) <= maxVisible, "%d chat.postMessage", len(posts)),
		check("every Slack write succeeded", failedWrites == 0, "%d failed writes", failedWrites),
	}
	if !o.AllowPartial {
		cs = append(cs, check("M1, M2 and M3 were all posted", len(posts) == maxVisible, "%d messages posted", len(posts)))
	}
	known := map[string]int{}
	for i, p := range posts {
		known[p.TS] = i + 1
	}
	stray := 0
	perMessage := map[int]int{}
	for _, u := range updates {
		if n, ok := known[u.TS]; ok {
			perMessage[n]++
		} else {
			stray++
		}
	}
	cs = append(cs, check("every update edits a message this run posted", stray == 0, "%d updates of an unknown ts", stray))
	if len(posts) >= 2 {
		cs = append(cs, check("M2 updated in place (same ts) by choose, edit and send", perMessage[2] >= minUpdatesOnM2, "%d chat.update on M2 (ts %s)", perMessage[2], posts[1].TS))
	} else if !o.AllowPartial {
		cs = append(cs, check("M2 updated in place (same ts) by choose, edit and send", false, "M2 was never posted"))
	}
	if len(posts) == maxVisible {
		cs = append(cs, check("M3 updated in place by the verdict", perMessage[3] >= 1, "%d chat.update on M3 (ts %s)", perMessage[3], posts[2].TS))
	}
	return cs
}

// count returns how many log entries match.
func count(es []fakecore.Entry, pred func(fakecore.Entry) bool) int {
	n := 0
	for _, e := range es {
		if pred(e) {
			n++
		}
	}
	return n
}

func hasField(raw json.RawMessage, key string) bool {
	var m map[string]json.RawMessage
	return json.Unmarshal(raw, &m) == nil && m[key] != nil
}

func post(suffix string) func(fakecore.Entry) bool {
	return func(e fakecore.Entry) bool { return e.Method == "POST" && strings.HasSuffix(e.Path, suffix) }
}

func coreChecks(o VerifyOptions, core fakecore.Log) []Check {
	es := core.Entries
	decisions := post("/strategy-decision")
	created := count(es, func(e fakecore.Entry) bool { return decisions(e) && e.Status == 201 })
	edited := count(es, func(e fakecore.Entry) bool {
		return decisions(e) && e.Status == 200 && hasField(e.Request, "final_artifact")
	})
	sent := count(es, func(e fakecore.Entry) bool { return post("/send")(e) && e.Status == 200 })
	verdicts := count(es, func(e fakecore.Entry) bool {
		return post("/judgment-verdict")(e) && e.Status == 200 && hasField(e.Request, "verdict")
	})
	unexpected := count(es, func(e fakecore.Entry) bool {
		return e.Method == "POST" && !decisions(e) && !post("/send")(e) && !post("/judgment-verdict")(e) && !post("/reservation")(e)
	})
	refused := count(es, func(e fakecore.Entry) bool { return e.Status == 400 || e.Status == 401 || e.Status >= 500 })
	strategies := count(es, func(e fakecore.Entry) bool { return e.Method == "GET" && strings.HasSuffix(e.Path, "/strategies") })
	cs := []Check{
		check("no draft regeneration on View or Choose", len(core.StrategiesSHA256) == 1 && created <= 1,
			"%d distinct strategy-set bodies over %d reads, %d choices created", len(core.StrategiesSHA256), strategies, created),
		check("only decision, send, verdict and reservation writes reached core", unexpected == 0, "%d other POSTs", unexpected),
		check("core answered no request with 400, 401 or 5xx", refused == 0, "%d such answers", refused),
	}
	// A complete run persisted each step exactly once; a partial one may have stopped before a step, but
	// no step may have been persisted twice.
	times := func(n int) bool { return n == 1 || (o.AllowPartial && n == 0) }
	return append(cs,
		check("the choice was persisted once", times(created), "%d choices created (201)", created),
		check("the edit was persisted", edited >= 1 || (o.AllowPartial && edited == 0), "%d edits saved (200 with final_artifact)", edited),
		check("send was executed once (record-only effect)", times(sent) && sent == len(core.Effects), "%d sends accepted, %d effects recorded", sent, len(core.Effects)),
		check("the verdict was persisted once", times(verdicts), "%d verdicts accepted", verdicts),
	)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("demosmoke: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("demosmoke: parse %s: %w", path, err)
	}
	return nil
}

func readAudit(path string) ([]slacksurface.AuditRecord, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("demosmoke: read %s: %w", path, err)
	}
	var out []slacksurface.AuditRecord
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r slacksurface.AuditRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("demosmoke: parse a line of %s: %w", path, err)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// noSecrets scans every file of the run (except the compiled commands in bin/) for Slack-shaped tokens and for the values of the named variables.
func noSecrets(o VerifyOptions) (Check, error) {
	var secrets []string
	for _, name := range o.SecretEnv {
		if v := strings.TrimSpace(o.Getenv(name)); len(v) >= 8 {
			secrets = append(secrets, v)
		}
	}
	files, scanned := 0, 0
	var leaks []string
	err := filepath.WalkDir(o.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == binDir { // compiled commands, not logs
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		scanned += len(b)
		found := tokenLike.Match(b)
		for _, s := range secrets {
			found = found || bytes.Contains(b, []byte(s))
		}
		if found {
			leaks = append(leaks, d.Name()) // names the file, never the match
		}
		return nil
	})
	if err != nil {
		return Check{}, fmt.Errorf("demosmoke: scan %s: %w", o.Dir, err)
	}
	return check("no token appears in any log", len(leaks) == 0, "%d files, %d bytes scanned, %d secret values and the Slack token shapes checked; leaks in: %v",
		files, scanned, len(secrets), leaks), nil
}
