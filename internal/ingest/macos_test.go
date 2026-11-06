package ingest_test

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"howett.net/plist"

	"github.com/charlesrobsampson/shh/internal/ingest"

	_ "modernc.org/sqlite"
)

// The record and app tables are the real usernoted schema.
const realSchema = `
CREATE TABLE record (rec_id INTEGER PRIMARY KEY, app_id INTEGER, uuid BLOB, data BLOB, request_date REAL, request_last_date REAL, delivered_date REAL, presented Bool, style INTEGER, snooze_fire_date REAL);
CREATE TABLE app (app_id INTEGER PRIMARY KEY, identifier VARCHAR, badge INTEGER NULL);
`

const cocoaEpoch = 978307200 // 2001-01-01 in Unix seconds

type row struct {
	recID     int64
	bundle    string
	delivered float64 // seconds since 2001-01-01
	data      []byte
}

func request(fields map[string]any) []byte {
	blob, err := plist.Marshal(map[string]any{"app": "x", "req": fields, "date": 1.0}, plist.BinaryFormat)
	if err != nil {
		panic(err)
	}
	return blob
}

func delivered(t time.Time) float64 {
	return float64(t.Unix()-cocoaEpoch) + float64(t.Nanosecond())/1e9
}

type db struct {
	t    *testing.T
	path string
	conn *sql.DB
}

func newDB(t *testing.T) *db {
	t.Helper()
	return newDBAt(t, filepath.Join(t.TempDir(), "db"))
}

func newDBAt(t *testing.T, path string) *db {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(realSchema); err != nil {
		t.Fatal(err)
	}
	return &db{t: t, path: path, conn: conn}
}

func (d *db) add(r row) {
	d.t.Helper()
	var appID int64
	err := d.conn.QueryRow(`SELECT app_id FROM app WHERE identifier = ?`, r.bundle).Scan(&appID)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := d.conn.Exec(`INSERT INTO app (identifier) VALUES (?)`, r.bundle)
		if err != nil {
			d.t.Fatal(err)
		}
		appID, _ = res.LastInsertId()
	} else if err != nil {
		d.t.Fatal(err)
	}
	if _, err := d.conn.Exec(`INSERT INTO record (rec_id, app_id, data, delivered_date) VALUES (?, ?, ?, ?)`, r.recID, appID, r.data, r.delivered); err != nil {
		d.t.Fatal(err)
	}
}

func (d *db) remove(recID int64) {
	d.t.Helper()
	if _, err := d.conn.Exec(`DELETE FROM record WHERE rec_id = ?`, recID); err != nil {
		d.t.Fatal(err)
	}
}

func recIDs(batch ingest.Batch) []int64 {
	out := make([]int64, len(batch.Notifications))
	for i, n := range batch.Notifications {
		out[i] = n.RecID
	}
	return out
}

func same(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

var when = time.Date(2026, 11, 5, 13, 30, 0, 0, time.UTC)

func TestSinceReturnsRowsAfterTheGivenID(t *testing.T) {
	d := newDB(t)
	for id := int64(1); id <= 3; id++ {
		d.add(row{recID: id, bundle: "com.microsoft.teams2", delivered: delivered(when), data: request(map[string]any{"titl": "t", "body": "b"})})
	}
	source := ingest.NewMacOS(d.path)

	tests := []struct {
		since int64
		want  []int64
	}{{0, []int64{1, 2, 3}}, {1, []int64{2, 3}}, {2, []int64{3}}, {3, nil}, {99, nil}}
	for _, tt := range tests {
		batch, err := source.Since(tt.since)
		if err != nil || !same(recIDs(batch), tt.want...) {
			t.Errorf("Since(%d) = %v, %v; want %v", tt.since, recIDs(batch), err, tt.want)
		}
	}
}

func TestSinceDecodesTheFields(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 7, bundle: "com.microsoft.outlook", delivered: delivered(when), data: request(map[string]any{
		"titl": "Weekly Sync", "subt": "Microsoft Teams Meeting", "body": "November 5, 2026 at 13:30 🚀",
		"cate": "OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier", "iden": "abc-123",
	})})
	batch, err := ingest.NewMacOS(d.path).Since(0)
	if err != nil || len(batch.Notifications) != 1 {
		t.Fatalf("Since = %+v, %v", batch, err)
	}
	got := batch.Notifications[0]
	want := ingest.Notification{
		RecID: 7, BundleID: "com.microsoft.outlook", Title: "Weekly Sync", Subtitle: "Microsoft Teams Meeting",
		Body: "November 5, 2026 at 13:30 🚀", Category: "OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier",
		Identifier: "abc-123", Delivered: when,
	}
	if got != want {
		t.Errorf("notification = %+v\nwant %+v", got, want)
	}
}

func TestMissingFieldsAreEmptyNotErrors(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 1, bundle: "app.one", delivered: delivered(when), data: request(map[string]any{"titl": "only a title"})})
	d.add(row{recID: 2, bundle: "app.two", delivered: delivered(when), data: request(map[string]any{"body": "no title, no subtitle"})})
	d.add(row{recID: 3, bundle: "app.three", delivered: delivered(when), data: request(map[string]any{})})

	batch, err := ingest.NewMacOS(d.path).Since(0)
	if err != nil || len(batch.Notifications) != 3 || len(batch.Skipped) != 0 {
		t.Fatalf("Since = %d notifications, %d skipped, %v; want 3, 0, nil", len(batch.Notifications), len(batch.Skipped), err)
	}
	if n := batch.Notifications[0]; n.Title != "only a title" || n.Body != "" {
		t.Errorf("row without a body = %+v", n)
	}
	if n := batch.Notifications[1]; n.Title != "" || n.Body != "no title, no subtitle" {
		t.Errorf("row without a title = %+v", n)
	}
}

