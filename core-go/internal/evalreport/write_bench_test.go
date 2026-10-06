package evalreport_test

// Writes the committed benchmark report. Opt-in (EVALREPORT_BENCH_OUT=<file>): it sorts last on purpose,
// so the database it reads holds every world the earlier tests built — the populated TEST world, not a
// real corpus. The bytes are exactly what `ghostctl learn report --all` emits (both go through
// evalreport.Encode).
//
//	EVALREPORT_BENCH_OUT=/abs/path/report.json go test -count=1 ./internal/evalreport/ -run '.*'

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/evalreport"
)

func TestEncodeIsIndentedJSONWithTrailingNewline(t *testing.T) {
	raw, err := evalreport.Encode(evalreport.ReportSet{Reports: map[string]*evalreport.Report{}})
	if err != nil {
		t.Fatal(err)
	}
	if raw[len(raw)-1] != '\n' || !json.Valid(raw) {
		t.Fatalf("Encode = %q", raw)
	}
}

func TestWriteBenchReport(t *testing.T) {
	out := os.Getenv("EVALREPORT_BENCH_OUT")
	if out == "" {
		t.Skip("set EVALREPORT_BENCH_OUT to write the benchmark report")
	}
	raw, err := evalreport.Encode(generate(t, evalreport.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", out, err)
	}
}
