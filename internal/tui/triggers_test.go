package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
)

const withTriggers = `{"routines": {
  "morning": {"steps": ["75% 25m work", "off 5m break"], "triggers": ["weekdays except fri at 09:00 until 12:00 --reminder 5m", "fri at 10:00 --ask"]},
  "plain": {"steps": ["75% 25m work"]},
  "off": {"steps": ["75% 25m work"], "triggers": ["mon at 09:00 --disabled"]}
}}`

// triggerRows moves the editor's cursor to the first trigger row.
func (r *rig) toTriggerRows() {
	r.t.Helper()
	for i := 0; i < 40 && r.m.editor.current().kind != rowTrigger && r.m.editor.current().kind != rowAddTrigger; i++ {
		r.press("j")
	}
}

func TestTheRoutinesListShowsTriggersAndTheNextStart(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	view := r.view()
	for _, want := range []string{"⏰ next ", " · mon-thu at 09:00 until 12:00 --reminder 5m · fri at 10:00 --ask", "⏰ all switched off · mon at 09:00 --disabled"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list should show %q:\n%s", want, view)
		}
	}
	if strings.Count(view, "⏰") != 2 {
		t.Errorf("only routines with triggers get the line:\n%s", view)
	}
}

func TestTheWorkersWordOnTheNextStartIncludingASkipIsShown(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	start := r.m.cfg.Now().Add(2 * time.Hour)
	then := start.Add(24 * time.Hour)
	r.worker.view = session.StatusView{Upcoming: []session.UpcomingStart{{Routine: "morning", Trigger: "x", Start: start, Skipped: true, Then: &then}}}
	r.refresh()
	if v := r.view(); !strings.Contains(v, "SKIPPED, then tomorrow") {
		t.Errorf("a skipped start should say so, and what comes after:\n%s", v)
	}
}

func TestSkipKeyLeavesOutTheNextStartAndTakesItBack(t *testing.T) {
	r, fr := routinesRig(t)
	r.m.saved, _ = routine.Parse("routines.json", []byte(withTriggers))
	r.m.cfg.Saved = func() ([]routine.Routine, error) { return routine.Parse("routines.json", []byte(withTriggers)) }
	r.press("S")
	if got := strings.Join(fr.calls, ","); got != "skip morning" || !strings.Contains(r.m.messageLine(), "$ shh routine skip morning") {
		t.Errorf("calls %s message %q", got, r.m.messageLine())
	}
	start := r.m.cfg.Now().Add(2 * time.Hour)
	r.worker.view = session.StatusView{Upcoming: []session.UpcomingStart{{Routine: "morning", Start: start, Skipped: true}}}
	r.refresh()
	r.press("S")
	if got := strings.Join(fr.calls, ","); got != "skip morning,unskip morning" || !strings.Contains(r.m.messageLine(), "skip --undo morning") {
		t.Errorf("calls %s message %q", got, r.m.messageLine())
	}
	r.press("j", "S")
	if !strings.Contains(r.m.notice, "no trigger switched on") || len(fr.calls) != 2 {
		t.Errorf("notice %q calls %v", r.m.notice, fr.calls)
	}
	r.press("j", "S") // the routine whose triggers are all disabled
	if !strings.Contains(r.m.notice, "no trigger switched on") {
		t.Errorf("notice %q", r.m.notice)
	}
}

func TestTheEditorListsTriggersAndEnterOpensTheForm(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	if v := r.view(); !strings.Contains(v, "Triggers") || !strings.Contains(v, "mon-thu at 09:00 until 12:00 --reminder 5m") || !strings.Contains(v, "add a trigger") {
		t.Errorf("the editor should list the triggers:\n%s", v)
	}
	r.press("enter")
	if r.m.trigForm == nil || r.m.trigForm.index != 0 {
		t.Fatalf("form = %+v", r.m.trigForm)
	}
	v := r.view()
	for _, want := range []string{"Trigger 1 of morning", "as words: mon-thu at 09:00 until 12:00 --reminder 5m", "[x]Mon", "[x]Thu", "[ ]Fri", "[ ]Sat", "Start", "09:00", "Stop", "12:00", "Reminder", "5m before", "Ask first", "When busy", "Grace", "15m late at most", "Stop alert", "Switched on", "next start:"} {
		if !strings.Contains(v, want) {
			t.Errorf("the form should show %q:\n%s", want, v)
		}
	}
	if h := r.m.messageLine(); !strings.Contains(h, "h/l pick a day") || !strings.Contains(h, "Space toggles it") {
		t.Errorf("hint = %q", h)
	}
}

