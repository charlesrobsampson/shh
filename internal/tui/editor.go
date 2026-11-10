package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/routine"
)

// rowKind says what a row of the routine editor is.
type rowKind int

const (
	rowName rowKind = iota
	rowDescription
	rowRepeat
	rowWaiting
	rowSummary
	rowStyle     // the routine's banner style
	rowLog       // where to say that notifications were flagged during a run
	rowStep      // a step of the routine; index says which
	rowAddStep   // "+ add a step"
	rowFinish    // a step of the finish; index says which
	rowAddFinish // "+ add a finish step"
	rowTrigger   // a trigger of the routine; index says which
	rowAddTrigger
	rowTriggerField // a value of the trigger form, typed in a box; index says which row of it
)

type editorRow struct {
	kind  rowKind
	index int
}

// routineEditor is a routine being made or changed, held here until it is saved.
type routineEditor struct {
	draft    routine.Routine
	original string // the name it was saved under, "" for a new one
	cursor   int
	dirty    bool
	// discard is set after Esc on a routine with unsaved changes, so a second Esc throws them away.
	discard bool
}

// logChoices are the combinations of places to say that notifications were flagged during a run, in the order Enter goes through them.
var logChoices = [][]routine.Where{
	nil,
	{routine.WhereBanner},
	{routine.WhereMenu},
	{routine.WhereUI},
	{routine.WhereBanner, routine.WhereMenu},
	{routine.WhereBanner, routine.WhereUI},
	{routine.WhereMenu, routine.WhereUI},
	{routine.WhereBanner, routine.WhereMenu, routine.WhereUI},
}

// summaryChoices are the combinations of places a summary can be shown, in the order Enter goes through them.
var summaryChoices = [][]routine.Where{
	{routine.WhereBanner, routine.WhereUI},
	{routine.WhereBanner},
	{routine.WhereMenu},
	{routine.WhereUI},
	{routine.WhereBanner, routine.WhereMenu},
	{routine.WhereMenu, routine.WhereUI},
	{routine.WhereBanner, routine.WhereMenu, routine.WhereUI},
	{},
}

// logText says where a run's flagged notifications are reported.
func logText(w []routine.Where) string {
	if len(w) == 0 {
		return "nowhere (shh routine log and the history still show them)"
	}
	return summaryText(w)
}

func summaryText(w []routine.Where) string {
	if len(w) == 0 {
		return "nowhere"
	}
	var parts []string
	for _, x := range w {
		parts = append(parts, string(x))
	}
	return strings.Join(parts, ", ")
}

// rows lists what the editor shows, top to bottom.
func (e *routineEditor) rows() []editorRow {
	rows := []editorRow{{rowName, 0}, {rowDescription, 0}, {rowRepeat, 0}, {rowWaiting, 0}, {rowSummary, 0}, {rowStyle, 0}, {rowLog, 0}}
	for i := range e.draft.Steps {
		rows = append(rows, editorRow{rowStep, i})
	}
	rows = append(rows, editorRow{rowAddStep, 0})
	for i := range e.draft.Finish {
		rows = append(rows, editorRow{rowFinish, i})
	}
	rows = append(rows, editorRow{rowAddFinish, 0})
	for i := range e.draft.Triggers {
		rows = append(rows, editorRow{rowTrigger, i})
	}
	return append(rows, editorRow{rowAddTrigger, 0})
}

func (e *routineEditor) current() editorRow {
	rows := e.rows()
	e.cursor = max(0, min(e.cursor, len(rows)-1))
	return rows[e.cursor]
}

// newDraft is what a new routine starts as: one step to change, so it is a valid routine from the first moment.
func newDraft() routine.Routine {
	first, _ := routine.ParseCommand("75% 25m work")
	return routine.Routine{Repeat: 1, Waiting: routine.WaitStay, Summary: slices.Clone(summaryChoices[0]), Steps: []routine.Step{first}}
}

// startEditor opens the editor on a routine (a copy of it), or on a new one when r is nil.
func (m *Model) startEditor(r *routine.Routine) {
	if m.cfg.SaveRoutine == nil {
		m.notice = "editing routines is not available here"
		return
	}
	e := &routineEditor{}
	if r == nil {
		e.draft, e.dirty = newDraft(), true
	} else {
		e.draft = *r
		e.draft.Steps, e.draft.Finish = slices.Clone(r.Steps), slices.Clone(r.Finish)
		e.draft.Summary = slices.Clone(r.Summary)
		e.original = r.Name
	}
	m.editor = e
}

