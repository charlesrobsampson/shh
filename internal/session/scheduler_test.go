package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
)

// The rig's clock starts at 09:00 UTC on Thursday 2026-11-05.

func scheduled(triggers ...string) string {
	q := make([]string, len(triggers))
	for i, t := range triggers {
		q[i] = `"` + t + `"`
	}
	return `{"routines": {"morning": {"steps": ["75% 25m work", "off 5m break"], "triggers": [` + strings.Join(q, ",") + `]}}}`
}

func (r *routineRig) running() string {
	r.t.Helper()
	if v := r.info().Active; v != nil {
		return v.Name
	}
	return ""
}

func TestATriggerStartsTheRoutineWhenItsTimeComes(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	r.pass(29 * time.Minute)
	if r.running() != "" {
		t.Fatal("it is not time yet")
	}
	r.pass(time.Minute)
	if r.running() != "morning" {
		t.Fatalf("the routine should have started at 09:30: %+v", r.info())
	}
	if top := r.top(); top.Percent != 75 || top.Remaining != 25*time.Minute {
		t.Errorf("top = %+v", top)
	}
}

func TestATriggerStartsItOnlyOncePerStartEvenAfterTheRoutineIsStoppedOrTheWorkerRestarts(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	r.pass(30 * time.Minute)
	if err := r.worker.RoutineStop(); err != nil {
		t.Fatal(err)
	}
	r.pass(time.Minute)
	r.pass(time.Minute)
	if r.running() != "" {
		t.Error("a stopped run must not be started again by the same trigger")
	}
	r.restart()
	r.pass(time.Minute)
	if r.running() != "" {
		t.Error("a restarted worker must not start it again either")
	}
	r.pass(24*time.Hour - 3*time.Minute) // tomorrow, 09:30
	if r.running() != "morning" {
		t.Errorf("tomorrow's start should happen: %+v", r.info())
	}
}

func TestATriggerOnlyFiresOnItsDays(t *testing.T) {
	r := newRoutineRig(t, scheduled("fri at 09:30"))
	r.pass(time.Hour) // thursday 10:00
	if r.running() != "" {
		t.Fatal("thursday is not a friday")
	}
	r.pass(23*time.Hour + 30*time.Minute) // friday 09:30
	if r.running() != "morning" {
		t.Errorf("friday's start should happen: %+v", r.info())
	}
}

func TestAStartMissedByMoreThanGraceIsSkippedNotCaughtUp(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	r.clock.Advance(50 * time.Minute) // the worker was down until 09:50
	r.tick()
	if r.running() != "" {
		t.Error("twenty minutes late is more than the 15 minute grace")
	}
	r.pass(time.Minute)
	if r.running() != "" {
		t.Error("and it is not tried again a minute later")
	}
}

func TestAStartWithinGraceIsMadeLateAndGraceIsConfigurable(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	r.clock.Advance(40 * time.Minute) // 09:40, ten minutes late
	r.tick()
	if r.running() != "morning" {
		t.Error("ten minutes late is inside the grace")
	}
	r2 := newRoutineRig(t, scheduled("daily at 09:30 --grace 30m"))
	r2.clock.Advance(55 * time.Minute) // 09:55
	r2.tick()
	if r2.running() != "morning" {
		t.Error("25 minutes late is inside a 30 minute grace")
	}
	r3 := newRoutineRig(t, scheduled("daily at 09:30 --grace 0"))
	r3.clock.Advance(31 * time.Minute)
	r3.tick()
	if r3.running() != "" {
		t.Error("with no grace a start that was not made on time is missed")
	}
}

func TestDisabledTriggersNeverFireAndDoNotKeepTheWorkerUp(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --disabled"))
	if r.worker.HasTriggers() {
		t.Error("only disabled triggers: nothing to wait for")
	}
	r.pass(30 * time.Minute)
	if r.running() != "" {
		t.Error("a disabled trigger fired")
	}
	on := newRoutineRig(t, scheduled("daily at 09:30"))
	if !on.worker.HasTriggers() {
		t.Error("an enabled trigger keeps the worker up")
	}
	none := newRoutineRig(t, pomodoroLite)
	if none.worker.HasTriggers() {
		t.Error("no triggers at all")
	}
}

