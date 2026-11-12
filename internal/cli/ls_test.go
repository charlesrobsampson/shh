package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

// lsRig has a few notifications in its history, at known times and scores.
func lsRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, false)
	r.env.WithStore = func(do func(*store.Store) error) error { return do(r.store) }
	r.env.Now = func() time.Time { return t0.Add(10 * time.Hour) }
	threshold := 65
	add := func(source, sender, title string, status store.Status, score float64, ago time.Duration, mut ...func(*store.Notification)) {
		n := store.Notification{Source: source, Sender: sender, Title: title, Body: "body", Timestamp: t0.Add(10*time.Hour - ago), Status: status, Score: score,
			SourceRecID: int64(len(title)*100) + int64(ago/time.Minute), ThresholdAtDecision: &threshold}
		for _, m := range mut {
			m(&n)
		}
		id, _, err := r.store.InsertNotification(n)
		if err != nil {
			t.Fatal(err)
		}
		if n.IsFlagged {
			err = r.store.SetFlagged(id, true)
		}
		if n.IsTrained {
			err = r.store.Train(id, store.Training{Feedback: "y"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	add("Teams", "Ana", "Standup notes", store.StatusBlocked, 0.30, 5*time.Minute)
	add("Teams", "Bo", "prod is down", store.StatusAlerted, 0.90, 30*time.Minute)
	add("Outlook", "Cy", "Lunch menu", store.StatusBlocked, 0.10, 3*time.Hour, func(n *store.Notification) { n.IsFlagged = true })
	add("Slack", "Di", "deploy done", store.StatusBlocked, 0.55, 30*time.Hour, func(n *store.Notification) { n.IsTrained = true })
	return r
}

func TestLsListsNotificationsNewestFirstWithWhatShhDid(t *testing.T) {
	r := lsRig(t)
	out := r.mustRun("ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "STATUS") || !strings.Contains(lines[0], "PCT") || !strings.Contains(lines[0], "APP") {
		t.Errorf("the first line should name the columns: %s", lines[0])
	}
	lines = lines[1:]
	if len(lines) != 4 {
		t.Fatalf("lines = %d:\n%s", len(lines), out)
	}
	for i, want := range []string{"Ana · Standup notes", "Bo · prod is down", "Cy · Lunch menu", "Di · deploy done"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d should be %q: %s", i, want, lines[i])
		}
	}
	if !strings.Contains(lines[0], "blocked") || !strings.Contains(lines[0], " 30 ") || !strings.Contains(lines[0], "Teams") {
		t.Errorf("a line says the status, the score and the app: %s", lines[0])
	}
	if !strings.Contains(lines[2], "[flagged]") || !strings.Contains(lines[3], "[trained]") || strings.Contains(lines[0], "[") {
		t.Errorf("marks: %v", lines)
	}
}

func TestLsFiltersBySourceSenderStatusScoreAndTime(t *testing.T) {
	r := lsRig(t)
	for args, want := range map[string]string{
		"--source Teams":        "Ana,Bo",
		"--sender Cy":           "Cy",
		"--status alerted":      "Bo",
		"--status blocked":      "Ana,Cy,Di",
		"--min-score 50":        "Bo,Di",
		"--since 1h":            "Ana,Bo",
		"--since 1d":            "Ana,Bo,Cy",
		"--since 1d12h":         "Ana,Bo,Cy,Di",
		"--flagged":             "Cy",
		"--untrained":           "Ana,Bo,Cy",
		"-n 2":                  "Ana,Bo",
		"--status blocked -n 2": "Ana,Cy",
	} {
		out := r.mustRun(append([]string{"ls"}, strings.Fields(args)...)...)
		var got []string
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			for _, who := range []string{"Ana", "Bo", "Cy", "Di"} {
				if strings.Contains(line, who+" ·") {
					got = append(got, who)
				}
			}
		}
		if strings.Join(got, ",") != want {
			t.Errorf("ls %s showed %v, want %s", args, got, want)
		}
	}
}

func TestLsSaysWhenNothingMatchesAndRefusesBadFilters(t *testing.T) {
	r := lsRig(t)
	if out := r.mustRun("ls", "--source", "Nothing"); !strings.Contains(out, "No notifications match.") {
		t.Errorf("out = %q", out)
	}
	for _, args := range [][]string{{"ls", "--min-score", "150"}, {"ls", "--since", "soon"}, {"ls", "--status", "maybe"}} {
		if code := r.run(args...); code != 2 {
			t.Errorf("%v exited %d, want a usage error (2): %s", args, code, r.err.String())
		}
	}
	n := newRig(t, false)
	if code := n.run("ls"); code == 0 || !strings.Contains(n.err.String(), "cannot be read from here") {
		t.Errorf("code %d stderr %q", code, n.err.String())
	}
}

func TestLsJSONGivesOneObjectPerLineAndLsChangesNothing(t *testing.T) {
	r := lsRig(t)
	before, _ := r.store.ListNotifications(store.Filter{})
	out := r.mustRun("ls", "--json", "-n", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d", len(lines))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["source"] != "Teams" || rec["status"] != "blocked" || rec["score"] != float64(30) || rec["level"] != float64(65) || rec["flagged"] != false {
		t.Errorf("record = %v", rec)
	}
	after, _ := r.store.ListNotifications(store.Filter{})
	if len(after) != len(before) {
		t.Error("ls must not change anything")
	}
	for i := range before {
		if before[i].IsFlagged != after[i].IsFlagged || before[i].IsRead != after[i].IsRead || before[i].IsTrained != after[i].IsTrained {
			t.Errorf("notification %d changed", before[i].ID)
		}
	}
}
