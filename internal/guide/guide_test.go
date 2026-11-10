package guide_test

import (
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/guide"
)

func TestFindTakesASlugItsStartOrATitleStart(t *testing.T) {
	for word, want := range map[string]string{
		"steps": "steps", "STEPS": "steps", "routine-files": "routine-files", "routine-c": "routine-commands", "during": "during-a-run",
		"Moving": "moving", "focus": "focus", "typing": "typing", "mov": "moving",
	} {
		s, ok := guide.Find(word)
		if !ok || s.Slug != want {
			t.Errorf("Find(%q) = %q, %v, want %q", word, s.Slug, ok, want)
		}
	}
	for _, word := range []string{"", "nothing", "routine", "r"} { // "routine" starts several sections; "r" even more
		if s, ok := guide.Find(word); ok {
			t.Errorf("Find(%q) = %q: a word that fits several sections or none should not pick one", word, s.Slug)
		}
	}
}

func TestListingHasEverySectionOnALine(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(guide.Listing()), "\n")
	if len(lines) != len(guide.Sections) {
		t.Fatalf("%d lines for %d sections", len(lines), len(guide.Sections))
	}
	if !strings.HasPrefix(lines[0], "moving") || !strings.Contains(guide.Listing(), "Routines file (routines.json)") {
		t.Errorf("listing:\n%s", guide.Listing())
	}
}

func TestLayoutWrapsEachTopicBesideItsKeysAndNumbersThem(t *testing.T) {
	lines := guide.Layout(guide.Sections, 80)
	topics := 0
	for _, s := range guide.Sections {
		topics += len(s.Topics)
	}
	firsts, maxTopic := 0, -1
	for _, l := range lines {
		if l.Kind == guide.LineRow {
			if l.First {
				firsts++
				if l.Keys == "" {
					t.Errorf("the first line of a topic carries its keys: %+v", l)
				}
			} else if l.Keys != "" {
				t.Errorf("only the first line has the keys: %+v", l)
			}
			maxTopic = max(maxTopic, l.Topic)
		}
		if w := len([]rune(l.Pad())); w > 80 {
			t.Errorf("line %q is %d wide", l.Pad(), w)
		}
	}
	if firsts != topics || maxTopic != topics-1 {
		t.Errorf("%d first lines and topic numbers up to %d for %d topics", firsts, maxTopic, topics)
	}
	if guide.SectionStart(lines, "steps") < 0 || guide.SectionStart(lines, "nope") != -1 {
		t.Error("SectionStart should find sections by slug")
	}
}

func TestWrapKeepsLongWordsWholeAndNeverReturnsNothing(t *testing.T) {
	got := guide.Wrap("aaa bbb ccc", 7)
	if strings.Join(got, "|") != "aaa bbb|ccc" {
		t.Errorf("%q", got)
	}
	if got := guide.Wrap("supercalifragilistic is long", 5); got[0] != "supercalifragilistic" {
		t.Errorf("%q", got)
	}
	if got := guide.Wrap("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("%q", got)
	}
}

func TestQueriesIgnoreCaseUnlessTheyHaveACapital(t *testing.T) {
	q := guide.NewQuery("routine")
	if !q.Has("A Routine runs") || !q.Has("ROUTINE") {
		t.Error("lower case matches any case")
	}
	q = guide.NewQuery("Routine")
	if !q.Has("A Routine runs") || q.Has("a routine runs") {
		t.Error("a capital makes it exact")
	}
	if !guide.NewQuery("").Empty() || guide.NewQuery("").Has("anything") {
		t.Error("an empty query matches nothing")
	}
}

func TestSpansAlternateBetweenPlainAndMatchedText(t *testing.T) {
	got := guide.NewQuery("ab").Spans("xxABxxabxx")
	if strings.Join(got, "|") != "xx|AB|xx|ab|xx" {
		t.Errorf("%q", got)
	}
	if got := guide.NewQuery("ab").Spans("ab"); strings.Join(got, "|") != "|ab|" {
		t.Errorf("%q", got)
	}
	if got := guide.NewQuery("zz").Spans("none"); len(got) != 1 || got[0] != "none" {
		t.Errorf("%q", got)
	}
	if got := guide.NewQuery("").Spans("text"); len(got) != 1 {
		t.Errorf("%q", got)
	}
}