func TestTheUntilTimeCutsTheRunOffWhateverIsGoingOn(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work", "off 5m break"], "triggers": ["daily at 09:30 until 10:00"]}}}`)
	r.pass(30 * time.Minute)
	if r.running() != "morning" {
		t.Fatal("not started")
	}
	r.pass(29 * time.Minute) // 09:59
	if r.running() != "morning" {
		t.Fatal("cut off early")
	}
	r.pass(time.Minute) // 10:00
	if r.running() != "" || len(r.status().Layers) != 0 {
		t.Errorf("the run should be cut off at 10:00: %+v layers %d", r.info(), len(r.status().Layers))
	}
	last := r.worker.RoutineLast()
	if last == nil || last.Reason != "until" || !strings.Contains(last.Line(), "was cut off at its end time") {
		t.Errorf("last = %+v", last)
	}
	for _, s := range r.shown() {
		if strings.Contains(s, "stopped at") {
			t.Errorf("no until alert was asked for, but got %q", s)
		}
	}
}

func TestTheUntilAlertIsABannerOrASoundWhenAskedFor(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work"], "triggers": ["daily at 09:30 until 10:00 --until-alert banner"]}}}`)
	r.pass(30 * time.Minute)
	r.pass(30 * time.Minute)
	found := false
	for _, s := range r.shown() {
		found = found || strings.HasPrefix(s, "morning stopped at 10:00 |")
	}
	if !found {
		t.Errorf("shown = %v", r.shown())
	}
	s := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work"], "triggers": ["daily at 09:30 until 10:00 --until-alert sound"]}}}`)
	s.pass(30 * time.Minute)
	before := s.plays
	s.pass(30 * time.Minute)
	if s.plays != before+1 {
		t.Errorf("sounds %d then %d", before, s.plays)
	}
}

func TestALateStartIsStillCutOffAtTheUntilTime(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work"], "triggers": ["daily at 09:30 until 10:00"]}}}`)
	r.clock.Advance(40 * time.Minute) // 09:40
	r.tick()
	if r.running() != "morning" {
		t.Fatal("a ten minute late start is made")
	}
	r.pass(20 * time.Minute)
	if r.running() != "" {
		t.Error("it still ends at 10:00")
	}
}

func TestAStartAfterTheEndTimeIsNeverMadeEvenInsideGrace(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"steps": ["75% 5m work"], "triggers": ["daily at 09:30 until 09:35 --grace 1h"]}}}`)
	r.clock.Advance(36 * time.Minute)
	r.tick()
	if r.running() != "" {
		t.Error("starting after the stop time makes no sense")
	}
}

func TestTheCutOffStartsAQueuedSessionLikeAnyEnd(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work"], "triggers": ["daily at 09:30 until 10:00"]}}}`)
	r.pass(30 * time.Minute)
	r.queue(90, 30*time.Minute, "after")
	r.pass(30 * time.Minute)
	if top := r.top(); top.Percent != 90 || top.Note != "after" {
		t.Errorf("top = %+v", top)
	}
}

func TestTheReminderBannerComesOnceBeforeTheStartAndCanSkipIt(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --reminder 5m"))
	r.pass(24 * time.Minute)
	if len(r.alerter.Shown()) != 0 {
		t.Fatal("too early for the reminder")
	}
	r.pass(time.Minute) // 09:25
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Title != "morning starts in 5m" || len(shown[0].Actions) != 1 || shown[0].Actions[0].ID != "skip" {
		t.Fatalf("shown = %+v", shown)
	}
	r.pass(time.Minute)
	r.pass(time.Minute)
	if len(r.alerter.Shown()) != 1 {
		t.Error("the reminder is given once")
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "skip"); err != nil {
		t.Fatal(err)
	}
	r.pass(3 * time.Minute) // 09:30
	if r.running() != "" {
		t.Error("the reminder's Skip button should skip that start")
	}
}

