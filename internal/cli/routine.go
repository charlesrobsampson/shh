package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
)

type routineCmd struct {
	List    routineListCmd    `cmd:"" help:"List the saved routines."`
	Example routineExampleCmd `cmd:"" help:"Print an example routines file to start from."`
	Add     routineAddCmd     `cmd:"" help:"Save a routine: its name, then its steps as shh commands."`
	Remove  routineRemoveCmd  `cmd:"" aliases:"rm" help:"Delete a saved routine."`
	Run     routineRunCmd     `cmd:"" help:"Start a saved routine."`
	Next    routineNextCmd    `cmd:"" help:"Start the step a routine is waiting on, or skip to the next step."`
	Back    routineBackCmd    `cmd:"" help:"Restart the step before the one in focus."`
	Stop    routineStopCmd    `cmd:"" help:"End the routine that is running."`
	Unqueue routineUnqueueCmd `cmd:"" help:"Drop the session that was queued to start when the routine ends."`
	Skip    routineSkipCmd    `cmd:"" help:"Leave out a routine's next scheduled start (--undo takes that back)."`
	Status  routineStatusCmd  `cmd:"" help:"Show the routine that is running, or the last one that ran."`
}

// ---- reading the file ----

type routineListCmd struct{}

func (c *routineListCmd) Run(env *Env) error {
	routines, err := routine.Load(env.RoutinesPath)
	if err != nil {
		return err
	}
	if len(routines) == 0 {
		fmt.Fprintf(env.Stdout, "No routines yet. Save some in %s; `shh routine example` prints a file to start from.\n", env.RoutinesPath)
		return nil
	}
	for i, r := range routines {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprint(env.Stdout, describeRoutine(r, routines, env.now()))
	}
	env.warnIfTriggersCannotFire(routines)
	return nil
}

// warnIfTriggersCannotFire says so when routines have triggers switched on and no worker is running to start them.
func (e *Env) warnIfTriggersCannotFire(routines []routine.Routine) {
	has := false
	for _, r := range routines {
		for _, t := range r.Triggers {
			has = has || !t.Disabled
		}
	}
	if !has {
		return
	}
	if _, err := e.client().Status(); errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(e.Stdout, "\nNote: the shh worker is not running, so triggers will not start anything yet. Start it with: shh daemon  (see: shh guide triggers)")
	}
}

// describeRoutine writes a routine for people: its name and description, then its plan on one line.
func describeRoutine(r routine.Routine, all []routine.Routine, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", r.Name)
	if r.Description != "" {
		fmt.Fprintf(&b, "  %s", r.Description)
	}
	fmt.Fprintf(&b, "\n  %s  (%s)\n", stepsLine(r), r.Length(all))
	if len(r.Triggers) > 0 {
		fmt.Fprintln(&b, "  starts by itself:")
		for _, t := range r.Triggers {
			fmt.Fprintf(&b, "    %s\n", t.Command())
		}
		if start, _, ok := r.NextStart(now); ok {
			fmt.Fprintf(&b, "  next start: %s\n", startWhen(start, now))
		} else {
			fmt.Fprintln(&b, "  next start: none (every trigger is disabled)")
		}
	}
	return b.String()
}

// startWhen says when a start is, in words that need no calendar: "today 09:00", "tomorrow 09:00", "Wed 09:00" within the week, and
// with the date after that.
func startWhen(start, now time.Time) string {
	start, now = start.In(now.Location()), now.In(now.Location())
	days := int(time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, now.Location()).Sub(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())).Hours()/24 + 0.5)
	clock := start.Format("15:04")
	switch {
	case days <= 0:
		return "today " + clock
	case days == 1:
		return "tomorrow " + clock
	case days < 7:
		return start.Format("Mon") + " " + clock
	}
	return start.Format("Mon 2 Jan") + " " + clock
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func stepsLine(r routine.Routine) string { return r.Outline() }

func levelWord(level int) string {
	if level == 0 {
		return "off"
	}
	return fmt.Sprintf("%d%%", level)
}

type routineExampleCmd struct{}

func (c *routineExampleCmd) Run(env *Env) error {
	_, err := io.WriteString(env.Stdout, routine.Example)
	return err
}