// copyRoutine opens the editor on a copy of the selected routine under a new name, to be saved as a routine of its own.
func (m *Model) copyRoutine() {
	if m.routineCursor >= len(m.saved) {
		return
	}
	r := m.saved[m.routineCursor]
	m.startEditor(&r)
	if m.editor != nil {
		m.editor.original = ""
		m.editor.draft.Name = r.Name + " copy"
		m.editor.dirty = true
	}
}

// editorKey handles every key while the editor is open.
func (m *Model) editorKey(key string) {
	e := m.editor
	rows := e.rows()
	row := e.current()
	if key != "esc" {
		e.discard = false
	}
	switch key {
	case "ctrl+c":
		m.editor = nil
	case "esc":
		switch {
		case !e.dirty || e.discard:
			m.editor = nil
		default:
			e.discard = true
			m.notice = "unsaved changes: s saves them, Esc again throws them away"
		}
	case "j", "down":
		e.cursor = min(e.cursor+1, len(rows)-1)
	case "k", "up":
		e.cursor = max(e.cursor-1, 0)
	case "g", "home":
		e.cursor = 0
	case "G", "end":
		e.cursor = len(rows) - 1
	case "enter", "e", " ":
		m.editRow(row, key == " ")
	case "a":
		m.addStepAfter(row)
	case "d", "x":
		m.deleteRow(row)
	case "J":
		m.moveStep(row, 1)
	case "K":
		m.moveStep(row, -1)
	case "s", "ctrl+s":
		m.saveEditor()
	}
}

// editRow does what Enter means on a row: type a new value, or go to the next choice.
func (m *Model) editRow(row editorRow, _ bool) {
	e := m.editor
	switch row.kind {
	case rowName:
		m.openField("name", e.draft.Name, "what it is called, and how it is started: shh routine run <name>. Letters, digits, spaces, . - _ · Enter keeps it · Esc cancels", row)
	case rowDescription:
		m.openField("description", e.draft.Description, "a line saying what it is for (may be empty) · Enter keeps it · Esc cancels", row)
	case rowRepeat:
		initial := strconv.Itoa(e.draft.Repeat)
		if e.draft.Repeat == routine.Forever {
			initial = "forever"
		}
		m.openField("repeat", initial, "how many times the steps run, 1 to 99, or forever (until stopped; no finish steps); the finish steps run once after the last · Enter keeps it · Esc cancels", row)
	case rowWaiting:
		order := []routine.Waiting{routine.WaitStay, routine.WaitNext, routine.WaitBaseline}
		i := slices.Index(order, e.draft.Waiting)
		e.draft.Waiting = order[(i+1)%len(order)]
		e.dirty = true
		m.notice = map[routine.Waiting]string{
			routine.WaitStay:     "waiting: stay: while it waits for you, the level of the step that ended stays in force",
			routine.WaitNext:     "waiting: next: while it waits, the next step's level is in force with its timer held",
			routine.WaitBaseline: "waiting: baseline: while it waits, focus falls back to the baseline",
		}[e.draft.Waiting]
	case rowSummary:
		i := slices.IndexFunc(summaryChoices, func(c []routine.Where) bool { return slices.Equal(c, e.draft.Summary) })
		e.draft.Summary = slices.Clone(summaryChoices[(i+1)%len(summaryChoices)])
		e.dirty = true
	case rowLog:
		i := slices.IndexFunc(logChoices, func(c []routine.Where) bool { return slices.Equal(c, e.draft.Log) })
		e.draft.Log = slices.Clone(logChoices[(i+1)%len(logChoices)])
		e.dirty = true
		m.notice = "log: " + logText(e.draft.Log)
	case rowStyle:
		m.openField("banner style", routine.StyleWords(e.draft.Style), "flags: --color 33 (background) --text-color 15 --button-color 238 --button-text-color 15 (0-255, shh alert colors shows the palette) --position top-left --size large --sound Hero --banner-for 10s · empty uses the config · Enter keeps it · Esc cancels", row)
	case rowStep:
		m.openField("step", e.draft.Steps[row.index].Command(), stepHint, row)
	case rowFinish:
		m.openField("finish step", e.draft.Finish[row.index].Command(), stepHint, row)
	case rowAddStep, rowAddFinish:
		m.openField("new step", "", stepHint, row)
	case rowTrigger:
		m.openTriggerForm(row.index)
	case rowAddTrigger:
		m.openTriggerForm(-1)
	}
}

