package corectx

// SetMaxEvidenceScan lowers the evidence scan bound for a test and returns the restore function.
func SetMaxEvidenceScan(n int) (restore func()) {
	old := maxEvidenceScan
	maxEvidenceScan = n
	return func() { maxEvidenceScan = old }
}
