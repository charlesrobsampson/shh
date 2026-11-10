package tui

import (
	"errors"
	"fmt"
	"github.com/charlesrobsampson/shh/internal/routine"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/session"
)

// RoutineControl is what the Routines tab does to the worker, as `shh routine` does. *session.Client satisfies it.
type RoutineControl interface {
	RoutineRun(name string) (session.RoutineInfo, error)
	RoutineNext() (session.RoutineInfo, error)
	RoutineBack() (session.RoutineInfo, error)
	RoutineStop() (session.RoutineInfo, error)
	RoutineUnqueue() (session.RoutineInfo, error)
	RoutineSkip(name string) (session.RoutineInfo, error)
	RoutineUnskip(name string) (session.RoutineInfo, error)
}

// listless says whether the tab shows no list of notifications, so the notification keys have nothing to act on.
func (m *Model) listless() bool { return m.tab == TabFocus || m.tab == TabRoutines }

// loadSaved re-reads the routines file, so edits show up without restarting the UI.
func (m *Model) loadSaved() {
	if m.cfg.Saved == nil {
		return
	}
	saved, err := m.cfg.Saved()
	m.savedErr = ""
	if err != nil {
		m.savedErr = err.Error()
		return // keep the last good list on screen
	}
	m.saved = saved
	m.routineCursor = max(0, min(m.routineCursor, len(saved)-1))
}

// upcomingFor is the worker's word on a routine's next scheduled start, if it has given one.
func (m *Model) upcomingFor(name string) (session.UpcomingStart, bool) {
	if !m.haveStatus {
		return session.UpcomingStart{}, false
	}
	for _, u := range m.status.Upcoming {
		if strings.EqualFold(u.Routine, name) {
			return u, true
		}
	}
	return session.UpcomingStart{}, false
}

// triggerLine is a routine's triggers and when it starts next, for the list: "⏰ weekdays at 09:00 · fri at 10:00 --ask · next Mon 09:00".
// The worker's word is used when it has one (it knows what was skipped), and the file's otherwise.
func (m *Model) triggerLine(r routine.Routine) string {
	var parts []string
	for _, t := range r.Triggers {
		parts = append(parts, t.Command())
	}
	// When it starts next comes first, so a narrow screen cuts the triggers and not that.
	now := m.cfg.Now()
	var next string
	switch u, ok := m.upcomingFor(r.Name); {
	case ok && u.Skipped:
		next = "next " + routine.StartWhen(u.Start, now) + " SKIPPED"
		if u.Then != nil {
			next += ", then " + routine.StartWhen(*u.Then, now)
		}
	case ok:
		next = "next " + routine.StartWhen(u.Start, now)
	default:
		if start, _, found := r.NextStart(now); found {
			next = "next " + routine.StartWhen(start, now)
		} else {
			next = "all switched off"
		}
	}
	return "⏰ " + next + " · " + strings.Join(parts, " · ")
}

// routineKey handles the keys that mean something only on the Routines tab, and says whether the key was one.
func (m *Model) routineKey(key string) bool {
	switch key {
	case "enter", "s":
		if m.routineCursor >= len(m.saved) {
			m.notice = "no saved routine to start: shh routine example prints a routines file to start from"
			return true
		}
		name := m.saved[m.routineCursor].Name
		m.routineDo("shh routine run "+name, func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineRun(name) },
			func(session.RoutineInfo) string { return "started " + name }, true)
	case "a":
		m.startEditor(nil)
	case "e":
		if m.routineCursor >= len(m.saved) {
			m.notice = "no saved routine to edit: a makes a new one"
			return true
		}
		r := m.saved[m.routineCursor]
		m.startEditor(&r)
	case "c":
		m.copyRoutine()
	case "d":
		m.confirmDelete()
	case "H":
		m.openHistory()
	case "R":
		if l := m.status.LastRoutine; m.haveStatus && l != nil && l.ToReview > 0 {
			m.goTo(TabTrain)
			m.notice = fmt.Sprintf("%d flagged during %s: they are at the top of the Train tab", l.ToReview, l.Name)
			return true
		}
		m.notice = "nothing flagged during the last run is waiting for review (H shows the runs)"
	case "S":
		if m.routineCursor >= len(m.saved) {
			return true
		}
		r := m.saved[m.routineCursor]
		if _, _, ok := r.NextStart(m.cfg.Now()); !ok {
			m.notice = r.Name + " has no trigger switched on, so there is no start to skip"
			return true
		}
		if u, ok := m.upcomingFor(r.Name); ok && u.Skipped {
			m.routineDo("shh routine skip --undo "+r.Name, func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineUnskip(r.Name) },
				func(session.RoutineInfo) string { return "the next start of " + r.Name + " is back on" }, true)
			return true
		}
		m.routineDo("shh routine skip "+r.Name, func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineSkip(r.Name) },
			func(session.RoutineInfo) string {
				return "skipped the next start of " + r.Name + " (S again takes it back)"
			}, true)
	case "u":
		if m.status.Routine == nil || m.status.Routine.Queued == "" {
			m.notice = "nothing is queued for when the routine ends"
			return true
		}
		m.routineDo("shh routine unqueue", func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineUnqueue() },
			func(session.RoutineInfo) string { return "dropped the queued session" }, false)
	case "n":
		m.routineDo("shh routine next", func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineNext() },
			func(session.RoutineInfo) string { return "on to the next step" }, false)
	case "b":
		m.routineDo("shh routine back", func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineBack() },
			func(session.RoutineInfo) string { return "back a step" }, false)
	case "x":
		m.routineDo("shh routine stop", func(c RoutineControl) (session.RoutineInfo, error) { return c.RoutineStop() },
			func(session.RoutineInfo) string { return "routine stopped" }, false)
	default:
		return false
	}
	return true
}

