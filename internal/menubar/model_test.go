package menubar_test

import (
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/menubar"
	"github.com/charlesrobsampson/shh/internal/session"
)

func ids(v menubar.View) string {
	var out []string
	for _, it := range v.Items {
		switch {
		case it.Separator:
			out = append(out, "-")
		case it.ID == "":
			out = append(out, "("+it.Title+")")
		default:
			out = append(out, it.ID)
		}
	}
	return strings.Join(out, " ")
}

func intp(n int) *int { return &n }

var (
	baseline = session.LayerView{Kind: "baseline", Percent: 30}
	timed    = session.LayerView{Kind: "escalation", Percent: 65, Timed: true, RemainingSecs: 28 * 60}
)

func TestTitleAndMenuForEveryKindOfState(t *testing.T) {
	paused := timed
	paused.Paused = true
	indefinite := session.LayerView{Kind: "escalation", Percent: 90}
	tests := []struct {
		name      string
		status    session.StatusView
		wantTitle string
		wantIDs   string
	}{
		{"off", session.StatusView{}, "shh", "(Focus is off) - open"},
		{"a baseline alone", session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, "shh 30%",
			"(baseline 30%) - off - open"},
		{"an escalation with time left", session.StatusView{Level: 65, Layers: []session.LayerView{baseline, timed}},
			"shh 28m · 65%", "(baseline 30%) (escalation 65% (medium) · 28m left) - pause plus minus stop off - open"},
		{"paused", session.StatusView{Level: 65, Layers: []session.LayerView{baseline, paused}}, "shh ⏸ 65%",
			"(baseline 30%) (escalation 65% (medium) · paused, 28m left) - resume plus minus stop off - open"},
		{"an escalation until stopped", session.StatusView{Level: 90, Layers: []session.LayerView{indefinite}}, "shh 90%",
			"(escalation 90% (deep) · until stopped) - stop off - open"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := menubar.Build(tt.status, "shh")
			if got.Title != tt.wantTitle {
				t.Errorf("title = %q, want %q", got.Title, tt.wantTitle)
			}
			if ids(got) != tt.wantIDs {
				t.Errorf("menu = %s\nwant   %s", ids(got), tt.wantIDs)
			}
		})
	}
}

func TestTheLabelComesFromTheConfiguration(t *testing.T) {
	got := menubar.Build(session.StatusView{Level: 30, Layers: []session.LayerView{baseline}}, "🔇 focus")
	if got.Title != "🔇 focus 30%" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestShortCountsArePaddedSoTheTitleDoesNotChangeWidth(t *testing.T) {
	title := func(secs int) string {
		l := timed
		l.RemainingSecs = secs
		return menubar.Build(session.StatusView{Level: 65, Layers: []session.LayerView{l}}, "shh").Title
	}
	// Counts of 9s and 10s must take the same room, as must 5m and 38m.
	for _, pair := range [][2]int{{9, 10}, {5 * 60, 38 * 60}, {0, 59}} {
		a, b := []rune(title(pair[0])), []rune(title(pair[1]))
		if len(a) != len(b) {
			t.Errorf("%q and %q have different lengths (%d, %d)", string(a), string(b), len(a), len(b))
		}
	}
	if got := title(9); !strings.Contains(got, " \u20079s") {
		t.Errorf("title = %q, want one figure space in front of 9s, to match the width of 10s", got)
	}
}

func TestHeadingsShowTheThenLevelAndNote(t *testing.T) {
	l := timed
	l.ThenLevel = intp(0)
	l.Note = "deep work"
	got := menubar.Build(session.StatusView{Level: 65, Layers: []session.LayerView{l}}, "shh")
	want := `escalation 65% (medium) · 28m left · then off · "deep work"`
	if got.Items[0].Title != want {
		t.Errorf("heading = %q, want %q", got.Items[0].Title, want)
	}
}

func TestOnlyActionsAreClickable(t *testing.T) {
	got := menubar.Build(session.StatusView{Level: 65, Layers: []session.LayerView{baseline, timed}}, "shh")
	for _, it := range got.Items {
		if it.ID == "" && !it.Separator && it.Title == "" {
			t.Errorf("an item with nothing in it: %+v", it)
		}
	}
}