const stepHint = "a shh command: 75% 25m work · off 5m break --alert banner · baseline 30% · add --ask to wait for approval after it · Enter keeps it · Esc cancels"

// openField opens the text box on a row of the editor.
func (m *Model) openField(label, initial, help string, row editorRow) {
	m.prompt = &prompt{kind: promptEditField, text: initial, label: label, help: help, row: row}
	m.cursorOn, m.restartBlink = true, true
}

// submitField applies what was typed to the row it was typed for. A text that cannot be used leaves the box open with the reason.
func (m *Model) submitField(p *prompt) {
	e := m.editor
	text := strings.TrimSpace(p.text)
	switch p.row.kind {
	case rowName:
		if err := routine.ValidName(text); err != nil {
			p.err = "a name starts with a letter or digit and uses letters, digits, spaces and . - _"
			return
		}
		e.draft.Name = text
	case rowDescription:
		e.draft.Description = text
	case rowRepeat:
		n := routine.Forever
		if !strings.EqualFold(text, "forever") {
			var err error
			if n, err = strconv.Atoi(text); err != nil || n < 1 || n > 99 {
				p.err = "a number from 1 to 99, or forever"
				return
			}
		}
		e.draft.Repeat = n
	case rowStyle:
		o, err := routine.ParseStyle(text)
		if err != nil {
			p.err = oneLine(err.Error())
			return
		}
		e.draft.Style = o
	case rowTriggerField:
		m.submitTriggerField(p)
		return // the form is the one that has changed, not the routine
	case rowStep, rowFinish, rowAddStep, rowAddFinish:
		step, err := routine.ParseCommand(text)
		if err != nil {
			p.err = shortStepError(err)
			return
		}
		switch p.row.kind {
		case rowStep:
			e.draft.Steps[p.row.index] = step
		case rowFinish:
			e.draft.Finish[p.row.index] = step
		case rowAddStep:
			e.draft.Steps = append(e.draft.Steps, step)
			e.cursor = rowIndex(e, editorRow{rowStep, len(e.draft.Steps) - 1})
		case rowAddFinish:
			e.draft.Finish = append(e.draft.Finish, step)
			e.cursor = rowIndex(e, editorRow{rowFinish, len(e.draft.Finish) - 1})
		}
	}
	e.dirty = true
	m.prompt = nil
}

func rowIndex(e *routineEditor, want editorRow) int {
	if i := slices.Index(e.rows(), want); i >= 0 {
		return i
	}
	return e.cursor
}

// shortStepError trims the parser's message to fit under the box.
func shortStepError(err error) string {
	return oneLine(err.Error())
}

// addStepAfter (a) inserts a step after the one under the cursor, as a blank to be typed in.
func (m *Model) addStepAfter(row editorRow) {
	switch row.kind {
	case rowTrigger, rowAddTrigger:
		m.openTriggerForm(-1)
		return
	case rowStep, rowAddStep:
		m.openField("new step", "", stepHint, editorRow{rowAddStep, 0})
	case rowFinish, rowAddFinish:
		m.openField("new step", "", stepHint, editorRow{rowAddFinish, 0})
	default:
		m.openField("new step", "", stepHint, editorRow{rowAddStep, 0})
	}
}

// deleteRow (d) takes a step away. A routine keeps at least one step.
func (m *Model) deleteRow(row editorRow) {
	e := m.editor
	switch row.kind {
	case rowStep:
		if len(e.draft.Steps) == 1 {
			m.notice = "a routine needs at least one step: change this one instead"
			return
		}
		e.draft.Steps = slices.Delete(e.draft.Steps, row.index, row.index+1)
	case rowFinish:
		e.draft.Finish = slices.Delete(e.draft.Finish, row.index, row.index+1)
	case rowTrigger:
		e.draft.Triggers = slices.Delete(e.draft.Triggers, row.index, row.index+1)
	default:
		m.notice = "d deletes a step or a trigger: select one first"
		return
	}
	e.dirty = true
	e.current()
}