// ---- writing the file ----

type routineAddCmd struct {
	Name        string   `arg:"" help:"The routine's name (quote it if it has spaces)."`
	Steps       []string `arg:"" optional:"" help:"Each step as a shh command in quotes: \"75% 25m work --alert banner\", \"off 5m break\", \"baseline 30%\". Add --ask to a step to wait for approval before the next."`
	Description string   `short:"d" help:"A line saying what the routine is for."`
	Repeat      string   `short:"r" default:"1" help:"How many times the steps run: a number, or forever to go round until stopped."`
	Trigger     []string `short:"t" sep:"none" help:"A way to start it by itself, in words: \"weekdays except fri at 09:00 until 12:00 --reminder 5m\" (repeat the flag for more than one)."`
	Finish      []string `short:"f" sep:"none" help:"A step to run once after the last round (repeat the flag for more than one)."`
	Waiting     string   `short:"w" default:"stay" enum:"stay,next,baseline" help:"What focus does while the routine waits for you: stay (keep the level), next (the next step's level, timer held) or baseline."`
	Summary     []string `default:"banner,ui" enum:"banner,menu,ui" help:"Where to show the summary of a finished run."`
	Replace     bool     `help:"Overwrite a routine of the same name."`
}

func (c *routineAddCmd) Run(env *Env) error {
	repeat := routine.Forever
	if !strings.EqualFold(c.Repeat, "forever") {
		n, err := strconv.Atoi(c.Repeat)
		if err != nil {
			return usagef("--repeat %q is not a number or forever", c.Repeat)
		}
		repeat = n
	}
	r := routine.Routine{Name: c.Name, Description: c.Description, Repeat: repeat, Waiting: routine.Waiting(c.Waiting)}
	for _, w := range c.Summary {
		r.Summary = append(r.Summary, routine.Where(w))
	}
	for i, text := range c.Steps {
		step, err := routine.ParseCommand(text)
		if err != nil {
			return fmt.Errorf("step %d (%q): %w", i+1, text, err)
		}
		r.Steps = append(r.Steps, step)
	}
	for i, text := range c.Trigger {
		trigger, err := routine.ParseTrigger(text)
		if err != nil {
			return fmt.Errorf("--trigger %d (%q): %w", i+1, text, err)
		}
		r.Triggers = append(r.Triggers, trigger)
	}
	for i, text := range c.Finish {
		step, err := routine.ParseCommand(text)
		if err != nil {
			return fmt.Errorf("--finish step %d (%q): %w", i+1, text, err)
		}
		r.Finish = append(r.Finish, step)
	}
	if len(r.Steps) == 0 {
		return errors.New("a routine needs at least one step: give them after the name, each in quotes, such as \"75% 25m work\"")
	}
	if _, err := routine.Check(r); err != nil {
		return err
	}
	if err := routine.Save(env.RoutinesPath, r, c.Replace); err != nil {
		if errors.Is(err, routine.ErrExists) {
			return fmt.Errorf("%w; add --replace to overwrite it", err)
		}
		return err
	}
	saved, _ := routine.Check(r)
	fmt.Fprintf(env.Stdout, "Saved %s in %s\n", saved.Name, env.RoutinesPath)
	all, _ := routine.Load(env.RoutinesPath)
	fmt.Fprint(env.Stdout, describeRoutine(saved, all, env.now()))
	fmt.Fprintf(env.Stdout, "Start it with: shh routine run %s\n", saved.Name)
	return nil
}

type routineRemoveCmd struct {
	Name []string `arg:"" name:"name" help:"The routine to delete; a name with spaces needs no quotes."`
	Yes  bool     `short:"y" help:"Do not ask first."`
}

