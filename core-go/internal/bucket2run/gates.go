package bucket2run

import "context"

// RunGates implements strategystore.GateRunner: it measures every gate that has facts so far and stores the
// results. The returned error joins the gates that could not be measured (they stay "not measured").
func (r *Runner) RunGates(ctx context.Context, runID string) error {
	_, err := r.Run(ctx, runID)
	return err
}
