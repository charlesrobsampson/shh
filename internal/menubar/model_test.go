package menubar_test

import (
	"strings"
	"testing"
	"time"

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
			"shh 28m · 65%", "(escalation 65% (medium) · 28m left) (then back to baseline 30%) - pause plus minus stop off - open"},
		{"paused", session.StatusView{Level: 65, Layers: []session.LayerView{baseline, paused}}, "shh ⏸ 65%",
			"(escalation 65% (medium) · paused, 28m left) (then back to baseline 30%) - resume plus minus stop off - open"},
		{"an escalation until stopped", session.StatusView{Level: 90, Layers: []session.LayerView{indefinite}}, "shh 90%",
			"(escalation 90% (deep) · until stopped) (then off) - stop off - open"},
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

func TestHeadingsShowTheNoteAndWhereTheSessionFallsBackTo(t *testing.T) {
	l := timed
	l.ThenLevel = intp(0)
	l.Note = "deep work"
	got := menubar.Build(session.StatusView{Level: 65, Layers: []session.LayerView{baseline, l}}, "shh")
	if want := `escalation 65% (medium) · 28m left · "deep work"`; got.Items[0].Title != want {
		t.Errorf("heading = %q, want %q", got.Items[0].Title, want)
	}
	if want := "then off"; got.Items[1].Title != want {
		t.Errorf("fallback = %q, want %q: an explicit then level wins over the layer beneath", got.Items[1].Title, want)
	}
}

func TestOnlyWhatIsInForceIsListed(t *testing.T) {
	covered := session.LayerView{Kind: "escalation", Percent: 65}
	top := session.LayerView{Kind: "escalation", Percent: 40}
	got := menubar.Build(session.StatusView{Level: 40, Layers: []session.LayerView{baseline, covered, top}}, "shh")
	var text []string
	for _, it := range got.Items {
		text = append(text, it.Title)
	}
	all := strings.Join(text, "|")
	if strings.Contains(all, "baseline") {
		t.Errorf("menu %q lists the baseline two layers down; only the session in force and where it falls back to belong there", all)
	}
	if !strings.Contains(all, "escalation 40%") || !strings.Contains(all, "then back to escalation 65%") {
		t.Errorf("menu %q should show the 40%% session and what it falls back to", all)
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

func TestARunningRoutineShowsItsStepInTheTitleAndItsOwnMenuItems(t *testing.T) {
	work := session.LayerView{Kind: "escalation", Percent: 75, Timed: true, RemainingSecs: 14 * 60}
	run := &session.RoutineView{Name: "pomodoro", Position: 2, Total: 9, Step: "work (2/4)"}
	v := menubar.Build(session.StatusView{Level: 75, Layers: []session.LayerView{baseline, work}, Routine: run}, "shh")
	if v.Title != "shh pomodoro · work (2/4) · 14m" {
		t.Errorf("title = %q", v.Title)
	}
	got := ids(v)
	for _, want := range []string{"(Routine pomodoro · step 3 of 9)", "routine_next", "routine_back", "routine_stop", "pause", "plus", "open"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu %q should have %s", got, want)
		}
	}
	for _, not := range []string{" stop ", " off "} {
		if strings.Contains(" "+got+" ", not) {
			t.Errorf("menu %q should not offer to stop the session by hand, the worker refuses it while a routine runs", got)
		}
	}
	paused := work
	paused.Paused = true
	if title := menubar.Build(session.StatusView{Level: 75, Layers: []session.LayerView{paused}, Routine: run}, "shh").Title; title != "shh pomodoro · work (2/4) · ⏸" {
		t.Errorf("paused title = %q", title)
	}
	waiting := &session.RoutineView{Name: "pomodoro", Step: "break (2/4)", Waiting: true}
	w := menubar.Build(session.StatusView{Level: 75, Layers: []session.LayerView{{Kind: "escalation", Percent: 75}}, Routine: waiting}, "shh")
	if w.Title != "shh pomodoro · break (2/4) ?" || !strings.Contains(ids(w), "routine_next") {
		t.Errorf("waiting: %q %s", w.Title, ids(w))
	}
	found := false
	for _, it := range w.Items {
		found = found || it.Title == "Start break (2/4)"
	}
	if !found {
		t.Errorf("while waiting the next item should say it starts the step: %+v", w.Items)
	}
}

func TestTheLastRoutineIsInTheMenuOnlyWhenItAsksForItAndOnlyForAWhile(t *testing.T) {
	end := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	last := &session.RoutineSummary{Name: "pomo", Reason: "finished", Steps: 4, ElapsedSecs: 3600, Held: 3, Let: 1, Ended: end, Where: []string{"menu"}}
	view := func(l *session.RoutineSummary, after time.Duration) menubar.View {
		return menubar.Build(session.StatusView{Now: end.Add(after), LastRoutine: l}, "shh")
	}
	v := view(last, time.Minute)
	if len(v.Items) < 2 || v.Items[0].Title != "Last routine: pomo finished: 4 steps in 1h. Held back 3 notifications, let through 1." {
		t.Errorf("items = %+v", v.Items)
	}
	if got := view(last, 2*time.Hour); strings.Contains(ids(got), "Last routine") {
		t.Errorf("an old summary should have gone from the menu: %s", ids(got))
	}
	banner := *last
	banner.Where = []string{"banner", "ui"}
	if got := view(&banner, time.Minute); strings.Contains(ids(got), "Last routine") {
		t.Errorf("a routine that did not ask for the menu stays out of it: %s", ids(got))
	}
	running := menubar.Build(session.StatusView{Now: end, LastRoutine: last, Routine: &session.RoutineView{Name: "x", Step: "s"}}, "shh")
	if strings.Contains(ids(running), "Last routine") {
		t.Error("while a routine runs the old summary is not shown")
	}
}

func TestAQueuedSessionIsListedUnderTheRoutine(t *testing.T) {
	run := &session.RoutineView{Name: "pomo", Step: "work (1/2)", Total: 5, Queued: `90% (deep) for 30m, "deep block"`}
	v := menubar.Build(session.StatusView{Level: 75, Layers: []session.LayerView{timed}, Routine: run}, "shh")
	if !strings.Contains(ids(v), `(Queued for when it ends: 90% (deep) for 30m, "deep block")`) {
		t.Errorf("menu = %s", ids(v))
	}
}

func TestTheMenuSaysWhenARoutineStartsNextExceptWhileOneRuns(t *testing.T) {
	now := time.Date(2026, 11, 9, 7, 0, 0, 0, time.UTC)
	up := []session.UpcomingStart{{Routine: "morning", Start: now.Add(2 * time.Hour)}, {Routine: "late", Start: now.Add(10 * time.Hour)}}
	v := menubar.Build(session.StatusView{Now: now, Upcoming: up}, "shh")
	if v.Items[0].Title != "Next: morning today 09:00" {
		t.Errorf("items = %+v", v.Items)
	}
	up[0].Skipped = true
	if v = menubar.Build(session.StatusView{Now: now, Upcoming: up}, "shh"); v.Items[0].Title != "Next: morning today 09:00 (skipped)" {
		t.Errorf("items = %+v", v.Items)
	}
	running := menubar.Build(session.StatusView{Now: now, Upcoming: up, Routine: &session.RoutineView{Name: "x", Step: "s"}}, "shh")
	for _, it := range running.Items {
		if strings.HasPrefix(it.Title, "Next:") {
			t.Error("while a routine runs the menu is about it, not the next one")
		}
	}
}
