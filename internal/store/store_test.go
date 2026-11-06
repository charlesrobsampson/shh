package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func exec(t *testing.T, db *sql.DB, query string, args ...any) error {
	t.Helper()
	_, err := db.Exec(query, args...)
	return err
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if err := exec(t, db, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestOpenInMemoryIsAtLatestVersion(t *testing.T) {
	s := mustOpen(t, ":memory:")
	got, err := s.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != len(migrations) {
		t.Fatalf("SchemaVersion() = %d, want %d", got, len(migrations))
	}
}

func TestSchemaHasAllTables(t *testing.T) {
	s := mustOpen(t, ":memory:")
	for _, table := range []string{
		"schema_version", "notifications", "focus_sessions", "model_weights",
		"calendar_events", "unparsed_calendar", "training_log",
	} {
		var name string
		err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestReopeningAFileIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first := mustOpen(t, path)
	mustExec(t, first.db, `INSERT INTO notifications (source, sender, body, status, predicted_score) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.2)`)
	first.Close()

	second := mustOpen(t, path)
	var rows, versions int
	second.db.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&rows)
	second.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&versions)
	if rows != 1 {
		t.Errorf("rows after reopen = %d, want 1", rows)
	}
	if versions != len(migrations) {
		t.Errorf("schema_version rows = %d, want %d (migrations must not be applied twice)", versions, len(migrations))
	}
}

func TestOlderDatabaseUpgrades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	old, err := openWith(path, migrations)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, old.db, `INSERT INTO notifications (source, sender, body, status, predicted_score) VALUES ('Teams', 'Ana', 'keep me', 'blocked', 0.2)`)
	old.Close()

	next := append(append([]string{}, migrations...), `ALTER TABLE notifications ADD COLUMN upgraded TEXT DEFAULT 'yes'`)
	upgraded, err := openWith(path, next)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	if v, _ := upgraded.SchemaVersion(); v != len(next) {
		t.Errorf("SchemaVersion() = %d, want %d", v, len(next))
	}
	var body, flag string
	if err := upgraded.db.QueryRow(`SELECT body, upgraded FROM notifications`).Scan(&body, &flag); err != nil {
		t.Fatal(err)
	}
	if body != "keep me" || flag != "yes" {
		t.Errorf("row after upgrade = (%q, %q), want existing data and the new column", body, flag)
	}
}

func TestFailedMigrationLeavesNoTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	broken := []string{`CREATE TABLE half_done (id INTEGER); THIS IS NOT SQL;`}
	if _, err := openWith(path, broken); err == nil {
		t.Fatal("openWith with a broken migration succeeded")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var leftover int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'half_done'`).Scan(&leftover)
	if leftover != 0 {
		t.Error("a failed migration left its partial changes behind")
	}
	var applied int
	db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&applied)
	if applied != 0 {
		t.Errorf("schema_version rows = %d, want 0", applied)
	}
}

func TestDatabaseFromANewerBuildIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	newer := append(append([]string{}, migrations...), `CREATE TABLE future (id INTEGER)`)
	db, err := openWith(path, newer)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open of a newer database: err = %v, want a 'newer' error", err)
	}
}

func TestFilePermissions(t *testing.T) {
	t.Run("new file and its directory are private", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "config", "shh")
		path := filepath.Join(dir, "shh.db")
		s := mustOpen(t, path)
		mustExec(t, s.db, `INSERT INTO model_weights (feature_version, weights_blob) VALUES ('v', x'00')`)

		matches, _ := filepath.Glob(path + "*")
		if len(matches) == 0 {
			t.Fatal("no database files found")
		}
		for _, file := range matches {
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			if perm := info.Mode().Perm(); perm != 0o600 {
				t.Errorf("%s mode = %o, want 600", filepath.Base(file), perm)
			}
		}
		info, _ := os.Stat(dir)
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("directory mode = %o, want 700", perm)
		}
	})
	t.Run("an existing world-readable file is tightened", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "shh.db")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		mustOpen(t, path)
		info, _ := os.Stat(path)
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("mode = %o, want 600", perm)
		}
	})
}

func TestFileDatabaseUsesWAL(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "shh.db"))
	var mode string
	s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestSchemaAcceptsTheDocumentedColumns(t *testing.T) {
	s := mustOpen(t, ":memory:")
	db := s.db
	mustExec(t, db, `INSERT INTO focus_sessions
		(id, kind, parent_id, start_time, end_time, target_duration_minutes, focus_percentage, then_level, paused_at, remaining_secs, note, total_blocked, total_allowed)
		VALUES (1, 'baseline', NULL, '2026-11-05 09:00:00', NULL, 0, 30, NULL, NULL, NULL, 'deep work', 4, 1),
		       (2, 'escalation', 1, '2026-11-05 09:10:00', NULL, 50, 90, 0, '2026-11-05 09:20:00', 1800, NULL, 0, 0)`)
	mustExec(t, db, `INSERT INTO notifications
		(id, source, sender, title, body, timestamp, status, predicted_score, time_sensitivity_boost, is_dismissed_inbox, is_trained, user_feedback,
		 bundle_id, source_rec_id, threshold_at_decision, user_label, reason, is_read, session_id, summary)
		VALUES (1, 'Teams', 'Ana', 'Ping', 'server is down', '2026-11-05 09:30:00', 'alerted', 0.82, 0.3, 1, 1, 'y',
		        'com.microsoft.teams2', 42, 65, 80, 'vip sender', 1, 2, 'outage')`)
	mustExec(t, db, `INSERT INTO calendar_events (id, title, start_time, alert_at, fired, source_notification_id)
		VALUES (1, 'Sync', '2026-11-05 13:30:00', '2026-11-05 13:29:00', 0, 1)`)
	mustExec(t, db, `INSERT INTO unparsed_calendar (id, notification_id, category, title, subtitle, raw_text)
		VALUES (1, 1, 'OLAlertsNotificationReminders', 'Sync', 'Teams Meeting', 'sometime tomorrow-ish')`)
	mustExec(t, db, `INSERT INTO training_log (id, notification_id, label, predicted, delta_blob) VALUES (1, 1, '80', 0.4, x'0102')`)
	mustExec(t, db, `INSERT INTO model_weights (id, feature_version, weights_blob) VALUES (1, 'l0l1-v1', x'0a0b')`)

	var title, status, reason string
	var score float64
	var threshold, label, sessionID int
	err := db.QueryRow(`SELECT title, status, predicted_score, threshold_at_decision, user_label, reason, session_id FROM notifications WHERE id = 1`).
		Scan(&title, &status, &score, &threshold, &label, &reason, &sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Ping" || status != "alerted" || score != 0.82 || threshold != 65 || label != 80 || reason != "vip sender" || sessionID != 2 {
		t.Errorf("notification round trip = %q %q %v %d %d %q %d", title, status, score, threshold, label, reason, sessionID)
	}

	var kind string
	var parent, thenLevel, remaining int
	err = db.QueryRow(`SELECT kind, parent_id, then_level, remaining_secs FROM focus_sessions WHERE id = 2`).Scan(&kind, &parent, &thenLevel, &remaining)
	if err != nil || kind != "escalation" || parent != 1 || thenLevel != 0 || remaining != 1800 {
		t.Errorf("focus_sessions round trip = %q %d %d %d (%v)", kind, parent, thenLevel, remaining, err)
	}

	var fired bool
	if err := db.QueryRow(`SELECT fired FROM calendar_events WHERE id = 1`).Scan(&fired); err != nil || fired {
		t.Errorf("calendar_events fired = %v (%v), want false", fired, err)
	}
	var blob []byte
	if err := db.QueryRow(`SELECT weights_blob FROM model_weights WHERE feature_version = 'l0l1-v1'`).Scan(&blob); err != nil || len(blob) != 2 {
		t.Errorf("model_weights blob = %v (%v)", blob, err)
	}
}

func TestSchemaRejectsInvalidData(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		query string
	}{
		{"unknown status", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score) VALUES ('Teams', 'Ana', 'hi', 'maybe', 0.1)`},
		{"score above 1", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score) VALUES ('Teams', 'Ana', 'hi', 'blocked', 1.5)`},
		{"boost above the 0.4 cap", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score, time_sensitivity_boost) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 0.5)`},
		{"threshold 100 (levels stop at 99)", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score, threshold_at_decision) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 100)`},
		{"label above 100", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score, user_label) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 101)`},
		{"feedback other than y or n", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score, user_feedback) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 'x')`},
		{"session that does not exist", nil, `INSERT INTO notifications (source, sender, body, status, predicted_score, session_id) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 99)`},
		{"duplicate macOS record id", []string{`INSERT INTO notifications (source, sender, body, status, predicted_score, source_rec_id) VALUES ('Teams', 'Ana', 'hi', 'blocked', 0.1, 7)`},
			`INSERT INTO notifications (source, sender, body, status, predicted_score, source_rec_id) VALUES ('Teams', 'Bo', 'again', 'blocked', 0.1, 7)`},
		{"focus level 100", nil, `INSERT INTO focus_sessions (kind, start_time, focus_percentage) VALUES ('baseline', '2026-11-05 09:00:00', 100)`},
		{"unknown focus kind", nil, `INSERT INTO focus_sessions (kind, start_time, focus_percentage) VALUES ('sideways', '2026-11-05 09:00:00', 50)`},
		{"same meeting twice", []string{`INSERT INTO calendar_events (title, start_time, alert_at) VALUES ('Sync', '2026-11-05 13:30:00', '2026-11-05 13:29:00')`},
			`INSERT INTO calendar_events (title, start_time, alert_at) VALUES ('Sync', '2026-11-05 13:30:00', '2026-11-05 13:29:00')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := mustOpen(t, ":memory:")
			for _, q := range tt.setup {
				mustExec(t, s.db, q)
			}
			if err := exec(t, s.db, tt.query); err == nil {
				t.Fatal("insert succeeded, want a constraint error")
			}
		})
	}
}