func (c *routineRemoveCmd) Run(env *Env) error {
	name := strings.Join(c.Name, " ")
	routines, err := routine.Load(env.RoutinesPath)
	if err != nil {
		return err
	}
	found, ok := routine.Find(routines, name)
	if !ok {
		return fmt.Errorf("%w: %q", routine.ErrNotFound, name)
	}
	if !c.Yes {
		if !env.IsTerminal || env.Stdin == nil {
			return fmt.Errorf("add --yes to delete %q without being asked", found.Name)
		}
		fmt.Fprintf(env.Stdout, "Delete the routine %s? [y/N] ", found.Name)
		answer, _ := bufio.NewReader(env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(env.Stdout, "Kept it.")
			return nil
		}
	}
	if err := routine.Remove(env.RoutinesPath, found.Name); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Deleted %s\n", found.Name)
	return nil
}

// ---- controlling a run ----

type routineRunCmd struct {
	// Name is several words because routine names may have spaces: shh routine run deep work.
	Name []string `arg:"" name:"name" help:"The routine to start; a name with spaces needs no quotes."`
}

func (c *routineRunCmd) Run(env *Env) error {
	name := strings.Join(c.Name, " ")
	if err := env.ensureWorker(); err != nil {
		return err
	}
	info, err := env.client().RoutineRun(name)
	if errors.Is(err, session.ErrUnknownRoutine) {
		return unknownRoutine(env, name, err)
	}
	if err != nil {
		return err
	}
	if info.Active != nil {
		fmt.Fprintf(env.Stdout, "Started %s\n", describeStep(*info.Active)) // which already begins with the routine's name
	}
	return nil
}

