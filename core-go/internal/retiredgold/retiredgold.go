// Package retiredgold reads the RETIRED.json marker a gold directory can carry. Gold about invented companies cannot
// back an agreement number, a backtest gate or a knowledge-applicability score: every reader of a gold directory asks
// here first, skips what is retired and says "no gold" rather than quietly passing on cases that mean nothing.
package retiredgold

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Marker is the file name a retired gold directory carries.
const Marker = "RETIRED.json"

// Notice is the content of a marker: why a gold directory is not gold any more.
type Notice struct {
	Dir      string   `json:"-"`
	Retired  string   `json:"retired"` // the reason class, e.g. "invented"
	Reason   string   `json:"reason"`
	Cases    int      `json:"cases"`
	Accounts []string `json:"accounts"`
}

// Read returns the notice of a retired directory, or nil when the directory has no marker (or does not exist). A marker
// that cannot be read, or that does not say what is retired, is an error: it must not quietly un-retire the gold.
func Read(dir string) (*Notice, error) {
	raw, err := os.ReadFile(filepath.Join(dir, Marker))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("retiredgold: read %s: %w", filepath.Join(dir, Marker), err)
	}
	var n Notice
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, fmt.Errorf("retiredgold: decode %s: %w", filepath.Join(dir, Marker), err)
	}
	if n.Retired == "" {
		return nil, fmt.Errorf("retiredgold: %s does not say what is retired", filepath.Join(dir, Marker))
	}
	n.Dir = dir
	return &n, nil
}

// NoGold is the sentence a report carries instead of numbers computed on retired gold.
func (n Notice) NoGold() string {
	return fmt.Sprintf("no gold: %s is retired (%s). %s", n.Dir, n.Retired, n.Reason)
}

// Split separates the directories that are still gold from the notices of the retired ones, keeping the order.
func Split(dirs []string) (live []string, retired []Notice, err error) {
	for _, d := range dirs {
		n, err := Read(d)
		if err != nil {
			return nil, nil, err
		}
		if n != nil {
			retired = append(retired, *n)
			continue
		}
		live = append(live, d)
	}
	return live, retired, nil
}
