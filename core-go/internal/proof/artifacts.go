package proof

import (
	"encoding/json"
	"fmt"
	"os"
)

// writeJSON writes v as canonical 2-space-indented JSON with a trailing newline.
func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("proof: marshal %s: %w", path, err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("proof: write %s: %w", path, err)
	}
	return nil
}