func TestHitsFindWholeTopicsEvenWhenTheirTextWraps(t *testing.T) {
	lines := guide.Layout(guide.Sections, 40) // narrow, so most topics wrap
	hits := guide.Hits(guide.Sections, lines, guide.NewQuery("shh routine unqueue drops it"))
	if len(hits) == 0 {
		t.Fatal("a phrase that wraps across lines should still be found")
	}
	for _, h := range hits {
		if !lines[h].First && lines[h].Kind != guide.LineSection {
			t.Errorf("a hit is where a topic starts: line %d %+v", h, lines[h])
		}
	}
	if got := guide.Hits(guide.Sections, lines, guide.NewQuery("")); got != nil {
		t.Error("nothing to find")
	}
	// a section title is a hit too
	foundTitle := false
	for _, h := range guide.Hits(guide.Sections, lines, guide.NewQuery("Writing steps")) {
		foundTitle = foundTitle || lines[h].Kind == guide.LineSection
	}
	if !foundTitle {
		t.Error("a section title is a hit too")
	}
}

func TestPlainWritesTitlesTopicsAndOnlyWhatMatches(t *testing.T) {
	steps, _ := guide.Find("steps")
	text := guide.Plain([]guide.Section{steps}, 80, guide.NewQuery(""))
	if !strings.HasPrefix(text, "Writing steps\n=============\n") || !strings.Contains(text, "until 11:00") {
		t.Errorf("text:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("line is %d wide: %q", len([]rune(line)), line)
		}
	}
	only := guide.Plain(guide.Sections, 80, guide.NewQuery("cycle"))
	if !strings.Contains(only, "run limits") || strings.Contains(only, "Moving around") {
		t.Errorf("only the matching topics should be written:\n%s", only)
	}
	whole := guide.Plain(guide.Sections, 80, guide.NewQuery("Writing steps"))
	if !strings.Contains(whole, "baseline 30% sets") || !strings.Contains(whole, "Writing steps\n=============") {
		t.Errorf("a section whose title matches is written whole:\n%s", whole)
	}
	if got := guide.Plain(guide.Sections, 80, guide.NewQuery("zzzzqqq")); strings.TrimSpace(got) != "" {
		t.Errorf("nothing matches: %q", got)
	}
}

// The guide is where each feature is documented, so a feature that is added without a line here is a feature nobody can find.
func TestTheGuideDocumentsTheRoutineFeatures(t *testing.T) {
	text := guide.Plain(guide.Sections, 200, guide.NewQuery(""))
	text = strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"routines.json", "repeat", "forever", "finish", "waiting", "summary", "until 11:00", "--alert", "--ask", "baseline 30%", "run pomodoro", "--times",
		"cycle", "shh routine add", "-t", "--replace", "shh routine remove", "shh routine next", "shh routine back", "shh routine unqueue",
		"--end-routine", "--continue-routine", "--after-routine", "interrupt", "queue", "Start next", "Skip",
		"shh guide", "shh routine list", "shh routine example",
		"8-bit ANSI palette", "https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit", "shh alert colors", "--color", "--position", "--size", "--sound", "--banner-for", "duration_secs", "button_color", "button_text_color", "--button-color", "live changes", "text_color", "--text-color", "Banner style", "--style",
		"triggers", "trigger form", "Switched on", "Skip the selected routine", "except", "--reminder", "--ask", "--when-busy", "--grace", "--until-alert", "--disabled", "shh routine skip", "shh routine log", "shh ui --tab train", "held back view", "Not important", "trained_label", "Run log", "flagged for review", "Review button", "shh daemon", "at login", "shh worker start", "shh worker stop", "shh worker restart", "shh worker status", "weekdays",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the guide never mentions %q", want)
		}
	}
}
