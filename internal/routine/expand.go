package routine

import (
	"fmt"
	"slices"
	"strings"
)

// maxDepth is how many routines can be inside one another: a routine that calls one that calls one, and so on. A run never gets
// anywhere near it unless something is wrong, which is what the limit is for.
const maxDepth = 5

// Expand returns the routine with each call step ("run pomodoro") replaced by the steps it stands for: the called routine's steps
// repeated as often as it says, then its finish steps, all that --times times. The steps are named after where they come from
// ("pomodoro › work (2/4)"), and a --ask or --alert on the call goes on the last session of what replaced it, so it applies when the
// called routine ends. The called routine's own waiting and summary settings do not apply: the routine that was started decides those.
//
// A call that cannot work is an error that says which step it is: a name no routine has, a routine that calls itself (with the path:
// A → B → A, which is also how a loop is stopped from running away), nesting deeper than maxDepth, or a call to a routine that repeats
// forever, after which nothing could run.
func Expand(all []Routine, r Routine) (Routine, error) {
	out := r
	var err error
	stack := []string{r.Name}
	if out.Steps, err = expandSteps(all, r.Steps, routineKey(r.Name)+".steps", stack); err != nil {
		return Routine{}, err
	}
	if out.Finish, err = expandSteps(all, r.Finish, routineKey(r.Name)+".finish", stack); err != nil {
		return Routine{}, err
	}
	return out, nil
}

// Length is how long the routine takes, worked out with the routines it calls, and how to say so: "1h15m in all", "at least 1h15m
// (some steps end at a time of day)" or "until stopped" for one that goes round forever. all is every routine, so calls can be found.
func (r Routine) Length(all []Routine) string {
	x, err := Expand(all, r)
	if err != nil {
		x = r
	}
	switch {
	case x.Repeat == Forever:
		return "until stopped"
	case x.Total() == 0 && !x.Exact():
		return "ends at set times"
	case !x.Exact() || err != nil:
		return "at least " + DurationWord(x.Total()) + " in all"
	}
	return DurationWord(x.Total()) + " in all"
}

func expandSteps(all []Routine, steps []Step, key string, stack []string) ([]Step, error) {
	var out []Step
	for i, s := range steps {
		if s.Kind != StepCall {
			out = append(out, s)
			continue
		}
		k := fmt.Sprintf("%s[%d]", key, i)
		callee, ok := Find(all, s.Call)
		if !ok {
			return nil, fmt.Errorf("%q: run %q: no routine is called that", k, s.Call)
		}
		if slices.ContainsFunc(stack, func(n string) bool { return strings.EqualFold(n, callee.Name) }) {
			return nil, fmt.Errorf("%q: run %q: routines would call each other forever: %s", k, s.Call, strings.Join(append(slices.Clone(stack), callee.Name), " → "))
		}
		if len(stack) >= maxDepth {
			return nil, fmt.Errorf("%q: run %q: routines are inside one another more than %d deep (%s)", k, s.Call, maxDepth, strings.Join(append(slices.Clone(stack), callee.Name), " → "))
		}
		if callee.Repeat == Forever {
			return nil, fmt.Errorf("%q: run %q: that routine repeats forever, so nothing after it would ever run", k, s.Call)
		}
		inner := append(slices.Clone(stack), callee.Name)
		calleeSteps, err := expandSteps(all, callee.Steps, routineKey(callee.Name)+".steps", inner)
		if err != nil {
			return nil, err
		}
		calleeFinish, err := expandSteps(all, callee.Finish, routineKey(callee.Name)+".finish", inner)
		if err != nil {
			return nil, err
		}
		var body []Step
		for round := 1; round <= callee.Repeat; round++ {
			for _, st := range calleeSteps {
				st.Style = callee.Style.Merge(st.Style) // the called routine's own look, under the step's
				st.Name = callee.Name + " › " + st.Name
				if callee.Repeat > 1 {
					st.Name += fmt.Sprintf(" (%d/%d)", round, callee.Repeat)
				}
				body = append(body, st)
			}
		}
		for _, st := range calleeFinish {
			st.Style = callee.Style.Merge(st.Style)
			st.Name = callee.Name + " › " + st.Name
			body = append(body, st)
		}
		var whole []Step
		for t := 0; t < max(s.Times, 1); t++ {
			whole = append(whole, body...)
		}
		for j := len(whole) - 1; j >= 0; j-- {
			if whole[j].Kind != StepSession {
				continue
			}
			if s.Advance == AdvanceAsk {
				whole[j].Advance = AdvanceAsk
			}
			if s.Alert != AlertNone && s.Alert != "" {
				whole[j].Alert = s.Alert
			}
			break
		}
		out = append(out, whole...)
	}
	return out, nil
}
