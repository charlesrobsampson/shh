package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

func TestCaptureWritesRawNotificationsAndFiltersByAppAndAge(t *testing.T) {
	r := newRig(t, false)
	now := t0.Add(10 * time.Hour)
	r.env.Now = func() time.Time { return now }
	src := testkit.NewFakeSource()
	src.Add(ingest.Notification{BundleID: "com.microsoft.teams2", Title: "Ana", Body: "old one", Delivered: now.Add(-3 * time.Hour)})
	src.Add(ingest.Notification{BundleID: "com.microsoft.teams2", Title: "Bo", Body: "recent <one>", Category: "chat", Delivered: now.Add(-20 * time.Minute)})
	src.Add(ingest.Notification{BundleID: "com.apple.mail", Title: "Cy", Body: "mail", Delivered: now.Add(-10 * time.Minute)})
	r.env.Notifications = src
	file := filepath.Join(t.TempDir(), "private", "raw.jsonl")

	out := r.mustRun("capture", "--once", "--file", file)
	if !strings.Contains(out, "Captured 0 notifications") {
		t.Errorf("only what comes next is captured by default: %q", out)
	}
	out = r.mustRun("capture", "--once", "--file", file, "--backfill", "1h", "--app", "TEAMS")
	raw, _ := os.ReadFile(file)
	if !strings.Contains(out, "Captured 1 notification ") || strings.Count(string(raw), "\n") != 1 || !strings.Contains(string(raw), `"body":"recent <one>"`) || strings.Contains(string(raw), "old one") || strings.Contains(string(raw), "Cy") {
		t.Errorf("out %q file %q", out, raw)
	}
	if info, _ := os.Stat(file); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if strings.Contains(r.err.String(), "warning") {
		t.Errorf("under private/ there is nothing to warn about: %s", r.err.String())
	}
	r.mustRun("capture", "--once", "--file", file, "--backfill", "1h", "--app", "mail")
	if raw, _ := os.ReadFile(file); strings.Count(string(raw), "\n") != 2 {
		t.Errorf("a second capture appends:\n%s", raw)
	}
	if code := r.run("capture", "--once", "--file", filepath.Join(t.TempDir(), "x.jsonl"), "--backfill", "soon"); code != 2 {
		t.Errorf("a bad --backfill exited %d", code)
	}
}