// unknownRoutine explains an unknown name and lists the ones there are. The worker is the one that said it does not know the name, so
// when the file here does have it, the worker is not reading the same file: most likely an older build that is still running.
func unknownRoutine(env *Env, name string, err error) error {
	routines, loadErr := routine.Load(env.RoutinesPath)
	if loadErr != nil || len(routines) == 0 {
		return fmt.Errorf("%w (there are no saved routines in %s)", err, env.RoutinesPath)
	}
	if found, ok := routine.Find(routines, name); ok {
		return fmt.Errorf("%q is in %s, but the running worker does not know it: it is probably an older build that was started before routines existed. "+
			"Turn focus off (shh stop --all), dismiss any alerts, wait a few seconds for it to exit, and run this again", found.Name, env.RoutinesPath)
	}
	var names []string
	for _, r := range routines {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	return fmt.Errorf("no saved routine is called %q; the saved ones are: %s", name, strings.Join(names, ", "))
}

type routineNextCmd struct{}

func (c *routineNextCmd) Run(env *Env) error {
	return env.routineAction(func(cl *session.Client) (session.RoutineInfo, error) { return cl.RoutineNext() }, "")
}

type routineBackCmd struct{}

func (c *routineBackCmd) Run(env *Env) error {
	return env.routineAction(func(cl *session.Client) (session.RoutineInfo, error) { return cl.RoutineBack() }, "")
}

type routineStopCmd struct{}

func (c *routineStopCmd) Run(env *Env) error {
	return env.routineAction(func(cl *session.Client) (session.RoutineInfo, error) { return cl.RoutineStop() }, "stopped")
}

type routineUnqueueCmd struct{}

func (c *routineUnqueueCmd) Run(env *Env) error {
	return env.routineAction(func(cl *session.Client) (session.RoutineInfo, error) {
		info, err := cl.RoutineUnqueue()
		if err == nil {
			fmt.Fprintln(env.Stdout, "Dropped the session that was queued for when the routine ends.")
		}
		return info, err
	}, "")
}

// routineAction runs a command on the routine that is running and says what it is doing now. A worker that is not running has no
// routine, and is not started just to say so.
func (e *Env) routineAction(do func(*session.Client) (session.RoutineInfo, error), ended string) error {
	info, err := do(e.client())
	switch {
	case errors.Is(err, session.ErrNotRunning), errors.Is(err, session.ErrNoRoutine):
		fmt.Fprintln(e.Stdout, "No routine is running.")
		return nil
	case err != nil:
		return err
	}
	if info.Active != nil {
		fmt.Fprintln(e.Stdout, describeStep(*info.Active))
		return nil
	}
	if ended != "" && info.Last != nil {
		fmt.Fprintf(e.Stdout, "Routine %s %s. %s\n", info.Last.Name, ended, summaryText(*info.Last))
		return nil
	}
	if info.Last != nil {
		fmt.Fprintf(e.Stdout, "Routine %s finished. %s\n", info.Last.Name, summaryText(*info.Last))
	}
	return nil
}

type routineStatusCmd struct{}

func (c *routineStatusCmd) Run(env *Env) error {
	info, err := env.client().RoutineStatus()
	if errors.Is(err, session.ErrNotRunning) {
		fmt.Fprintln(env.Stdout, "No routine is running (shh is not running).")
		return nil
	}
	if err != nil {
		return err
	}
	switch {
	case info.Active != nil:
		fmt.Fprintln(env.Stdout, describeStep(*info.Active))
		if info.Active.Next != "" {
			fmt.Fprintf(env.Stdout, "  next: %s\n", info.Active.Next)
		}
	default:
		fmt.Fprintln(env.Stdout, "No routine is running.")
	}
	if info.Last != nil {
		fmt.Fprintf(env.Stdout, "Last run: %s %s at %s. %s\n", info.Last.Name, session.ReasonText(info.Last.Reason), info.Last.Ended.Local().Format("Mon 15:04"), summaryText(*info.Last))
	}
	if len(info.Upcoming) > 0 {
		fmt.Fprintln(env.Stdout, "Starts by itself:")
		for _, u := range info.Upcoming {
			fmt.Fprintln(env.Stdout, "  "+describeUpcoming(u, env.now()))
		}
	}
	return nil
}

// describeUpcoming says when a routine starts next: "morning  Mon 09:00  (weekdays at 09:00)", with the skip noted.
func describeUpcoming(u session.UpcomingStart, now time.Time) string {
	line := fmt.Sprintf("%s  %s  (%s)", u.Routine, startWhen(u.Start, now), u.Trigger)
	if u.Skipped {
		line = fmt.Sprintf("%s  %s  (%s)  SKIPPED", u.Routine, startWhen(u.Start, now), u.Trigger)
		if u.Then != nil {
			line += ", then " + startWhen(*u.Then, now)
		}
	}
	return line
}

type routineSkipCmd struct {
	Name []string `arg:"" name:"name" help:"The routine; a name with spaces needs no quotes."`
	Undo bool     `help:"Take back a skip: the next start happens after all."`
}

func (c *routineSkipCmd) Run(env *Env) error {
	name := strings.Join(c.Name, " ")
	if err := env.ensureWorker(); err != nil { // the worker is what starts routines, and it keeps what was skipped
		return err
	}
	var info session.RoutineInfo
	var err error
	if c.Undo {
		info, err = env.client().RoutineUnskip(name)
	} else {
		info, err = env.client().RoutineSkip(name)
	}
	switch {
	case errors.Is(err, session.ErrUnknownRoutine):
		return unknownRoutine(env, name, err)
	case errors.Is(err, session.ErrNoTrigger):
		return fmt.Errorf("%q has no trigger that is switched on, so it does not start by itself (shh routine list shows its triggers)", name)
	case errors.Is(err, session.ErrNotSkipped):
		return fmt.Errorf("the next start of %q is not skipped", name)
	case err != nil:
		return err
	}
	for _, u := range info.Upcoming {
		if !strings.EqualFold(u.Routine, name) {
			continue
		}
		if c.Undo {
			fmt.Fprintf(env.Stdout, "The next start of %s is back on: %s\n", u.Routine, startWhen(u.Start, env.now()))
		} else {
			fmt.Fprintf(env.Stdout, "Skipped the next start of %s (%s).", u.Routine, startWhen(u.Start, env.now()))
			if u.Then != nil {
				fmt.Fprintf(env.Stdout, " The one after is %s.", startWhen(*u.Then, env.now()))
			}
			fmt.Fprintf(env.Stdout, " Take it back with: shh routine skip --undo %s\n", u.Routine)
		}
	}
	return nil
}

// describeStep says where a run is: "work (2/4), step 3 of 9: 25m at 75%", or that it is waiting for the next step to be started.
func describeStep(v session.RoutineView) string {
	text := describeStepOnly(v)
	if v.Queued != "" {
		text += "\n  Queued for when it ends: " + v.Queued + " (shh routine unqueue drops it)"
	}
	return text
}

func describeStepOnly(v session.RoutineView) string {
	if v.Interrupted {
		return fmt.Sprintf("%s: %s, step %d of %d, held while your own session runs; it carries on when that ends.", v.Name, v.Step, v.Position+1, v.Total)
	}
	if v.Waiting {
		text := fmt.Sprintf("%s: waiting to start %s (%s at %s), step %d of %d.", v.Name, v.Step, decide.FormatRemaining(v.LengthSecs), levelWord(v.Level), v.Position+1, v.Total)
		return text + " Start it with: shh routine next"
	}
	return fmt.Sprintf("%s: %s, step %d of %d: %s at %s", v.Name, v.Step, v.Position+1, v.Total, decide.FormatRemaining(v.LengthSecs), levelWord(v.Level))
}

func summaryText(s session.RoutineSummary) string {
	held := "held back 1 notification"
	if s.Held != 1 {
		held = fmt.Sprintf("held back %d notifications", s.Held)
	}
	return fmt.Sprintf("%d of %d steps in %s; %s, let through %d.", s.Steps, s.Total, decide.FormatRemaining(s.ElapsedSecs), held, s.Let)
}

// ---- manual changes during a routine ----

// routineChoice is what to do about a routine that is in the way of a session started or stopped by hand.
type routineChoice int

const (
	choiceNone     routineChoice = iota // leave everything as it was
	choiceEnd                           // end the routine, then do what was asked
	choiceContinue                      // interrupt the routine: do it now, and the routine carries on when it ends
	choiceQueue                         // keep it for when the routine ends
)

// routineInTheWay is what `shh focus` and `shh stop` do when the worker refuses because a routine is running. On a terminal they ask
// what to do (for a new session: end the routine, interrupt it, queue the session for after it, or leave it), and with --end-routine,
// --continue-routine or --after-routine they just do, so scripts can say so. Declining (or not being able to ask) leaves everything as
// it was and says how to carry on.
func (e *Env) routineInTheWay(refused error, end, interrupt, queue bool, what string, canContinue bool) (routineChoice, error) {
	if !errors.Is(refused, session.ErrRoutineRunning) {
		return choiceNone, refused
	}
	choice := choiceNone
	switch {
	case interrupt:
		choice = choiceContinue
	case queue:
		choice = choiceQueue
	case end:
		choice = choiceEnd
	default:
		if !e.IsTerminal || e.Stdin == nil {
			flags := "--end-routine to end it and " + what
			if canContinue {
				flags = "--end-routine to end it and start this session, --continue-routine to interrupt it (it carries on when this session ends) or --after-routine to start this session when it ends"
			}
			return choiceNone, fmt.Errorf("%w; add %s", refused, flags)
		}
		if canContinue {
			fmt.Fprintln(e.Stdout, "A routine is running. What should happen to it?")
			fmt.Fprintln(e.Stdout, "  [e] end it, and start this session")
			fmt.Fprintln(e.Stdout, "  [c] interrupt it: start this session now; the routine waits and carries on when this session ends")
			fmt.Fprintln(e.Stdout, "  [q] queue this session: it starts when the routine ends")
			fmt.Fprint(e.Stdout, "  [N] leave it as it is\nChoose [e/c/q/N]: ")
		} else {
			fmt.Fprintf(e.Stdout, "A routine is running. End it and %s? [y/N] ", what)
		}
		answer, _ := bufio.NewReader(e.Stdin).ReadString('\n')
		switch a := strings.ToLower(strings.TrimSpace(answer)); {
		case a == "y" || a == "yes" || a == "e" || a == "end":
			choice = choiceEnd
		case canContinue && (a == "c" || a == "continue" || a == "interrupt"):
			choice = choiceContinue
		case canContinue && (a == "q" || a == "queue"):
			choice = choiceQueue
		default:
			fmt.Fprintln(e.Stdout, "Left the routine running.")
			return choiceNone, nil
		}
	}
	if choice == choiceEnd {
		if _, err := e.client().RoutineStop(); err != nil && !errors.Is(err, session.ErrNoRoutine) {
			return choiceNone, err
		}
	}
	return choice, nil
}