func TestASkippedStartGivesNoReminder(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --reminder 5m"))
	if err := r.worker.RoutineSkip("morning"); err != nil {
		t.Fatal(err)
	}
	r.pass(26 * time.Minute)
	if len(r.alerter.Shown()) != 0 {
		t.Errorf("shown = %v", r.shown())
	}
}

func TestSomethingRunningMakesAStartAskByDefault(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	if _, err := r.worker.Escalate(session.Escalation{Level: 90, Duration: 2 * time.Hour, Note: "my own work"}); err != nil {
		t.Fatal(err)
	}
	r.pass(30 * time.Minute)
	if r.running() != "" {
		t.Fatal("it should have asked, not started")
	}
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Title != "Start morning?" || len(shown[0].Actions) != 2 || shown[0].Actions[0].ID != "start" || shown[0].Actions[1].ID != "skip" ||
		!strings.Contains(shown[0].Body, "a focus session is running") {
		t.Fatalf("shown = %+v", shown)
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "start"); err != nil {
		t.Fatal(err)
	}
	if r.running() != "morning" {
		t.Error("Start now should start it")
	}
	if len(r.status().Layers) != 2 {
		t.Errorf("the user's own session stays underneath: %d layers", len(r.status().Layers))
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "start"); err != nil {
		t.Fatal(err)
	}
	if v := r.info().Active; v == nil || v.Position != 0 {
		t.Error("a second press must not restart it")
	}
}

func TestSkipOnTheBannerLeavesTheStartOut(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --ask"))
	r.pass(30 * time.Minute)
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Title != "Start morning?" {
		t.Fatalf("shown = %+v", shown)
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "skip"); err != nil {
		t.Fatal(err)
	}
	if err := r.worker.RoutineAction(shown[0].ActionToken, "start"); err != nil {
		t.Fatal(err)
	}
	if r.running() != "" {
		t.Error("a skipped start cannot be started afterwards by its banner")
	}
}

func TestAskTriggerAsksEvenWhenNothingIsRunningAndStartsOnTheButton(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --ask"))
	r.pass(30 * time.Minute)
	if r.running() != "" {
		t.Fatal("it should wait to be asked")
	}
	shown := r.alerter.Shown()
	if strings.Contains(shown[0].Body, "is running") {
		t.Errorf("nothing is running, so the banner should not say so: %q", shown[0].Body)
	}
	r.pass(5 * time.Minute)
	if err := r.worker.RoutineAction(shown[0].ActionToken, "start"); err != nil {
		t.Fatal(err)
	}
	if r.running() != "morning" {
		t.Error("Start now should start it, even a few minutes late")
	}
}

func TestWhenBusySkipLeavesWhatIsRunningAloneAndWhenBusyStartEndsARoutine(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"other": {"repeat": "forever", "steps": ["50% 25m x"]},
	  "morning": {"steps": ["75% 25m work"], "triggers": ["daily at 09:30 --when-busy skip", "daily at 10:30 --when-busy start"]}}}`)
	r.start("other")
	r.pass(30 * time.Minute)
	if r.running() != "other" || len(r.alerter.Shown()) != 0 {
		t.Errorf("skip: running %q shown %v", r.running(), r.shown())
	}
	r.pass(time.Hour) // 10:30
	if r.running() != "morning" {
		t.Errorf("start should end the running routine and start this one: %q", r.running())
	}
}

func TestABaselineAloneIsNotBusy(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --when-busy skip"))
	if err := r.worker.SetBaseline(30); err != nil {
		t.Fatal(err)
	}
	r.pass(30 * time.Minute)
	if r.running() != "morning" {
		t.Error("a standing baseline is not something being done, so the start goes ahead")
	}
}

func TestSkipLeavesOutTheNextStartAndUnskipTakesItBack(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	if err := r.worker.RoutineSkip("MORNING"); err != nil { // names are found ignoring case
		t.Fatal(err)
	}
	up := r.info().Upcoming
	if len(up) != 1 || !up[0].Skipped || up[0].Routine != "morning" || up[0].Then == nil || up[0].Then.Sub(up[0].Start) != 24*time.Hour {
		t.Fatalf("upcoming = %+v", up)
	}
	r.pass(30 * time.Minute)
	if r.running() != "" {
		t.Error("the skipped start should not happen")
	}
	r.pass(24 * time.Hour)
	if r.running() != "morning" {
		t.Error("but the next one should")
	}
}

func TestUnskipRestoresTheStart(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30"))
	if err := r.worker.RoutineUnskip("morning"); !errors.Is(err, session.ErrNotSkipped) {
		t.Errorf("nothing to take back: %v", err)
	}
	if err := r.worker.RoutineSkip("morning"); err != nil {
		t.Fatal(err)
	}
	if err := r.worker.RoutineUnskip("morning"); err != nil {
		t.Fatal(err)
	}
	if up := r.info().Upcoming; len(up) != 1 || up[0].Skipped {
		t.Errorf("upcoming = %+v", up)
	}
	r.pass(30 * time.Minute)
	if r.running() != "morning" {
		t.Error("the start should happen after an unskip")
	}
}

func TestSkipNeedsARoutineWithAnEnabledTrigger(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"plain": {"steps": ["75% 5m x"]}, "off": {"steps": ["75% 5m x"], "triggers": ["daily at 09:30 --disabled"]}}}`)
	for _, name := range []string{"plain", "off"} {
		if err := r.worker.RoutineSkip(name); !errors.Is(err, session.ErrNoTrigger) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := r.worker.RoutineSkip("nothing"); !errors.Is(err, session.ErrUnknownRoutine) {
		t.Errorf("err = %v", err)
	}
}

