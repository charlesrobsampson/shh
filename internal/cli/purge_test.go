package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func purgeRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, false)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	now := t0.Add(100 * 24 * time.Hour)
	r.env.Now = func() time.Time { return now }
	r.env.Retention = func() store.Retention {
		return store.Retention{TTL: 30 * 24 * time.Hour, KeepUntrained: 90 * 24 * time.Hour}
	}
	threshold := 65
	for i, age := range []time.Duration{2 * 24 * time.Hour, 40 * 24 * time.Hour, 50 * 24 * time.Hour} {
		status := store.StatusAlerted // alerted rows go at the time to live
		if i == 0 {
			status = store.StatusBlocked
		}
		if _, _, err := r.store.InsertNotification(store.Notification{Source: "Teams", Sender: "Ana", Title: "t", Body: "b", Timestamp: now.Add(-age),
			Status: status, SourceRecID: int64(i + 1), ThresholdAtDecision: &threshold}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestPurgeAsksBeforeDeletingAndOnlyTakesWhatIsPastItsTime(t *testing.T) {
	r := purgeRig(t)
	if code := r.run("purge"); code != 1 || !strings.Contains(r.err.String(), "2 notifications past their time to live would be deleted: add --yes") {
		t.Errorf("code %d: %s", code, r.err.String())
	}
	if left, _ := r.store.CountAll(); left != 3 {
		t.Fatalf("a refused purge deleted things: %d left", left)
	}
	r.env.IsTerminal, r.env.Stdin = true, strings.NewReader("n\n")
	if out := r.mustRun("purge"); !strings.Contains(out, "[y/N]") || !strings.Contains(out, "Kept them.") {
		t.Errorf("out = %q", out)
	}
	if left, _ := r.store.CountAll(); left != 3 {
		t.Errorf("answering no deleted things: %d left", left)
	}
	r.env.Stdin = strings.NewReader("y\n")
	if out := r.mustRun("purge"); !strings.Contains(out, "Deleted 2 notifications.") {
		t.Errorf("out = %q", out)
	}
	if left, _ := r.store.CountAll(); left != 1 {
		t.Errorf("%d left, want the recent one", left)
	}
	if out := r.mustRun("purge", "--yes"); !strings.Contains(out, "nothing to delete") {
		t.Errorf("out = %q", out)
	}
}

func TestPurgeAllWipesEverythingWithYes(t *testing.T) {
	r := purgeRig(t)
	if out := r.mustRun("purge", "--all", "--yes"); !strings.Contains(out, "Deleted 3 notifications.") {
		t.Errorf("out = %q", out)
	}
	if left, _ := r.store.CountAll(); left != 0 {
		t.Errorf("%d left", left)
	}
}
