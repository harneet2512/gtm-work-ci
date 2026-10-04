package speakers_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/speakers"
)

func seg(label, text string) speakers.Segment { return speakers.Segment{Label: label, Text: text} }

var (
	priya = speakers.Candidate{PersonID: "p-priya", Name: "Priya Shah"}
	tom   = speakers.Candidate{PersonID: "p-tom", Name: "Tom Becker"}
	tom2  = speakers.Candidate{PersonID: "p-tom2", Name: "Tom Brandt"}
	jose  = speakers.Candidate{PersonID: "p-jose", Name: "José Álvarez"}
)

func TestResolveTable(t *testing.T) {
	tests := []struct {
		name string
		in   speakers.Input
		want []speakers.Match
	}{
		{
			name: "self introduction by first name (HAR-99 puzzle 1)",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya},
				Segments: []speakers.Segment{
					seg("speaker_01", "Quick round of intros?"),
					seg("speaker_02", "Sure. Priya here - I run operations for the US sites."),
				},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}},
		},
		{
			name: "self introduction with I'm and a curly apostrophe",
			in: speakers.Input{
				Unlabelled: []string{"speaker_03"},
				Candidates: []speakers.Candidate{priya, tom},
				Segments:   []speakers.Segment{seg("speaker_03", "Hi all, I’m Tom, I manage EU operations.")},
			},
			want: []speakers.Match{{Label: "speaker_03", PersonID: "p-tom"}},
		},
		{
			name: "self introduction that opens the turn with the full name",
			in: speakers.Input{
				Unlabelled: []string{"speaker_03"},
				Candidates: []speakers.Candidate{tom, tom2},
				Segments:   []speakers.Segment{seg("speaker_03", "Tom Becker, I manage EU operations out of Berlin.")},
			},
			want: []speakers.Match{{Label: "speaker_03", PersonID: "p-tom"}},
		},
		{
			name: "addressed by name: the next speaker is the addressee",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya},
				Segments: []speakers.Segment{
					seg("speaker_01", "Great. Priya, what does success look like?"),
					seg("speaker_02", "Berlin and Rotterdam live before January."),
				},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}},
		},
		{
			name: "thanks NAME: the previous speaker is the addressee",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya},
				Segments: []speakers.Segment{
					seg("speaker_02", "We can start in October."),
					seg("speaker_01", "Thanks Priya, that helps."),
				},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}},
		},
		{
			name: "two attendees named Tom: a first-name cue is ambiguous",
			in: speakers.Input{
				Unlabelled: []string{"speaker_03"},
				Candidates: []speakers.Candidate{tom, tom2},
				Segments:   []speakers.Segment{seg("speaker_03", "Tom here, I manage EU operations.")},
			},
			want: nil,
		},
		{
			name: "full name disambiguates two Toms",
			in: speakers.Input{
				Unlabelled: []string{"speaker_03"},
				Candidates: []speakers.Candidate{tom, tom2},
				Segments:   []speakers.Segment{seg("speaker_03", "This is Tom Brandt from finance.")},
			},
			want: []speakers.Match{{Label: "speaker_03", PersonID: "p-tom2"}},
		},
		{
			name: "no cue, no match",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya},
				Segments:   []speakers.Segment{seg("speaker_02", "Budget is the open question.")},
			},
			want: nil,
		},
		{
			name: "a name merely mentioned is not a cue",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya, tom},
				Segments:   []speakers.Segment{seg("speaker_02", "I will ask Tom whether Priya can approve it.")},
			},
			want: nil,
		},
		{
			name: "conflicting cues leave the speaker unresolved",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya, tom},
				Segments: []speakers.Segment{
					seg("speaker_01", "Tom, what do you think?"),
					seg("speaker_02", "Priya here, happy to take that."),
				},
			},
			want: nil,
		},
		{
			name: "two labels pointing at one person are both dropped",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02", "speaker_04"},
				Candidates: []speakers.Candidate{priya},
				Segments: []speakers.Segment{
					seg("speaker_02", "Priya here."),
					seg("speaker_04", "This is Priya again, on my phone."),
				},
			},
			want: nil,
		},
		{
			name: "labels that are not unlabelled are ignored",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya, tom},
				Segments: []speakers.Segment{
					seg("speaker_02", "Priya here."),
					seg("speaker_03", "Tom here."),
				},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}},
		},
		{
			name: "non-ASCII names at the end of a sentence",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{jose},
				Segments:   []speakers.Segment{seg("speaker_02", "Hello, I am José")},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-jose"}},
		},
		{
			name: "possessives and relations are not introductions",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya},
				Segments: []speakers.Segment{
					seg("speaker_02", "It's Priya's turn to present."),
					seg("speaker_02", "I'm Priya’s manager, by the way."),
					seg("speaker_01", "Thanks Priya's team for the notes."),
				},
			},
			want: nil,
		},
		{
			name: "no candidates",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Segments:   []speakers.Segment{seg("speaker_02", "Priya here.")},
			},
			want: nil,
		},
		{
			name: "candidate without a usable name is never matched",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{{PersonID: "p-x", Name: "  "}},
				Segments:   []speakers.Segment{seg("speaker_02", "Priya here.")},
			},
			want: nil,
		},
		{
			name: "duplicate candidates count once",
			in: speakers.Input{
				Unlabelled: []string{"speaker_02"},
				Candidates: []speakers.Candidate{priya, priya},
				Segments:   []speakers.Segment{seg("speaker_02", "Priya here.")},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}},
		},
		{
			name: "results are ordered by label",
			in: speakers.Input{
				Unlabelled: []string{"speaker_05", "speaker_02"},
				Candidates: []speakers.Candidate{priya, tom},
				Segments: []speakers.Segment{
					seg("speaker_05", "Tom here."),
					seg("speaker_02", "Priya here."),
				},
			},
			want: []speakers.Match{{Label: "speaker_02", PersonID: "p-priya"}, {Label: "speaker_05", PersonID: "p-tom"}},
		},
		{
			name: "empty transcript",
			in:   speakers.Input{Unlabelled: []string{"speaker_02"}, Candidates: []speakers.Candidate{priya}},
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := speakers.Resolve(tc.in)
			for i := range got {
				if got[i].Cue.Kind == "" || got[i].Cue.Span == "" || got[i].Cue.Pattern == "" {
					t.Errorf("match %+v carries no evidence", got[i])
				}
				got[i].Cue = speakers.Cue{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveRecordsTheMatchedCue(t *testing.T) {
	got := speakers.Resolve(speakers.Input{
		Unlabelled: []string{"speaker_02"},
		Candidates: []speakers.Candidate{priya},
		Segments: []speakers.Segment{
			seg("speaker_01", "Great. Priya, what does success look like?"),
			seg("speaker_02", "Sure. Priya here - I run operations."),
		},
	})
	if len(got) != 1 {
		t.Fatalf("matches = %+v", got)
	}
	if c := got[0].Cue; c.Kind != speakers.CueIntro || c.Span != "Priya here" || c.Pattern != "intro:name-here" {
		t.Errorf("cue = %+v, want the self-introduction (it outranks the addressed cue) with its span", c)
	}
	vocative := speakers.Resolve(speakers.Input{
		Unlabelled: []string{"speaker_02"},
		Candidates: []speakers.Candidate{priya},
		Segments:   []speakers.Segment{seg("speaker_01", "Great. Priya, what does success look like?"), seg("speaker_02", "Soon.")},
	})
	if len(vocative) != 1 || vocative[0].Cue.Kind != speakers.CueVocative || !strings.Contains(vocative[0].Cue.Span, "Priya,") {
		t.Errorf("vocative cue = %+v", vocative)
	}
}