// routineDo sends one routine command to the worker, then reads the status again so the page is right at once.
func (m *Model) routineDo(cmd string, do func(RoutineControl) (session.RoutineInfo, error), done func(session.RoutineInfo) string, needsWorker bool) {
	if m.cfg.Routines == nil {
		m.notice = "routines are not available here"
		return
	}
	if needsWorker {
		if err := m.ensureWorker(); err != nil {
			m.notice = "cannot reach the worker: " + err.Error()
			return
		}
	} else if !m.haveStatus {
		m.notice = "shh is not running, so there is no routine to change"
		return
	}
	info, err := do(m.cfg.Routines)
	switch {
	case errors.Is(err, session.ErrRoutineRunning):
		m.notice = "a routine is already running: x ends it first"
		return
	case errors.Is(err, session.ErrNoRoutine):
		m.notice = "no routine is running"
		return
	case err != nil:
		m.notice = err.Error()
		return
	}
	if m.cfg.Worker != nil {
		st, err := m.cfg.Worker.Status()
		m.status, m.haveStatus = st, err == nil
	}
	m.notice, m.noticeCmd, m.noticeHasCmd = done(info), cmd, true
}

// askToEndRoutine is what a manual focus change does when a routine is in the way: ask, and carry on with it if the answer is yes. A
// new session (continuing is not nil) may also go on top of the routine, which carries on when it ends.
func (m *Model) askToEndRoutine(retry, continuing, queueing func()) {
	m.afterRoutine, m.afterContinue, m.afterQueue = retry, continuing, queueing
	m.openAsk(promptConfirmEndRoutine, "")
	m.prompt.canContinue = continuing != nil
}

// queueRoutineThen keeps what was asked for when the routine ends.
func (m *Model) queueRoutineThen() {
	do := m.afterQueue
	m.afterRoutine, m.afterContinue, m.afterQueue = nil, nil, nil
	if do != nil {
		do()
	}
}

// continueRoutineThen does what was asked on top of the routine, which waits for it.
func (m *Model) continueRoutineThen() {
	do := m.afterContinue
	m.afterRoutine, m.afterContinue, m.afterQueue = nil, nil, nil
	if do != nil {
		do()
	}
}

// endRoutineThen ends the running routine and then does what was asked, which the routine had refused.
func (m *Model) endRoutineThen() {
	retry := m.afterRoutine
	m.afterRoutine, m.afterContinue, m.afterQueue = nil, nil, nil
	if m.cfg.Routines == nil {
		m.notice = "routines are not available here"
		return
	}
	if _, err := m.cfg.Routines.RoutineStop(); err != nil && !errors.Is(err, session.ErrNoRoutine) {
		m.notice = "could not end the routine: " + err.Error()
		return
	}
	if retry != nil {
		retry()
	}
}

// routineLine is a running routine in one line, for the Focus tab and the top of the Routines tab.
func routineLine(r session.RoutineView) string {
	line := routineLineOnly(r)
	if r.Queued != "" {
		line += " · queued after it: " + r.Queued
	}
	return line
}

