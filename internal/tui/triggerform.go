package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/routine"
)

// The rows of the trigger form.
const (
	tfDays = iota
	tfStart
	tfStop
	tfReminder
	tfAsk
	tfBusy
	tfGrace
	tfUntilAlert
	tfEnabled
	tfRows
)

var weekdayCells = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

// triggerForm is one trigger of a routine being changed: the days as a row of checkboxes, and the rest as fields. It is applied to the
// routine being edited when it is accepted, and dropped when it is not.
type triggerForm struct {
	trigger routine.Trigger
	index   int // which of the routine's triggers it is, or -1 for a new one
	cursor  int // the row
	day     int // the day under the cursor on the days row
}

// openTriggerForm opens the form on a trigger of the routine being edited, or on a new one when index is -1.
func (m *Model) openTriggerForm(index int) {
	e := m.editor
	var t routine.Trigger
	if index >= 0 {
		t = e.draft.Triggers[index]
		t.Days = slices.Clone(t.Days)
	} else {
		t, _ = routine.ParseTrigger("weekdays at 09:00")
	}
	m.trigForm = &triggerForm{trigger: t, index: index}
}

func (f *triggerForm) hasDay(d time.Weekday) bool { return slices.Contains(f.trigger.Days, d) }

func (f *triggerForm) setDays(days []time.Weekday) {
	f.trigger.Days = nil
	for _, d := range weekdayCells { // Monday first, each once
		if slices.Contains(days, d) {
			f.trigger.Days = append(f.trigger.Days, d)
		}
	}
}

func (f *triggerForm) toggleDay(d time.Weekday) {
	if f.hasDay(d) {
		f.setDays(slices.DeleteFunc(slices.Clone(f.trigger.Days), func(x time.Weekday) bool { return x == d }))
		return
	}
	f.setDays(append(slices.Clone(f.trigger.Days), d))
}

// valid says whether the trigger, as it is, could be saved, and why not.
func (f *triggerForm) valid() error {
	if len(f.trigger.Days) == 0 {
		return fmt.Errorf("pick at least one day")
	}
	_, err := routine.ParseTrigger(f.trigger.Command())
	return err
}

// triggerFormKey handles every key while the form is open.
func (m *Model) triggerFormKey(key string) {
	f := m.trigForm
	switch key {
	case "ctrl+c", "esc":
		m.trigForm = nil
	case "j", "down":
		f.cursor = min(f.cursor+1, tfRows-1)
	case "k", "up":
		f.cursor = max(f.cursor-1, 0)
	case "g", "home":
		f.cursor = 0
	case "G", "end":
		f.cursor = tfRows - 1
	case "s", "ctrl+s":
		m.acceptTriggerForm()
	case "h", "left":
		if f.cursor == tfDays {
			f.day = (f.day + 6) % 7
		}
	case "l", "right":
		if f.cursor == tfDays {
			f.day = (f.day + 1) % 7
		}
	case "w":
		if f.cursor == tfDays {
			f.setDays(weekdayCells[:5])
		}
	case "e":
		if f.cursor == tfDays {
			f.setDays(weekdayCells[5:])
		}
	case "a":
		if f.cursor == tfDays {
			f.setDays(weekdayCells)
		}
	case "x", " ", "enter":
		m.changeTriggerRow(key != "x")
	}
}