// moveStep (J, K) moves a step down or up within its list; the cursor goes with it.
func (m *Model) moveStep(row editorRow, delta int) {
	e := m.editor
	var list *[]routine.Step
	switch row.kind {
	case rowStep:
		list = &e.draft.Steps
	case rowFinish:
		list = &e.draft.Finish
	default:
		return
	}
	to := row.index + delta
	if to < 0 || to >= len(*list) {
		return
	}
	(*list)[row.index], (*list)[to] = (*list)[to], (*list)[row.index]
	e.cursor += delta
	e.dirty = true
}

// saveEditor writes the routine to the routines file: in place if it keeps its name, as a rename (the new one saved, the old one
// removed) if not. It stays open if that fails, with the reason.
func (m *Model) saveEditor() {
	e := m.editor
	r := e.draft
	if strings.TrimSpace(r.Name) == "" {
		m.notice = "give it a name first: select Name and press Enter"
		e.cursor = 0
		return
	}
	if _, err := routine.Check(r); err != nil {
		m.notice = oneLine(err.Error())
		return
	}
	renamed := e.original != "" && e.original != r.Name
	sameName := strings.EqualFold(e.original, r.Name) // the same routine, perhaps with different capitals
	err := m.cfg.SaveRoutine(r, e.original != "" && (!renamed || sameName))
	switch {
	case errors.Is(err, routine.ErrExists):
		m.notice = fmt.Sprintf("a routine called %q already exists: choose another name", r.Name)
		return
	case err != nil:
		m.notice = "not saved: " + oneLine(err.Error())
		return
	}
	if renamed && !sameName && m.cfg.RemoveRoutine != nil {
		if err := m.cfg.RemoveRoutine(e.original); err != nil && !errors.Is(err, routine.ErrNotFound) {
			m.notice = fmt.Sprintf("saved as %s, but could not remove the old %s: %v", r.Name, e.original, err)
		}
	}
	m.editor = nil
	m.loadSaved()
	for i, s := range m.saved {
		if strings.EqualFold(s.Name, r.Name) {
			m.routineCursor = i
		}
	}
	if m.notice == "" {
		m.notice, m.noticeCmd, m.noticeHasCmd = "saved "+r.Name, "shh routine run "+r.Name, true
	}
}

// confirmDelete asks before deleting the selected routine.
func (m *Model) confirmDelete() {
	if m.cfg.RemoveRoutine == nil {
		m.notice = "editing routines is not available here"
		return
	}
	if m.routineCursor >= len(m.saved) {
		return
	}
	m.openAsk(promptConfirmDelete, "")
}

// deleteSelected removes the selected routine from the file.
func (m *Model) deleteSelected() {
	if m.routineCursor >= len(m.saved) {
		return
	}
	name := m.saved[m.routineCursor].Name
	if err := m.cfg.RemoveRoutine(name); err != nil {
		m.notice = "not deleted: " + oneLine(err.Error())
		return
	}
	m.loadSaved()
	m.notice, m.noticeCmd, m.noticeHasCmd = "deleted "+name, "shh routine remove "+name, true
}

// addCommand is the shh command that would save the routine as it is now: the same thing, typed in a terminal.
func addCommand(r routine.Routine, replace bool) string {
	parts := []string{"shh", "routine", "add", shellWord(r.Name)}
	for _, s := range r.Steps {
		parts = append(parts, shellWord(s.Command()))
	}
	if r.Description != "" {
		parts = append(parts, "-d", shellWord(r.Description))
	}
	switch {
	case r.Repeat == routine.Forever:
		parts = append(parts, "-r", "forever")
	case r.Repeat > 1:
		parts = append(parts, "-r", strconv.Itoa(r.Repeat))
	}
	for _, s := range r.Finish {
		parts = append(parts, "-f", shellWord(s.Command()))
	}
	for _, t := range r.Triggers {
		parts = append(parts, "-t", shellWord(t.Command()))
	}
	if r.Waiting != "" && r.Waiting != routine.WaitStay {
		parts = append(parts, "-w", string(r.Waiting))
	}
	if len(r.Log) > 0 {
		var w []string
		for _, x := range r.Log {
			w = append(w, string(x))
		}
		parts = append(parts, "--log", strings.Join(w, ","))
	}
	if w := routine.StyleWords(r.Style); w != "" {
		parts = append(parts, "--style", shellWord(w))
	}
	if !slices.Equal(r.Summary, summaryChoices[0]) {
		var w []string
		for _, x := range r.Summary {
			w = append(w, string(x))
		}
		parts = append(parts, "--summary", shellWord(strings.Join(w, ",")))
	}
	if replace {
		parts = append(parts, "--replace")
	}
	return strings.Join(parts, " ")
}

