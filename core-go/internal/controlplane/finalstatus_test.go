package controlplane

import "testing"

func TestFinalStatusSaysSentOnlyForALiveRun(t *testing.T) {
	send := &strategyDecision{sendDecision: "send"}
	cases := []struct {
		name, status, mode string
		d                  *strategyDecision
		want               string
	}{
		{"a live run that sent", "decided", "live", send, "sent"},
		{"a dry run records the send but nothing left the system", "decided", "dry_run", send, "send_recorded"},
		{"a discard is a discard in either mode", "decided", "dry_run", &strategyDecision{sendDecision: "discard"}, "discarded"},
		{"chosen, not yet sent", "chosen", "dry_run", &strategyDecision{sendDecision: "pending"}, "awaiting_send"},
		{"no chooser, decided the older way", "decided", "dry_run", nil, "decided"},
		{"nothing chosen", "awaiting_choice", "dry_run", nil, "awaiting_choice"},
	}
	for _, c := range cases {
		if got := finalStatus(c.status, c.mode, c.d); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
