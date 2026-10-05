package claims

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The worker serves exactly one prompt version and rejects any other with 422, which the worker
// client treats as permanent, so a stale default here would quarantine every activity's model
// extraction. The default must follow the contract's extractor_version default.
func TestDefaultExtractorVersionMatchesWorkerContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "openapi", "worker.yaml"))
	if err != nil {
		t.Fatalf("read worker contract: %v", err)
	}
	m := regexp.MustCompile(`extractor_version:\s*\{\s*type:\s*string,\s*default:\s*([a-z0-9.-]+)\s*\}`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("worker.yaml declares no extractor_version default")
	}
	if got, want := DefaultExtractorVersion, string(m[1]); got != want {
		t.Fatalf("DefaultExtractorVersion = %q, worker contract default = %q", got, want)
	}
}
