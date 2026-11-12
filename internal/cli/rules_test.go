package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestRulesReviewListsWhatCouldNotBeReadAndSaysHowToFixIt(t *testing.T) {
	r := newRig(t, false)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	r.env.Now = func() time.Time { return t0 }
	if out := r.mustRun("rules", "review"); !strings.Contains(out, "Nothing to add") {
		t.Errorf("out = %q", out)
	}
	for _, text := range []string{"Standup begins whenever", "Retro soonish"} {
		if err := r.store.AddUnparsedCalendar(store.UnparsedCalendar{Category: "OLAlertsNotificationReminders", Title: "Team " + text, RawText: text}); err != nil {
			t.Fatal(err)
		}
	}
	out := r.mustRun("rules", "review")
	for _, want := range []string{"2 calendar reminders", "Retro soonish", "Standup begins whenever", "OLAlertsNotificationReminders", "rules.json", "(?P<hour>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "Retro") > strings.Index(out, "Standup") {
		t.Error("newest first")
	}
	if one := r.mustRun("rules", "review", "-n", "1"); strings.Contains(one, "Standup") {
		t.Errorf("-n 1 showed both:\n%s", one)
	}
}