// shellWord quotes a word for a shell when it needs it.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$`&|;<>()*?#~!{}[]%") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// editorView draws the editor in place of the routines list.
func (m *Model) editorView() string {
	e := m.editor
	title := "New routine"
	if e.original != "" {
		title = "Editing " + e.original
	}
	lines := []string{"", "  " + bold.Render(title) + dim.Render("  ·  "+e.draft.Length(m.saved))}
	if e.dirty {
		lines[1] += dim.Render("  ·  unsaved")
	}
	lines = append(lines, "")
	rows := e.rows()
	cursor := e.cursor
	var body []string
	at := 0
	add := func(text string) { body = append(body, text) }
	for i, row := range rows {
		switch {
		case row.kind == rowStep && row.index == 0:
			add("  " + bold.Underline(true).Render("Steps") + dim.Render("  (run in order, repeated)"))
		case row.kind == rowFinish && row.index == 0, row.kind == rowAddFinish && len(e.draft.Finish) == 0:
			add("  " + bold.Underline(true).Render("Finish") + dim.Render("  (run once, after the last round)"))
		case row.kind == rowTrigger && row.index == 0, row.kind == rowAddTrigger && len(e.draft.Triggers) == 0:
			add("  " + bold.Underline(true).Render("Triggers") + dim.Render("  (ways it starts by itself)"))
		}
		if i == cursor {
			at = len(body)
		}
		add(m.editorRow(e, row, i == cursor))
	}
	room := max(1, m.bodyHeight()-len(lines))
	first := 0
	if at >= room {
		first = at - room + 1
	}
	lines = append(lines, body[first:min(len(body), first+room)]...)
	return pad(lines, m.bodyHeight(), m.width)
}

// editorRow is one row of the editor, drawn like a menu row so the selected one is plain to see.
func (m *Model) editorRow(e *routineEditor, row editorRow, selected bool) string {
	var label, value string
	switch row.kind {
	case rowName:
		label, value = "Name", e.draft.Name
		if value == "" {
			value = "(none yet)"
		}
	case rowDescription:
		label, value = "Description", e.draft.Description
		if value == "" {
			value = "(none)"
		}
	case rowRepeat:
		label, value = "Repeat", fmt.Sprintf("%d time%s", e.draft.Repeat, map[bool]string{true: "", false: "s"}[e.draft.Repeat == 1])
		if e.draft.Repeat == routine.Forever {
			value = "forever (until stopped)"
		}
	case rowWaiting:
		label, value = "Waiting", string(e.draft.Waiting)+"  (Enter changes it: stay, next, baseline)"
	case rowSummary:
		label, value = "Summary", summaryText(e.draft.Summary)+"  (Enter changes it)"
	case rowLog:
		label, value = "Log", logText(e.draft.Log)+"  (Enter changes it)"
	case rowStyle:
		label, value = "Banner style", routine.StyleWords(e.draft.Style)
		if value == "" {
			value = "(as configured)  Enter sets this routine's banner color, position, size, sound"
		}
	case rowStep:
		label, value = fmt.Sprintf("%d", row.index+1), e.draft.Steps[row.index].Command()
	case rowFinish:
		label, value = fmt.Sprintf("%d", row.index+1), e.draft.Finish[row.index].Command()
	case rowAddStep:
		label, value = "+", "add a step"
	case rowAddFinish:
		label, value = "+", "add a finish step"
	case rowTrigger:
		label, value = fmt.Sprintf("%d", row.index+1), e.draft.Triggers[row.index].Command()
	case rowAddTrigger:
		label, value = "+", "add a trigger"
	}
	cell := func(style lipgloss.Style, text string) string { return style.Reverse(selected).Render(text) }
	cursor := " "
	if selected {
		cursor = ">"
	}
	valueStyle := lipgloss.NewStyle()
	if row.kind == rowAddStep || row.kind == rowAddFinish || row.kind == rowAddTrigger {
		valueStyle = dim
	}
	indent := "  "
	if row.kind >= rowStep {
		indent = "    "
	}
	out := cell(bold, " "+cursor+indent) + cell(bold, fmt.Sprintf("%-12s", label)) + cell(valueStyle, " "+oneLine(value))
	if gap := m.width - lipgloss.Width(out); selected && gap > 0 {
		out += cell(lipgloss.NewStyle(), strings.Repeat(" ", gap))
	}
	return out
}