func TestTheWeekRowTogglesDaysWithTheCursorAndPresets(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter") // mon-thu
	f := r.m.trigForm
	r.press("l", "l", "l", "l") // to friday
	r.press(" ")
	if !f.hasDay(time.Friday) || !strings.Contains(r.view(), "as words: weekdays at 09:00 until 12:00") {
		t.Errorf("space should tick friday: %v\n%s", f.trigger.Days, r.view())
	}
	r.press(" ")
	if f.hasDay(time.Friday) {
		t.Error("and untick it")
	}
	r.press("e")
	if len(f.trigger.Days) != 2 || !strings.Contains(r.view(), "as words: weekends at 09:00") {
		t.Errorf("e is the weekend: %v", f.trigger.Days)
	}
	r.press("w")
	if len(f.trigger.Days) != 5 {
		t.Errorf("w is weekdays: %v", f.trigger.Days)
	}
	r.press("a")
	if len(f.trigger.Days) != 7 || !strings.Contains(r.view(), "as words: daily at 09:00") {
		t.Errorf("a is every day: %v", f.trigger.Days)
	}
	r.press("h", "h", "h", "h") // back to monday (the cursor was on friday)
	r.press("h")                // and one more wraps to sunday
	r.press(" ")
	if f.hasDay(time.Sunday) {
		t.Error("h from monday wraps to sunday")
	}
}

func TestPickingNoDaysIsRefusedWhenAccepting(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter")
	for _, day := range []string{"Mon", "Tue", "Wed", "Thu"} {
		r.press(" ", "l")
		_ = day
	}
	if !strings.Contains(r.view(), "pick at least one day") {
		t.Errorf("the form should say a day is needed:\n%s", r.view())
	}
	r.press("s")
	if r.m.trigForm == nil || !strings.Contains(r.m.notice, "pick at least one day") {
		t.Errorf("accepting with no days should be refused: %q", r.m.notice)
	}
}

func TestFieldsOfTheFormAreTypedAndCheckedAsAWhole(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter", "j", "enter") // start
	r.typeInto("8:15")
	if r.m.trigForm.trigger.At != "08:15" || r.m.prompt != nil {
		t.Errorf("start = %q", r.m.trigForm.trigger.At)
	}
	r.press("j", "enter") // the stop row
	r.typeInto("08:00")   // a stop before the start
	if r.m.prompt == nil || !strings.Contains(r.m.prompt.err, "not after the start") {
		t.Errorf("the box should keep the bad time with the reason: %+v", r.m.prompt)
	}
	r.press("esc")
	if r.m.trigForm.trigger.Until != "12:00" {
		t.Error("a refused value changes nothing")
	}
}

func TestEachFieldCanBeEditedAndCleared(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter")
	f := r.m.trigForm
	step := func(row int) {
		for f.cursor < row {
			r.press("j")
		}
		for f.cursor > row {
			r.press("k")
		}
		r.press("enter")
	}
	step(tfStop)
	r.typeInto("") // clear
	if f.trigger.Until != "" || !strings.Contains(r.view(), "(none: it runs until the routine ends)") {
		t.Errorf("stop = %q", f.trigger.Until)
	}
	step(tfReminder)
	r.typeInto("10m")
	if f.trigger.Reminder != 10*time.Minute {
		t.Errorf("reminder = %v", f.trigger.Reminder)
	}
	step(tfReminder)
	r.typeInto("0")
	if f.trigger.Reminder != 0 {
		t.Errorf("0 clears the reminder: %v", f.trigger.Reminder)
	}
	step(tfAsk)
	if !f.trigger.Ask {
		t.Error("Enter toggles ask")
	}
	step(tfBusy)
	if f.trigger.WhenBusy != routine.BusySkip {
		t.Errorf("busy = %v", f.trigger.WhenBusy)
	}
	step(tfBusy)
	step(tfBusy)
	if f.trigger.WhenBusy != routine.BusyAsk {
		t.Errorf("busy cycles round: %v", f.trigger.WhenBusy)
	}
	step(tfGrace)
	r.typeInto("0")
	if f.trigger.Grace != 0 || !strings.Contains(r.view(), "0 (a start that is not on time is missed)") {
		t.Errorf("grace = %v", f.trigger.Grace)
	}
	step(tfGrace)
	r.typeInto("7h")
	if r.m.prompt == nil || !strings.Contains(r.m.prompt.err, "up to 6h") {
		t.Errorf("grace too long: %+v", r.m.prompt)
	}
	r.press("esc")
	step(tfUntilAlert)
	if f.trigger.UntilAlert != routine.AlertSound {
		t.Errorf("until alert = %v", f.trigger.UntilAlert)
	}
	step(tfEnabled)
	if !f.trigger.Disabled || !strings.Contains(r.view(), "Switched on  no") {
		t.Errorf("enabled toggled off: %+v", f.trigger)
	}
}

func TestAcceptingPutsTheTriggerBackAndSavingWritesItToTheFile(t *testing.T) {
	r, path, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter", "j", "enter")
	r.typeInto("08:30")
	r.press("s")
	if r.m.trigForm != nil {
		t.Fatalf("s should accept and close the form: notice %q", r.m.notice)
	}
	if got := r.m.editor.draft.Triggers[0].Command(); got != "mon-thu at 08:30 until 12:00 --reminder 5m" || !r.m.editor.dirty {
		t.Errorf("trigger = %q", got)
	}
	r.press("s")
	rs, err := routine.Load(*path)
	if err != nil {
		t.Fatal(err)
	}
	morning, _ := routine.Find(rs, "morning")
	if len(morning.Triggers) != 2 || morning.Triggers[0].At != "08:30" {
		t.Errorf("triggers = %+v", morning.Triggers)
	}
}