func routineLineOnly(r session.RoutineView) string {
	if r.Interrupted {
		return fmt.Sprintf("%s: %s (step %d of %d), held while your own session runs", r.Name, r.Step, r.Position+1, r.Total)
	}
	if r.Waiting {
		line := fmt.Sprintf("%s: waiting for you to start %s (step %d of %d)", r.Name, r.Step, r.Position+1, r.Total)
		if r.Previous != "" {
			line += ", after " + r.Previous
		}
		return line
	}
	line := fmt.Sprintf("%s: %s (step %d of %d)", r.Name, r.Step, r.Position+1, r.Total)
	if r.Next != "" {
		line += ", then " + r.Next
	}
	return line
}

// routinesView is the Routines tab: the run in progress, if any, and the saved routines to start one from.
func (m *Model) routinesView() string {
	if m.history != nil {
		return m.historyView()
	}
	if m.trigForm != nil {
		return m.triggerFormView()
	}
	if m.editor != nil {
		return m.editorView()
	}
	lines := []string{""}
	switch {
	case !m.haveStatus:
		lines = append(lines, "  "+bold.Render("shh is not running")+dim.Render(" · Enter starts it along with the routine"))
	case m.status.Routine == nil:
		lines = append(lines, "  "+bold.Render("No routine is running"))
		if l := m.status.LastRoutine; l != nil && l.Shows("ui") {
			lines = append(lines, "  "+dim.Render("last: "+oneLine(l.Line())))
		}
		if l := m.status.LastRoutine; l != nil && l.ToReview > 0 && l.ShowsLog("ui") {
			lines = append(lines, "  "+bold.Render(fmt.Sprintf("⚑ %d flagged during %s to review", l.ToReview, l.Name))+dim.Render(" · R: Train tab · H: all runs"))
		}
	default:
		r := m.status.Routine
		lines = append(lines, "  "+bold.Render("Running ")+routineLine(*r))
		if !r.Waiting && r.LengthSecs > 0 {
			lines = append(lines, "  "+dim.Render(fmt.Sprintf("this step: %s at %s · n skips on, b goes back, x ends the routine", decide.FormatRemaining(r.LengthSecs), decide.FormatLevel(r.Level))))
		} else if r.Waiting {
			lines = append(lines, "  "+dim.Render("press n to start it · focus is held meanwhile as the routine says"))
		}
	}
	lines = append(lines, "", "  "+bold.Underline(true).Render("Saved routines")+dim.Render("  (j/k to select, Enter to start)"))
	switch {
	case m.savedErr != "":
		lines = append(lines, "  "+errStyle.Render("✗ cannot read the routines file: "+oneLine(m.savedErr)))
	case len(m.saved) == 0:
		lines = append(lines, "  "+dim.Render("none yet · shh routine example prints a routines.json to start from"))
	}
	// A routine takes two lines, or three when it has triggers. Show what fits around the selected one.
	height := func(r routine.Routine) int { return 2 + min(len(r.Triggers), 1) }
	room := max(1, m.bodyHeight()-len(lines))
	first := max(0, min(m.routineCursor, len(m.saved)-1))
	if len(m.saved) > 0 {
		for used := height(m.saved[first]); first > 0 && used+height(m.saved[first-1]) <= room; first-- {
			used += height(m.saved[first-1])
		}
	}
	for i, used := first, 0; i < len(m.saved) && (used == 0 || used+height(m.saved[i]) <= room); i++ {
		used += height(m.saved[i])
		r := m.saved[i]
		selected := i == m.routineCursor
		cursor := "  "
		if selected {
			cursor = "> "
		}
		title := fmt.Sprintf("%-14s %s", r.Name, r.Description)
		total := dim.Render("  (" + r.Length(m.saved) + ")")
		rowStyle := func(s lipgloss.Style, text string) string { return s.Reverse(selected).Render(text) }
		row := rowStyle(bold, " "+cursor+title)
		if gap := m.width - lipgloss.Width(row); selected && gap > 0 {
			row += rowStyle(lipgloss.NewStyle(), strings.Repeat(" ", gap))
		}
		lines = append(lines, row, "      "+dim.Render(oneLine(r.Outline()))+total)
		if len(r.Triggers) > 0 {
			lines = append(lines, "      "+dim.Render(m.triggerLine(r)))
		}
	}
	return pad(lines, m.bodyHeight(), m.width)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
