package session_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
)

// soundRig is a worker rig that counts the times the alert sound is played.
type soundRig struct {
	*workerRig
	plays int
}

func newSoundRig(t *testing.T) *soundRig {
	t.Helper()
	sr := &soundRig{}
	sr.workerRig = newWorkerRig(t, func(c *session.Config) {
		c.PlaySound = func() error { sr.plays++; return nil }
	})
	return sr
}

// restart builds a second worker over the same store and clock, as a worker restarting would.
func (r *soundRig) restart() {
	r.worker = r.build(func(c *session.Config) { c.PlaySound = func() error { r.plays++; return nil } })
}

func (r *workerRig) escalate(e session.Escalation) {
	r.t.Helper()
	if _, err := r.worker.Escalate(e); err != nil {
		r.t.Fatalf("Escalate(%+v): %v", e, err)
	}
}

func TestASessionThatAsksForABannerAnnouncesItsEndOnce(t *testing.T) {
	r := newSoundRig(t)
	r.escalate(session.Escalation{Level: 75, Duration: 25 * time.Minute, Note: "writing", Alert: "banner"})
	r.clock.Advance(24 * time.Minute)
	r.tick()
	if got := r.alerter.Shown(); len(got) != 0 {
		t.Fatalf("announced before the end: %v", got)
	}
	r.clock.Advance(time.Minute)
	r.tick()
	shown := r.alerter.Shown()
	if len(shown) != 1 || shown[0].Title != "Focus session over" || shown[0].Body != "25m at 75% · writing" || shown[0].Source != "Focus" {
		t.Fatalf("alerts = %+v, want one banner saying how long it was, at what level, and what it was for", shown)
	}
	r.clock.Advance(time.Minute)
	r.tick()
	r.tick()
	if len(r.alerter.Shown()) != 1 || r.plays != 0 {
		t.Errorf("a second alert was given: %d banners, %d sounds", len(r.alerter.Shown()), r.plays)
	}
}

func TestASoundAlertPlaysTheSoundAndShowsNoBanner(t *testing.T) {
	r := newSoundRig(t)
	r.escalate(session.Escalation{Level: 50, Duration: 10 * time.Minute, Alert: "sound"})
	r.clock.Advance(10 * time.Minute)
	r.tick()
	if r.plays != 1 || len(r.alerter.Shown()) != 0 {
		t.Errorf("sounds %d banners %d, want one sound and no banner", r.plays, len(r.alerter.Shown()))
	}
	r.tick()
	if r.plays != 1 {
		t.Error("the sound played again")
	}
}

func TestNoAlertIsTheDefaultAndNoneMeansNone(t *testing.T) {
	for _, alert := range []string{"", "none"} {
		r := newSoundRig(t)
		r.escalate(session.Escalation{Level: 50, Duration: 5 * time.Minute, Alert: alert})
		r.clock.Advance(5 * time.Minute)
		r.tick()
		if r.plays != 0 || len(r.alerter.Shown()) != 0 {
			t.Errorf("alert %q announced something: %d sounds, %v", alert, r.plays, r.alerter.Shown())
		}
	}
}

func TestASessionStoppedByHandIsNotAnnounced(t *testing.T) {
	r := newSoundRig(t)
	r.escalate(session.Escalation{Level: 75, Duration: 25 * time.Minute, Alert: "banner"})
	r.clock.Advance(5 * time.Minute)
	if err := r.worker.Stop(); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Hour)
	r.tick()
	if len(r.alerter.Shown()) != 0 {
		t.Errorf("a session you stopped yourself was announced: %v", r.alerter.Shown())
	}
}