func TestEscLeavesTheFormWithoutChangingTheRoutine(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("enter", "a") // every day... on the days row
	r.press("esc")
	if r.m.trigForm != nil || r.m.editor == nil {
		t.Fatal("Esc should close the form and keep the editor")
	}
	if got := r.m.editor.draft.Triggers[0].Command(); got != "mon-thu at 09:00 until 12:00 --reminder 5m" || r.m.editor.dirty {
		t.Errorf("trigger = %q dirty %v", got, r.m.editor.dirty)
	}
}

func TestANewTriggerStartsAsWeekdaysAt9AndAddsToTheEnd(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	r.press("a") // a on a trigger row adds a new one
	if r.m.trigForm == nil || r.m.trigForm.index != -1 || r.m.trigForm.trigger.Command() != "weekdays at 09:00" {
		t.Fatalf("form = %+v", r.m.trigForm)
	}
	if !strings.Contains(r.view(), "New trigger") {
		t.Errorf("view:\n%s", r.view())
	}
	r.press("s")
	if got := len(r.m.editor.draft.Triggers); got != 3 {
		t.Fatalf("triggers = %d", got)
	}
	if row := r.m.editor.current(); row.kind != rowTrigger || row.index != 2 {
		t.Errorf("the cursor should be on the new trigger: %+v", row)
	}
}

func TestATriggerCanBeDeletedAndAddCommandIncludesTriggers(t *testing.T) {
	r, _, _ := editorRigText(t, withTriggers)
	r.press("e")
	r.toTriggerRows()
	if cmd := addCommand(r.m.editor.draft, true); !strings.Contains(cmd, "-t 'mon-thu at 09:00 until 12:00 --reminder 5m'") || !strings.Contains(cmd, "-t 'fri at 10:00 --ask'") {
		t.Errorf("command = %s", cmd)
	}
	r.press("d")
	if got := len(r.m.editor.draft.Triggers); got != 1 {
		t.Errorf("triggers = %d", got)
	}
	r.press("d")
	if got := len(r.m.editor.draft.Triggers); got != 0 {
		t.Errorf("a routine may have no triggers: %d", got)
	}
}

func TestANewRoutineCanGetATriggerBeforeItIsSaved(t *testing.T) {
	r, path, _ := editorRigText(t, withTriggers)
	r.press("a", "enter")
	r.typeInto("late")
	r.toTriggerRows()
	r.press("enter") // add a trigger
	if r.m.trigForm == nil {
		t.Fatal("Enter on add a trigger should open the form")
	}
	r.press("s", "s")
	rs, err := routine.Load(*path)
	if err != nil {
		t.Fatal(err)
	}
	late, ok := routine.Find(rs, "late")
	if !ok || len(late.Triggers) != 1 || late.Triggers[0].Command() != "weekdays at 09:00" {
		t.Errorf("late = %+v", late)
	}
}

func TestTheEditorSetsTheRoutinesBannerStyleFromFlags(t *testing.T) {
	r, path, _ := editorRigText(t, withTriggers)
	r.press("e")
	for i := 0; i < 5; i++ {
		r.press("j") // the Banner style row
	}
	if row := r.m.editor.current(); row.kind != rowStyle {
		t.Fatalf("row = %+v", row)
	}
	if v := r.view(); !strings.Contains(v, "Banner style") || !strings.Contains(v, "(as configured)") {
		t.Errorf("view:\n%s", v)
	}
	r.press("enter")
	if h := r.m.prompt.hint(); !strings.Contains(h, "shh alert colors") {
		t.Errorf("the box should point at the palette: %q", h)
	}
	r.typeInto("--color 300")
	if r.m.prompt == nil || !strings.Contains(r.m.prompt.err, "color") {
		t.Errorf("a color past 255 stays in the box: %+v", r.m.prompt)
	}
	r.press("esc")
	r.press("enter")
	r.typeInto("--color 33 --size large --sound Hero")
	if r.m.prompt != nil || *r.m.editor.draft.Style.Color != 33 || *r.m.editor.draft.Style.Size != "large" {
		t.Fatalf("style = %+v", r.m.editor.draft.Style)
	}
	if v := r.view(); !strings.Contains(v, "--color 33 --size large --sound Hero") {
		t.Errorf("view:\n%s", v)
	}
	if got := addCommand(r.m.editor.draft, true); !strings.Contains(got, `--style '--color 33 --size large --sound Hero'`) {
		t.Errorf("command = %s", got)
	}
	r.press("s")
	rs, err := routine.Load(*path)
	if err != nil {
		t.Fatal(err)
	}
	morning, _ := routine.Find(rs, "morning")
	if morning.Style.Color == nil || *morning.Style.Color != 33 {
		t.Errorf("style = %+v", morning.Style)
	}
	// clearing it
	r.press("e")
	for i := 0; i < 5; i++ {
		r.press("j")
	}
	r.press("enter")
	r.typeInto("")
	if !r.m.editor.draft.Style.IsZero() {
		t.Errorf("an empty box clears the style: %+v", r.m.editor.draft.Style)
	}
}