// changeTriggerRow does what Enter or Space means on the row: toggle a day, cycle a choice, or open the box for a value.
func (m *Model) changeTriggerRow(enter bool) {
	f := m.trigForm
	t := &f.trigger
	switch f.cursor {
	case tfDays:
		f.toggleDay(weekdayCells[f.day])
	case tfStart:
		m.openField("start", t.At, "the time it starts, 24 hour, such as 09:00 · Enter keeps it · Esc cancels", editorRow{rowTriggerField, tfStart})
	case tfStop:
		m.openField("stop", t.Until, "the time it is cut off, such as 12:00 (after the start; empty for none) · Enter keeps it · Esc cancels", editorRow{rowTriggerField, tfStop})
	case tfReminder:
		initial := ""
		if t.Reminder > 0 {
			initial = routine.DurationWord(t.Reminder)
		}
		m.openField("reminder", initial, "a banner this long before the start, such as 5m (empty for none) · Enter keeps it · Esc cancels", editorRow{rowTriggerField, tfReminder})
	case tfAsk:
		t.Ask = !t.Ask
	case tfBusy:
		order := []routine.WhenBusy{routine.BusyAsk, routine.BusySkip, routine.BusyStart}
		t.WhenBusy = order[(slices.Index(order, t.WhenBusy)+1)%len(order)]
		m.notice = map[routine.WhenBusy]string{
			routine.BusyAsk:   "when busy: ask: a banner says what is running, with Start now and Skip",
			routine.BusySkip:  "when busy: skip: leave what is running alone and do not start",
			routine.BusyStart: "when busy: start: start anyway, ending a running routine (a session of yours stays underneath)",
		}[t.WhenBusy]
	case tfGrace:
		initial := "0"
		if t.Grace > 0 {
			initial = routine.DurationWord(t.Grace)
		}
		m.openField("grace", initial, "how late a start can still be made, such as 15m (0 for none, up to 6h) · Enter keeps it · Esc cancels", editorRow{rowTriggerField, tfGrace})
	case tfUntilAlert:
		order := []routine.Alert{routine.AlertNone, routine.AlertSound, routine.AlertBanner}
		t.UntilAlert = order[(slices.Index(order, t.UntilAlert)+1)%len(order)]
		if t.UntilAlert != routine.AlertNone && t.Until == "" {
			m.notice = "an until alert needs a stop time: set one on the Stop row"
		}
	case tfEnabled:
		t.Disabled = !t.Disabled
	}
}

// submitTriggerField applies a value typed for a row of the form. A value that cannot be used stays in the box with the reason.
func (m *Model) submitTriggerField(p *prompt) {
	f := m.trigForm
	text := strings.TrimSpace(p.text)
	t := f.trigger
	switch p.row.index {
	case tfStart:
		at, ok := routine.NormalClock(text)
		if !ok {
			p.err = "a time of day such as 09:00"
			return
		}
		t.At = at
	case tfStop:
		t.Until = ""
		if text != "" {
			until, ok := routine.NormalClock(text)
			if !ok {
				p.err = "a time of day such as 12:00, or empty"
				return
			}
			t.Until = until
		}
	case tfReminder:
		t.Reminder = 0
		if text != "" && text != "0" {
			d, err := decide.ParseDuration(text)
			if err != nil || d <= 0 {
				p.err = "a length such as 5m, or empty"
				return
			}
			t.Reminder = d
		}
	case tfGrace:
		g, err := routine.ParseGrace(text)
		if err != nil {
			p.err = "0, or a length such as 15m (up to 6h)"
			return
		}
		t.Grace = g
	}
	// The whole trigger must still hold together (a stop after the start, say): check it as the file would.
	check := t
	if len(check.Days) == 0 {
		check.Days = weekdayCells[:1]
	}
	if _, err := routine.ParseTrigger(check.Command()); err != nil {
		p.err = oneLine(err.Error())
		return
	}
	f.trigger = t
	m.prompt = nil
}

// acceptTriggerForm puts the trigger back in the routine being edited.
func (m *Model) acceptTriggerForm() {
	f := m.trigForm
	if err := f.valid(); err != nil {
		m.notice = oneLine(err.Error())
		return
	}
	// Written and read back, so it is exactly what the file will hold.
	t, err := routine.ParseTrigger(f.trigger.Command())
	if err != nil {
		m.notice = oneLine(err.Error())
		return
	}
	e := m.editor
	if f.index >= 0 {
		e.draft.Triggers[f.index] = t
	} else {
		e.draft.Triggers = append(e.draft.Triggers, t)
		e.cursor = rowIndex(e, editorRow{rowTrigger, len(e.draft.Triggers) - 1})
	}
	e.dirty = true
	m.trigForm = nil
}