func TestSeveralWeightVersionsCanCoexist(t *testing.T) {
	s := mustOpen(t, ":memory:")
	for _, version := range []string{"v1", "v1", "v2"} {
		mustExec(t, s.db, `INSERT INTO model_weights (feature_version, weights_blob) VALUES (?, x'00')`, version)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM model_weights`).Scan(&n)
	if n != 3 {
		t.Fatalf("model_weights rows = %d, want 3 (history is kept)", n)
	}
}

func TestPurgingANotificationKeepsItsDependents(t *testing.T) {
	s := mustOpen(t, ":memory:")
	mustExec(t, s.db, `INSERT INTO notifications (id, source, sender, body, status, predicted_score) VALUES (1, 'Teams', 'Ana', 'hi', 'blocked', 0.1)`)
	mustExec(t, s.db, `INSERT INTO calendar_events (title, start_time, alert_at, source_notification_id) VALUES ('Sync', '2026-11-05 13:30:00', '2026-11-05 13:29:00', 1)`)
	mustExec(t, s.db, `INSERT INTO unparsed_calendar (notification_id, raw_text) VALUES (1, 'huh')`)
	mustExec(t, s.db, `INSERT INTO training_log (notification_id, label) VALUES (1, 'y')`)

	mustExec(t, s.db, `DELETE FROM notifications WHERE id = 1`)

	for _, table := range []string{"calendar_events", "unparsed_calendar", "training_log"} {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 1 {
			t.Errorf("%s rows after the TTL purge = %d, want 1 (history survives)", table, n)
		}
	}
}
