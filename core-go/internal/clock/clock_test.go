package clock

import (
	"sync"
	"testing"
	"time"
)

var (
	_ Clock = Real{}
	_ Clock = (*Fixed)(nil)
)

func TestRealClockTracksWallTimeInUTC(t *testing.T) {
	before := time.Now()
	got := Real{}.Now()
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Fatalf("Real.Now() = %v not within [%v, %v]", got, before, after)
	}
	if got.Location() != time.UTC {
		t.Fatalf("Real.Now() location = %v, want UTC", got.Location())
	}
}

func TestFixedClock(t *testing.T) {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.FixedZone("x", 3600))
	c := NewFixed(start)
	if got := c.Now(); !got.Equal(start) || got.Location() != time.UTC {
		t.Fatalf("Now() = %v, want %v in UTC", got, start)
	}
	c.Advance(2 * time.Second)
	if got, want := c.Now(), start.Add(2*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance Now() = %v, want %v", got, want)
	}
	later := start.Add(time.Hour)
	c.Set(later)
	if !c.Now().Equal(later) {
		t.Fatalf("after Set Now() = %v, want %v", c.Now(), later)
	}
}

func TestFixedClockIsSafeForConcurrentUse(t *testing.T) {
	c := NewFixed(time.Unix(0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Advance(time.Millisecond)
			_ = c.Now()
		}()
	}
	wg.Wait()
	if got, want := c.Now(), time.Unix(0, 0).Add(50*time.Millisecond); !got.Equal(want) {
		t.Fatalf("Now() = %v, want %v", got, want)
	}
}