// triggerFormView draws the form in place of the editor.
func (m *Model) triggerFormView() string {
	f := m.trigForm
	t := f.trigger
	title := "New trigger"
	if f.index >= 0 {
		title = fmt.Sprintf("Trigger %d of %s", f.index+1, m.editorTitleName())
	}
	lines := []string{"", "  " + bold.Render(title), ""}
	if err := f.valid(); err != nil {
		lines = append(lines, "  "+dim.Render("as words: ")+errStyle.Render("✗ "+oneLine(err.Error())), "")
	} else {
		lines = append(lines, "  "+dim.Render("as words: ")+t.Command(), "")
	}
	for row := 0; row < tfRows; row++ {
		lines = append(lines, m.triggerRow(f, row))
	}
	if next, _, ok := (routine.Routine{Triggers: []routine.Trigger{t}}).NextStart(m.cfg.Now()); ok && len(t.Days) > 0 && !t.Disabled {
		lines = append(lines, "", "  "+dim.Render("next start: "+routine.StartWhen(next, m.cfg.Now())))
	}
	return pad(lines, m.bodyHeight(), m.width)
}

func (m *Model) editorTitleName() string {
	if m.editor != nil && m.editor.draft.Name != "" {
		return m.editor.draft.Name
	}
	return "this routine"
}

func onOff(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// triggerRow draws one row of the form: the days as checkboxes with a cursor, the others as a label and a value.
func (m *Model) triggerRow(f *triggerForm, row int) string {
	t := f.trigger
	selected := row == f.cursor
	cell := func(style lipgloss.Style, text string) string { return style.Reverse(selected).Render(text) }
	cursor := " "
	if selected {
		cursor = ">"
	}
	var label, value string
	switch row {
	case tfDays:
		label = "Days"
		var cells []string
		for i, d := range weekdayCells {
			box := "[ ]"
			if f.hasDay(d) {
				box = "[x]"
			}
			mark := " "
			if selected && i == f.day {
				mark = ">" // the cell under the cursor
			}
			cells = append(cells, mark+box+d.String()[:3])
		}
		value = strings.Join(cells, "")
	case tfStart:
		label, value = "Start", t.At
	case tfStop:
		label, value = "Stop", t.Until
		if value == "" {
			value = "(none: it runs until the routine ends)"
		}
	case tfReminder:
		label, value = "Reminder", "(none)"
		if t.Reminder > 0 {
			value = routine.DurationWord(t.Reminder) + " before"
		}
	case tfAsk:
		label, value = "Ask first", onOff(t.Ask)+"  (a Start / Skip banner instead of starting)"
	case tfBusy:
		label, value = "When busy", string(t.WhenBusy)+"  (Enter changes it: ask, skip, start)"
	case tfGrace:
		label, value = "Grace", "0 (a start that is not on time is missed)"
		if t.Grace > 0 {
			value = routine.DurationWord(t.Grace) + " late at most"
		}
	case tfUntilAlert:
		label, value = "Stop alert", string(t.UntilAlert)+"  (Enter changes it: none, sound, banner)"
	case tfEnabled:
		label, value = "Switched on", onOff(!t.Disabled)
	}
	out := cell(bold, " "+cursor+"  ") + cell(bold, fmt.Sprintf("%-12s", label)) + cell(lipgloss.NewStyle(), " "+oneLine(value))
	if gap := m.width - lipgloss.Width(out); selected && gap > 0 {
		out += cell(lipgloss.NewStyle(), strings.Repeat(" ", gap))
	}
	return out
}

// triggerFormHint is the line under the form.
func triggerFormHint(f *triggerForm) string {
	if f.cursor == tfDays {
		return "h/l pick a day · Space toggles it · w weekdays · e weekends · a every day · j/k rows · s accept · Esc cancel"
	}
	return "j/k rows · Enter change · s accept · Esc cancel"
}
