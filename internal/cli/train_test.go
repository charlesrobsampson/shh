package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func trainRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, true)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	r.env.Now = func() time.Time { return t0.Add(10 * time.Hour) }
	threshold := 65
	for i, title := range []string{"prod is down", "Lunch menu", "weekly newsletter"} {
		_, _, err := r.store.InsertNotification(store.Notification{Source: "Teams", Sender: "Ana", Title: title, Body: "body " + title,
			Timestamp: t0.Add(time.Duration(i) * time.Minute), Status: store.StatusBlocked, Score: 0.4, SourceRecID: int64(i + 1), ThresholdAtDecision: &threshold})
		if err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestTrainLabelsSeveralNotificationsAndReportsEachOne(t *testing.T) {
	r := trainRig(t)
	out := r.mustRun("train", "1", "--yes")
	if !strings.Contains(out, "#1: score") {
		t.Errorf("out = %q", out)
	}
	if code := r.run("train", "2", "3", "--label", "5"); code != 0 {
		t.Fatalf("code %d: %s", code, r.err.String())
	}
	for _, id := range []int64{1, 2, 3} {
		if n, _ := r.store.GetNotification(id); !n.IsTrained {
			t.Errorf("#%d should be trained", id)
		}
	}
	if code := r.run("train", "1", "--no"); code != 1 || !strings.Contains(r.err.String(), "already trained") {
		t.Errorf("code %d: %s", code, r.err.String())
	}
	if code := r.run("train", "99", "--yes"); code != 1 {
		t.Errorf("a missing id exited %d", code)
	}
}

func TestTrainNeedsExactlyOneThingToDo(t *testing.T) {
	r := trainRig(t)
	for _, args := range [][]string{{"train"}, {"train", "1"}, {"train", "--yes"}, {"train", "1", "--yes", "--no"}, {"train", "--reset", "--rollback"}, {"train", "1", "--label", "120", "--no"}} {
		if code := r.run(args...); code != 2 {
			t.Errorf("%v exited %d, want a usage error", args, code)
		}
	}
}

func TestTrainUndoResetAndRollback(t *testing.T) {
	r := trainRig(t)
	r.mustRun("train", "1", "--label", "95")
	r.mustRun("train", "--undo")
	if n, _ := r.store.GetNotification(1); n.IsTrained {
		t.Error("--undo should untrain it")
	}
	if code := r.run("train", "--undo"); code != 1 {
		t.Errorf("a second undo exited %d", code)
	}
	r.mustRun("train", "1", "--label", "95")
	r.mustRun("train", "--reset")
	r.mustRun("train", "2", "--label", "5")
	out := r.mustRun("train", "--rollback")
	if !strings.Contains(out, "untrained again") {
		t.Errorf("out = %q", out)
	}
	if n, _ := r.store.GetNotification(2); n.IsTrained {
		t.Error("rolling back should untrain what was labelled after the reset")
	}
}

func TestTrainExportThenImportRoundTrips(t *testing.T) {
	r := trainRig(t)
	r.mustRun("train", "1", "--yes")
	r.mustRun("train", "2", "--label", "10")
	dir := t.TempDir()
	file := filepath.Join(dir, "private", "out.jsonl")
	out := r.mustRun("train", "--export", "--file", file)
	if !strings.Contains(out, "Wrote 2 labelled") || strings.Contains(r.err.String(), "warning") {
		t.Errorf("out %q err %q", out, r.err.String())
	}
	raw, _ := os.ReadFile(file)
	if lines := strings.Split(strings.TrimSpace(string(raw)), "\n"); len(lines) != 2 || !strings.Contains(lines[0], `"feedback":"y"`) || !strings.Contains(lines[1], `"label":10`) {
		t.Errorf("file:\n%s", raw)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}

	// Importing on a rig that has none of them stores them as history and trains on them.
	other := trainRig2(t)
	other.mustRun("train", "--import", "--file", file)
	trained, _ := other.store.ListNotifications(store.Filter{})
	count := 0
	for _, n := range trained {
		if n.IsTrained {
			count++
		}
	}
	if count != 2 {
		t.Errorf("%d trained after the import, want 2", count)
	}
	// and importing again changes nothing
	if out := other.mustRun("train", "--import", "--file", file); !strings.Contains(out, "2 were already trained") {
		t.Errorf("second import: %q", out)
	}
	if all, _ := other.store.ListNotifications(store.Filter{}); len(all) != 2 {
		t.Errorf("%d rows after importing twice", len(all))
	}
}

func trainRig2(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, true)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	return r
}

func TestTrainExportWarnsOutsideAPrivateFolderAndImportRefusesBadRows(t *testing.T) {
	r := trainRig(t)
	r.mustRun("train", "1", "--yes")
	file := filepath.Join(t.TempDir(), "out.jsonl")
	r.mustRun("train", "--export", "--file", file)
	if !strings.Contains(r.err.String(), "not under a private/ folder") {
		t.Errorf("stderr = %q", r.err.String())
	}
	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	os.WriteFile(bad, []byte(`{"source":"Teams","body":"x","at":"2026-01-01T00:00:00Z","level":65}`+"\n"), 0o600)
	if code := r.run("train", "--import", "--file", bad); code != 1 || !strings.Contains(r.err.String(), "line 1") {
		t.Errorf("code %d: %s", code, r.err.String())
	}
}
