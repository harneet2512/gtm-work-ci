package bucket1

import (
	"encoding/json"
	"fmt"
	"os"
)

// ModelCallsPerEpisode is how many model calls a full recording of one episode needs: one per gate that has
// a semantic assertion (B1 inference boundary, B3 links, B5 relevance, B8 confidence), never one per
// assertion. Grading itself makes none: judgments are replayed from a cassette file.
const ModelCallsPerEpisode = 4

// Run grades B1 to B9 on one episode, in gate order. A gate that cannot run is "not measured" (unknown).
func Run(ep Episode, js map[string]Judgment) []Result {
	return []Result{
		GradeB1(ep, js), GradeB2(ep), GradeB3(ep, js), GradeB4(ep), GradeB5(ep, js),
		GradeB6(ep), GradeB7(ep), GradeB8(ep, js), GradeB9(ep),
	}
}

// File is the results artifact the web evals page reads: results by episode.
type File struct {
	Version  int                 `json:"version"`
	Episodes map[string][]Result `json:"episodes"`
}

// ReadEpisode loads an episode bundle (JSON). A missing or malformed bundle is an explicit error.
func ReadEpisode(path string) (Episode, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Episode{}, fmt.Errorf("bucket1: read episode bundle: %w", err)
	}
	var ep Episode
	if err := json.Unmarshal(raw, &ep); err != nil {
		return Episode{}, fmt.Errorf("bucket1: episode bundle %s: %w", path, err)
	}
	if ep.ID == "" || ep.At.IsZero() {
		return Episode{}, fmt.Errorf("bucket1: episode bundle %s needs an id and the episode's world time", path)
	}
	return ep, nil
}

// WriteResults merges the episode's results into the artifact at path (created when absent).
func WriteResults(path, episodeID string, results []Result) error {
	f := File{Version: 1, Episodes: map[string][]Result{}}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &f); err != nil {
			return fmt.Errorf("bucket1: existing results %s: %w", path, err)
		}
		if f.Episodes == nil {
			f.Episodes = map[string][]Result{}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bucket1: read results %s: %w", path, err)
	}
	f.Episodes[episodeID] = results
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("bucket1: encode results: %w", err)
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
