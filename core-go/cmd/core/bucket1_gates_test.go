package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type gateStub struct {
	err    error
	sawErr error
}

func (g *gateStub) RunGates(ctx context.Context, _ string) error {
	g.sawErr = ctx.Err()
	return g.err
}

func TestBucket1RegradeIsNotFailedByTheDeadlineBucket2RanInto(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	b2, b1 := &gateStub{err: errors.New("judges timed out")}, &gateStub{}
	err := bothGates{b2: b2, b1: b1}.RunGates(ctx, "run")
	if b1.sawErr != nil {
		t.Fatalf("B9 got an expired context: %v", b1.sawErr)
	}
	if err == nil || err.Error() != "judges timed out" {
		t.Fatalf("the Bucket 2 failure must still be reported, got %v", err)
	}
}
