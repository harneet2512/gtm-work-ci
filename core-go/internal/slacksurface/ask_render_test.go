package slacksurface

import (
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func TestMrkdwnConversionKeepsLinksAndEscapesControlCharacters(t *testing.T) {
	got := toMrkdwn("**Bold** <script> & [A & B](http://web.test/x?a=1&b=2) and [Two](http://web.test/y)")
	want := "*Bold* &lt;script&gt; &amp; <http://web.test/x?a=1&b=2|A &amp; B> and <http://web.test/y|Two>"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
	if toMrkdwn("javascript:alert(1) [x](javascript:alert(1))") != "javascript:alert(1) [x](javascript:alert(1))" {
		t.Error("a non-http link must stay inert text")
	}
}

func TestLongAnswersAreSplitIntoSectionsWithinSlacksLimit(t *testing.T) {
	long := strings.Repeat("line of text that goes on\n", 400)
	blocks := askBlocks(long)
	if len(blocks) < 2 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	for _, b := range blocks {
		if n := len([]rune(b.(*slack.SectionBlock).Text.Text)); n > 3000 {
			t.Errorf("a section has %d characters", n)
		}
	}
	if len(askBlocks(strings.Repeat("x", askSectionLimit*60))) > askMaxBlocks {
		t.Error("too many blocks")
	}
}

func TestConfirmValuesRoundTripAndRefuseGarbage(t *testing.T) {
	k, th, ck, ok := parseConfirmValue(confirmValue("play_next", "12.3", "thread"))
	if !ok || k != "play_next" || th != "12.3" || ck != "thread" {
		t.Errorf("round trip: %v %v %v %v", k, th, ck, ok)
	}
	for _, bad := range []string{"", "play_next", "|1|2", "a|b", "play_next||thread"} {
		if _, _, _, ok := parseConfirmValue(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	a, core, _ := newAsk()
	if a.interaction(click("U1", ActionAskRun, "garbage", "")) != nil || len(core.actions) != 0 {
		t.Error("a malformed button value must be ignored")
	}
}
