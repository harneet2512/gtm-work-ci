package deterministic

import "testing"

func TestFindDates(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"meet on 2026-10-01 please", []string{"2026-10-01"}},
		{"our review on Thursday, October 1. See you then", []string{"2026-10-01"}},
		{"by Sept. 30th, 2026", []string{"2026-09-30"}},
		{"the 14 of December works", []string{"2026-12-14"}},
		{"I'll send it by Monday the 14th", []string{"2026-09-14"}},
		{"Send revised order form by 9/14", []string{"2026-09-14"}},
		{"in January 3 weeks", []string{"2027-01-03"}},
		{"open 24/7, a 60-minute call at 10:00-11:00", nil},
		{"no date on February 31", nil},
		{"on 10/1/27 and 3 Mar", []string{"2027-10-01", "2027-03-03"}},
	}
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got := findDates(tc.text, now)
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %v", got, tc.want)
			}
			for i, d := range got {
				if d.Day.Format("2006-01-02") != tc.want[i] {
					t.Fatalf("date %d = %s, want %s", i, d.Day.Format("2006-01-02"), tc.want[i])
				}
			}
		})
	}
}