func TestUpcomingListsEachRoutinesNextStartSoonestFirst(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {
	  "late": {"steps": ["75% 5m x"], "triggers": ["daily at 17:00"]},
	  "early": {"steps": ["75% 5m x"], "triggers": ["fri at 09:30", "daily at 09:15 until 10:00"]},
	  "none": {"steps": ["75% 5m x"]}}}`)
	up := r.info().Upcoming
	if len(up) != 2 || up[0].Routine != "early" || up[1].Routine != "late" {
		t.Fatalf("upcoming = %+v", up)
	}
	if up[0].Trigger != "daily at 09:15 until 10:00" || up[0].Start.Hour() != 9 || up[0].Start.Minute() != 15 {
		t.Errorf("the earliest of a routine's triggers: %+v", up[0])
	}
}

func TestAButtonFromAnOldOrChangedBannerDoesNothing(t *testing.T) {
	r := newRoutineRig(t, scheduled("daily at 09:30 --ask"))
	r.pass(30 * time.Minute)
	tok := r.alerter.Shown()[0].ActionToken
	*r.text = `{"routines": {"morning": {"steps": ["75% 25m work"]}}}` // the triggers are gone
	r.pass(time.Minute)
	r.pass(time.Minute * 10)
	for _, bad := range []string{tok, "sched:", "sched:x:y:z", "sched:1:0:nothing", "sched:99999999999:5:morning"} {
		if err := r.worker.RoutineAction(bad, "start"); err != nil {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if r.running() != "" {
		t.Error("nothing should have started")
	}
}

func TestAScheduledRunCarriesItsEndTimeAcrossARestart(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"morning": {"repeat": "forever", "steps": ["75% 25m work"], "triggers": ["daily at 09:30 until 10:00"]}}}`)
	r.pass(30 * time.Minute)
	r.restart()
	r.pass(30 * time.Minute)
	if r.running() != "" {
		t.Error("the restarted worker should still cut the run off at 10:00")
	}
}

func TestAStartInTheMiddleOfAnUntilStepRoutineJoinsInProgress(t *testing.T) {
	r := newRoutineRig(t, `{"routines": {"day": {"steps": ["90% until 09:45 deep", "off until 10:00 break", "50% until 12:00 email"],
	  "triggers": ["daily at 09:30 --grace 1h"]}}}`)
	r.clock.Advance(50 * time.Minute) // 09:50, late but inside the grace
	r.tick()
	if v := r.info().Active; v == nil || v.Step != "break" || r.top().Remaining != 10*time.Minute {
		t.Errorf("view %+v top %+v: it should join in the break", v, r.top())
	}
}
