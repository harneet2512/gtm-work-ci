package runs

import "testing"

// The Executor interface is sealed: only this package's executors satisfy it.
func TestExecutorsAreSealed(t *testing.T) {
	var execs = []Executor{&RecordingExecutor{}, liveExecutor{}}
	for _, e := range execs {
		e.sealed()
	}
}
