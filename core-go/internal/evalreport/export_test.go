package evalreport

// SetBetweenReads installs a hook that runs after each of Generate's table reads (the step index is the
// number of reads completed) and returns a restore func — the seam the snapshot test uses to commit a
// concurrent write mid-report.
func SetBetweenReads(f func(step int)) (restore func()) {
	prev := betweenReads
	betweenReads = f
	return func() { betweenReads = prev }
}
