package slacksurface

import (
	"testing"
	"time"
)

// HAR-124: a send waits on model work in core (the pre-send judgment and the send-time evals: a minute each while recording), so it gets
// a long client while every other call keeps the 30 s one.
func TestSendRunUsesALongClientAndEveryOtherCallKeepsTheShortOne(t *testing.T) {
	c := NewCoreHTTP("http://core.invalid", "tok", nil)
	if c.hc.Timeout != 30*time.Second || c.long.Timeout != SendRunTimeout || SendRunTimeout < 10*time.Minute {
		t.Fatalf("hc %v long %v", c.hc.Timeout, c.long.Timeout)
	}
}
