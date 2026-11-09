package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/routine"
	"github.com/charlesrobsampson/shh/internal/session"
)

type routineCmd struct {
	List    routineListCmd    `cmd:"" help:"List the saved routines."`
	Example routineExampleCmd `cmd:"" help:"Print an example routines file to start from."`
	Run     routineRunCmd     `cmd:"" help:"Start a saved routine."`
	Next    routineNextCmd    `cmd:"" help:"Start the step a routine is waiting on, or skip to the next step."`
	Back    routineBackCmd    `cmd:"" help:"Restart the step before the one in focus."`
	Stop    routineStopCmd    `cmd:"" help:"End the routine that is running."`
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
		fmt.Fprint(env.Stdout, describeRoutine(r))
	}
	return nil
}

// describeRoutine writes a routine for people: its name and description, then its plan on one line.
func describeRoutine(r routine.Routine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", r.Name)
	if r.Description != "" {
		fmt.Fprintf(&b, "  %s", r.Description)
	}
	fmt.Fprintf(&b, "\n  %s  (%s in all)\n", stepsLine(r), decide.FormatRemaining(int(r.Total()/time.Second)))
	return b.String()
}

// stepsLine is the steps as the shh commands that do them: "75% 25m work -> off 5m break, 4 times -> off 15m long break".
func stepsLine(r routine.Routine) string {
	part := func(steps []routine.Step) string {
		var out []string
		for _, s := range steps {
			out = append(out, s.Command())
		}
		return strings.Join(out, " -> ")
	}
	line := part(r.Steps)
	if r.Repeat > 1 {
		line += fmt.Sprintf(", %d times", r.Repeat)
	}
	if len(r.Finish) > 0 {
		line += " -> " + part(r.Finish)
	}
	return line
}

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
		fmt.Fprintf(env.Stdout, "Last run: %s %s at %s. %s\n", info.Last.Name, info.Last.Reason, info.Last.Ended.Local().Format("Mon 15:04"), summaryText(*info.Last))
	}
	return nil
}

// describeStep says where a run is: "work (2/4), step 3 of 9: 25m at 75%", or that it is waiting for the next step to be started.
func describeStep(v session.RoutineView) string {
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

// endRoutineFirst is what `shh focus` and `shh stop` do when the worker refuses because a routine is running: on a terminal they ask
// whether to end the routine, and with --end-routine they just do, so scripts can say so. Declining (or not being able to ask) leaves
// everything as it was and says how to carry on.
func (e *Env) endRoutineFirst(refused error, endRoutine bool, what string) (bool, error) {
	if !errors.Is(refused, session.ErrRoutineRunning) {
		return false, refused
	}
	if !endRoutine {
		if !e.IsTerminal || e.Stdin == nil {
			return false, fmt.Errorf("%w; add --end-routine to end it and %s", refused, what)
		}
		fmt.Fprintf(e.Stdout, "A routine is running. End it and %s? [y/N] ", what)
		answer, _ := bufio.NewReader(e.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(e.Stdout, "Left the routine running.")
			return false, nil
		}
	}
	if _, err := e.client().RoutineStop(); err != nil && !errors.Is(err, session.ErrNoRoutine) {
		return false, err
	}
	return true, nil
}