func TestDeliveredDateUsesTheCocoaEpoch(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 1, bundle: "app", delivered: 0, data: request(map[string]any{"body": "x"})})
	d.add(row{recID: 2, bundle: "app", delivered: 86400.5, data: request(map[string]any{"body": "x"})})
	d.add(row{recID: 3, bundle: "app", delivered: delivered(when.Add(250 * time.Millisecond)), data: request(map[string]any{"body": "x"})})

	batch, _ := ingest.NewMacOS(d.path).Since(0)
	wants := []time.Time{
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2001, 1, 2, 0, 0, 0, 500_000_000, time.UTC),
		when.Add(250 * time.Millisecond),
	}
	for i, want := range wants {
		if got := batch.Notifications[i].Delivered; got.Sub(want).Abs() > time.Millisecond || got.Location() != time.UTC {
			t.Errorf("row %d delivered = %v (%v), want %v in UTC", i+1, got, got.Location(), want)
		}
	}
}

func TestRowsRemovedAfterReadingAreSimplyGone(t *testing.T) {
	d := newDB(t)
	for id := int64(1); id <= 3; id++ {
		d.add(row{recID: id, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "x"})})
	}
	source := ingest.NewMacOS(d.path)
	first, _ := source.Since(0)
	if len(first.Notifications) != 3 {
		t.Fatalf("first read = %v", recIDs(first))
	}

	d.remove(2)
	d.add(row{recID: 4, bundle: "app", delivered: delivered(when.Add(time.Minute)), data: request(map[string]any{"body": "new"})})
	second, err := source.Since(0)
	if err != nil || !same(recIDs(second), 1, 3, 4) {
		t.Fatalf("second read = %v, %v; want the removed row gone and the new one present (each read opens a fresh connection)", recIDs(second), err)
	}
}

func TestACorruptPlistIsSkippedAndReported(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 1, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "good"})})
	d.add(row{recID: 2, bundle: "app", delivered: delivered(when), data: []byte("this is not a plist")})
	d.add(row{recID: 3, bundle: "app", delivered: delivered(when), data: nil})
	d.add(row{recID: 4, bundle: "app", delivered: delivered(when), data: func() []byte {
		blob, _ := plist.Marshal(map[string]any{"app": "x", "date": 1.0}, plist.BinaryFormat)
		return blob // valid plist without a "req" dictionary
	}()})
	d.add(row{recID: 5, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "also good"})})

	batch, err := ingest.NewMacOS(d.path).Since(0)
	if err != nil {
		t.Fatalf("corrupt rows must not fail the read: %v", err)
	}
	if !same(recIDs(batch), 1, 5) {
		t.Errorf("notifications = %v, want only the readable rows 1 and 5", recIDs(batch))
	}
	var skipped []int64
	for _, s := range batch.Skipped {
		if s.Err == nil {
			t.Errorf("skipped row %d has no reason", s.RecID)
		}
		skipped = append(skipped, s.RecID)
	}
	if !same(skipped, 2, 3, 4) {
		t.Errorf("skipped = %v, want [2 3 4]", skipped)
	}
}

func TestMissingDatabaseGivesAClearError(t *testing.T) {
	first := filepath.Join(t.TempDir(), "nope", "db")
	second := filepath.Join(t.TempDir(), "also-nope", "db")
	_, err := ingest.NewMacOS(first, second).Since(0)
	if !errors.Is(err, ingest.ErrNoDatabase) {
		t.Fatalf("err = %v, want ErrNoDatabase", err)
	}
	for _, path := range []string{first, second} {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("error %q should list the probed path %s", err, path)
		}
	}
}

func TestTheFirstExistingPathIsUsed(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 1, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "x"})})
	missing := filepath.Join(t.TempDir(), "newer-macos", "db")

	batch, err := ingest.NewMacOS(missing, d.path).Since(0)
	if err != nil || len(batch.Notifications) != 1 {
		t.Fatalf("Since with a missing first path = %v, %v; want it to fall back to the second", recIDs(batch), err)
	}
}

func TestAnUnreadableFileIsAnErrorNotAnEmptyResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	if err := os.WriteFile(path, []byte("this is not a sqlite database at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.NewMacOS(path).Since(0); err == nil {
		t.Fatal("reading a non-database file succeeded")
	}
}

func TestReadingNeverModifiesTheDatabase(t *testing.T) {
	d := newDB(t)
	d.add(row{recID: 1, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "x"})})
	d.conn.Close()
	before, _ := os.ReadFile(d.path)

	if _, err := ingest.NewMacOS(d.path).Since(0); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(d.path)
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("the notification database changed after a read")
	}
}

func TestDefaultPathsProbeTheCurrentLocationFirst(t *testing.T) {
	paths := ingest.DefaultPaths("/home/me")
	if len(paths) < 2 {
		t.Fatalf("paths = %v, want the current and an older location", paths)
	}
	want := "/home/me/Library/Group Containers/group.com.apple.usernoted/db2/db"
	if paths[0] != want {
		t.Errorf("first path = %q, want %q", paths[0], want)
	}
}

func TestPathsWithSpacesAndPunctuationWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Group Containers (test)", "group.com.apple.usernoted", "db2", "db")
	d := newDBAt(t, path)
	d.add(row{recID: 1, bundle: "app", delivered: delivered(when), data: request(map[string]any{"body": "x"})})
	batch, err := ingest.NewMacOS(path).Since(0)
	if err != nil || len(batch.Notifications) != 1 {
		t.Fatalf("Since = %v, %v", recIDs(batch), err)
	}
}
