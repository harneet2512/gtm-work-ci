package rundriver

import (
	"testing"
	"time"
)

func TestBackoffIsForgottenForRunsThatAreNoLongerOpenAndAnAccountIsReservedOnce(t *testing.T) {
	now := time.Now()
	d := &Driver{opt: Options{Clock: fixedClock{now}}, inflight: map[string]string{}, retryAt: map[string]time.Time{
		"gone": now.Add(time.Hour), "still-open": now.Add(time.Hour)}}
	d.forget([]openRun{{ID: "still-open", AccountID: "a"}})
	if _, ok := d.retryAt["gone"]; ok {
		t.Fatal("the backoff of a run that left the open statuses was kept")
	}
	if _, ok := d.retryAt["still-open"]; !ok {
		t.Fatal("the backoff of an open run was dropped")
	}
	if d.reserve(openRun{ID: "still-open", AccountID: "a"}) {
		t.Fatal("a run inside its backoff was reserved")
	}
	if !d.reserve(openRun{ID: "other", AccountID: "a"}) {
		t.Fatal("a free account was refused")
	}
	if d.reserve(openRun{ID: "second", AccountID: "a"}) {
		t.Fatal("an account already being driven was reserved a second time")
	}
	d.release(openRun{ID: "other", AccountID: "a"}, 0)
	if !d.reserve(openRun{ID: "second", AccountID: "a"}) {
		t.Fatal("a released account was not free again")
	}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }
