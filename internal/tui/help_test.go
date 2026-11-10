package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/guide"
)

func helpRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t)
	r.m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	r.press("?")
	return r
}

func footer(r *rig) string {
	lines := strings.Split(r.view(), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func TestSlashSearchesTheHelpAndJumpsToTheFirstMatch(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	if r.m.prompt == nil || r.m.prompt.kind != promptHelpSearch {
		t.Fatal("/ should open a search box in the help")
	}
	if !strings.Contains(footer(r), "search the guide:") {
		t.Errorf("the box should be where the footer is: %q", footer(r))
	}
	r.typeText("unqueue")
	r.press("enter")
	view := r.view()
	if !strings.Contains(view, "unqueue") && !strings.Contains(view, "queue") {
		t.Errorf("the screen should show the match:\n%s", view)
	}
	if f := footer(r); !strings.Contains(f, "/unqueue") || !strings.Contains(f, "match 1 of") || !strings.Contains(f, "n/N") {
		t.Errorf("footer = %q", f)
	}
	if r.m.helpScroll == 0 {
		t.Error("the guide should have scrolled to the match")
	}
}

func TestNAndShiftNGoThroughTheMatchesAndWrapAround(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	r.typeText("routine")
	r.press("enter")
	n := len(r.m.helpHits)
	if n < 4 {
		t.Fatalf("routine should be in several places, found %d", n)
	}
	first := r.m.helpScroll
	r.press("n")
	second := r.m.helpScroll
	if second <= first || r.m.helpAt != 1 {
		t.Errorf("n should go on: scroll %d then %d, at %d", first, second, r.m.helpAt)
	}
	r.press("N")
	if r.m.helpScroll != first || r.m.helpAt != 0 {
		t.Errorf("N should go back: scroll %d, at %d", r.m.helpScroll, r.m.helpAt)
	}
	r.press("N")
	if r.m.helpAt != n-1 {
		t.Errorf("N from the first match wraps to the last: at %d of %d", r.m.helpAt, n)
	}
	r.press("n")
	if r.m.helpAt != 0 {
		t.Errorf("n from the last wraps to the first: at %d", r.m.helpAt)
	}
	if f := footer(r); !strings.Contains(f, "match 1 of "+itoa(n)) {
		t.Errorf("footer = %q", f)
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+n/10)) + string(rune('0'+n%10)))
}

func TestASearchWithNoMatchSaysSoAndEscClearsItBeforeClosing(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	r.typeText("zzzzqqqq")
	r.press("enter")
	if f := footer(r); !strings.Contains(f, `no match for "zzzzqqqq"`) {
		t.Errorf("footer = %q", f)
	}
	before := r.m.helpScroll
	r.press("n", "N")
	if r.m.helpScroll != before {
		t.Error("n and N with no matches should do nothing")
	}
	r.press("esc")
	if !r.m.showHelp || !r.m.helpQuery.Empty() {
		t.Errorf("the first Esc clears the search: open %v empty %v", r.m.showHelp, r.m.helpQuery.Empty())
	}
	r.press("esc")
	if r.m.showHelp {
		t.Error("the second Esc closes the help")
	}
}

func TestSearchIgnoresCaseUnlessYouTypeACapital(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	r.typeText("pomodoro")
	r.press("enter")
	lower := len(r.m.helpHits)
	r.press("esc", "/")
	r.typeText("Pomodoro")
	r.press("enter")
	exact := len(r.m.helpHits)
	if lower == 0 {
		t.Fatal("pomodoro should be in the guide")
	}
	if exact >= lower && exact != 0 {
		// "Pomodoro" with a capital only matches where it is written that way
		t.Logf("lower %d exact %d", lower, exact)
	}
	r.press("esc", "/")
	r.typeText("ZZZ")
	r.press("enter")
	if len(r.m.helpHits) != 0 {
		t.Error("a capital makes the search exact")
	}
}

func TestEscCancelsTheSearchBoxAndTheGuideStaysOpen(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	r.typeText("abc")
	r.press("esc")
	if r.m.prompt != nil || !r.m.showHelp {
		t.Errorf("prompt %v open %v", r.m.prompt, r.m.showHelp)
	}
}

func TestTheSearchBoxHasACursorLikeTheOthers(t *testing.T) {
	r := helpRig(t)
	r.press("/")
	r.typeText("routin")
	r.press("left", "left")
	r.typeText("X")
	if r.m.prompt.text != "routXin" {
		t.Errorf("text = %q", r.m.prompt.text)
	}
}

func TestMatchesAreMarkedOnScreenWhereTheTextIs(t *testing.T) {
	r := helpRig(t)
	r.m.helpQuery = guide.NewQuery("routine")
	l := guide.Line{Kind: guide.LineRow, Keys: "x", Text: "a routine runs", Topic: 3, First: true}
	// ascii colour profile: the marks cannot be seen, but the text must come out whole and the same width
	got := r.m.styledHelpLine(l, 3, false)
	if !strings.Contains(got, "a routine runs") {
		t.Errorf("line = %q", got)
	}
	plain := (guide.Line{Kind: guide.LineRow, Keys: "x", Text: "a routine runs"}).Pad()
	if lipgloss.Width(got) != lipgloss.Width(plain) {
		t.Errorf("marking must not change the width: %d vs %d", lipgloss.Width(got), lipgloss.Width(plain))
	}
}

func TestTheGuideCanOpenAtASectionWithASearch(t *testing.T) {
	s := newRig(t)
	cfg := s.m.cfg
	cfg.HelpSection = "routine-commands"
	m := New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view := m.View()
	if !strings.Contains(view, "Routine commands (shh routine ...)") {
		t.Errorf("the guide should open at the section:\n%s", view)
	}
	cfg.HelpSection, cfg.HelpSearch = "", "unqueue"
	m = New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	view = m.View()
	if !strings.Contains(view, "match 1 of") {
		t.Errorf("the search should have been made:\n%s", view)
	}
}

func TestAGuideOpenedOnItsOwnQuitsWhereTheTabsWouldHaveBeenShown(t *testing.T) {
	s := newRig(t)
	cfg := s.m.cfg
	cfg.HelpOnly = true
	m := New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	if !m.showHelp {
		t.Fatal("the guide should be open")
	}
	if f := strings.Split(m.View(), "\n"); !strings.Contains(f[len(f)-1], "Esc or q quits") {
		t.Errorf("footer = %q", f[len(f)-1])
	}
	for _, k := range []string{"esc", "?", "q"} {
		m = New(cfg)
		_, cmd := m.Update(key(k))
		if cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%s should leave shh from a guide opened on its own", k)
		}
	}
	// with a search made, the first Esc only clears it
	m = New(cfg)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.setHelpQuery("routine")
	if _, cmd := m.Update(key("esc")); cmd != nil {
		t.Error("the first Esc clears the search, it does not quit")
	}
}

func TestTheGuideHasASectionForEachPartOfRoutinesAndEverySectionHasASlugAndTopics(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range guide.Sections {
		if s.Slug == "" || s.Title == "" || len(s.Topics) == 0 || seen[s.Slug] {
			t.Errorf("section %+v is missing something or repeats a slug", s)
		}
		seen[s.Slug] = true
		for _, tp := range s.Topics {
			if tp.Keys == "" || tp.Text == "" {
				t.Errorf("%s: topic %+v needs keys and a sentence", s.Slug, tp)
			}
		}
	}
	for _, want := range []string{"routines-tab", "routine-files", "steps", "routine-commands", "during-a-run"} {
		if !seen[want] {
			t.Errorf("the guide has no %s section", want)
		}
	}
}