func TestASessionThatEndedWhileTheWorkerWasDownIsAnnouncedOnlyIfItWasRecent(t *testing.T) {
	recent := newSoundRig(t)
	recent.escalate(session.Escalation{Level: 75, Duration: 25 * time.Minute, Alert: "banner"})
	recent.clock.Advance(40 * time.Minute) // ended 15 minutes ago, while no tick ran
	recent.restart()
	recent.tick()
	if len(recent.alerter.Shown()) != 1 {
		t.Errorf("a session that ended a quarter of an hour ago should still be announced: %v", recent.alerter.Shown())
	}

	old := newSoundRig(t)
	old.escalate(session.Escalation{Level: 75, Duration: 25 * time.Minute, Alert: "banner"})
	old.clock.Advance(5 * time.Hour)
	old.restart()
	old.tick()
	if len(old.alerter.Shown()) != 0 {
		t.Errorf("an alert for something that ended hours ago should not pop up now: %v", old.alerter.Shown())
	}
	old.clock.Advance(time.Minute)
	old.tick()
	if len(old.alerter.Shown()) != 0 {
		t.Error("and it should not come back later either")
	}
}

func TestARestartDoesNotAnnounceTheSameEndTwice(t *testing.T) {
	r := newSoundRig(t)
	r.escalate(session.Escalation{Level: 75, Duration: 5 * time.Minute, Alert: "banner"})
	r.clock.Advance(5 * time.Minute)
	r.tick()
	r.restart()
	r.tick()
	if len(r.alerter.Shown()) != 1 {
		t.Errorf("banners = %d after a restart, want the one already given", len(r.alerter.Shown()))
	}
}

func TestAnAlertThatFailsIsReportedOnceAndNotRetried(t *testing.T) {
	r := newSoundRig(t)
	r.alerter.FailWith(errors.New("overlay broke"))
	r.escalate(session.Escalation{Level: 75, Duration: 5 * time.Minute, Alert: "banner"})
	r.clock.Advance(5 * time.Minute)
	r.tick()
	r.tick()
	r.tick()
	if n := len(r.errs); n != 1 || !strings.Contains(r.errs[0].Error(), "overlay broke") {
		t.Errorf("errors = %v, want the failure reported once", r.errs)
	}
}

func TestBadAlertsAreRefused(t *testing.T) {
	r := newSoundRig(t)
	if _, err := r.worker.Escalate(session.Escalation{Level: 75, Duration: time.Minute, Alert: "loud"}); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("an unknown alert: err = %v", err)
	}
	if _, err := r.worker.Escalate(session.Escalation{Level: 75, Alert: "banner"}); !errors.Is(err, session.ErrInvalid) || !strings.Contains(err.Error(), "never ends") {
		t.Errorf("an alert with no length: err = %v, want it to say a session with no timer never ends", err)
	}
	if _, err := r.worker.Escalate(session.Escalation{Level: 75, Alert: "none"}); err != nil {
		t.Errorf("none with no length is fine: %v", err)
	}
	if st, err := r.worker.Status(); err != nil || len(st.Layers) != 1 {
		t.Errorf("only the valid session should be in force: %+v, %v", st.Layers, err)
	}
}

func TestRoutineStepsAreNotAnnouncedTwice(t *testing.T) {
	r := newRoutineRig(t, pomodoroLite)
	r.start("pomo")
	r.pass(25 * time.Minute)
	// the routine's own announcement, and no "Focus session over" from the session mechanism
	for _, a := range r.alerter.Shown() {
		if a.Title == "Focus session over" {
			t.Errorf("a routine step was announced as a plain session as well: %+v", a)
		}
	}
	if len(r.alerter.Shown()) != 1 {
		t.Errorf("alerts = %v", r.shown())
	}
}

func TestTheStatusShowsWhichSessionsAlert(t *testing.T) {
	r := newIPCRig(t)
	if _, err := r.client.FocusAlert(75, 25*time.Minute, nil, "", "banner"); err != nil {
		t.Fatal(err)
	}
	st, err := r.client.Status()
	if err != nil || len(st.Layers) != 1 || st.Layers[0].Alert != "banner" {
		t.Fatalf("status = %+v, %v", st.Layers, err)
	}
	if _, err := r.client.FocusAlert(75, 0, nil, "", "banner"); !errors.Is(err, session.ErrInvalid) {
		t.Errorf("an alert with no length across the socket: %v", err)
	}
}
