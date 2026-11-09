package ingest

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"howett.net/plist"

	_ "modernc.org/sqlite"
)

// ErrNoDatabase means none of the known notification database locations exists.
var ErrNoDatabase = errors.New("notification database not found")

// cocoaEpoch is 2001-01-01 UTC, which macOS uses as time zero.
const cocoaEpoch = 978307200

// MacOS reads the Notification Center database.
type MacOS struct {
	paths []string
}

// NewMacOS reads from the first of paths that exists.
func NewMacOS(paths ...string) *MacOS {
	return &MacOS{paths: paths}
}

// DefaultPaths lists where the database lives for a home directory, newest macOS first.
func DefaultPaths(home string) []string {
	return []string{
		filepath.Join(home, "Library", "Group Containers", "group.com.apple.usernoted", "db2", "db"),
		filepath.Join(home, "Library", "Group Containers", "group.com.apple.usernotificationsd", "db"),
	}
}

// Since reads the database read-only, over a fresh connection each time so it always sees the latest committed rows.
func (m *MacOS) Since(recID int64) (Batch, error) {
	path, err := m.find()
	if err != nil {
		return Batch{}, err
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Opaque: url.PathEscape(path), RawQuery: "mode=ro"}).String())
	if err != nil {
		return Batch{}, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT r.rec_id, a.identifier, r.delivered_date, r.data
		FROM record r JOIN app a USING (app_id) WHERE r.rec_id > ? ORDER BY r.rec_id`, recID)
	if err != nil {
		return Batch{}, fmt.Errorf("reading %s (does this terminal need Full Disk Access?): %w", path, err)
	}
	defer rows.Close()

	var batch Batch
	for rows.Next() {
		var (
			id        int64
			bundle    sql.NullString
			delivered sql.NullFloat64
			data      []byte
		)
		if err := rows.Scan(&id, &bundle, &delivered, &data); err != nil {
			return Batch{}, err
		}
		n, err := decode(data)
		if err != nil {
			batch.Skipped = append(batch.Skipped, Skipped{RecID: id, Err: err})
			continue
		}
		n.RecID, n.BundleID = id, bundle.String
		if delivered.Valid {
			n.Delivered = cocoaTime(delivered.Float64)
		}
		batch.Notifications = append(batch.Notifications, n)
	}
	return batch, rows.Err()
}

func (m *MacOS) find() (string, error) {
	for _, path := range m.paths {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: looked in %s", ErrNoDatabase, strings.Join(m.paths, ", "))
}

type payload struct {
	Req *struct {
		Title      string `plist:"titl"`
		Subtitle   string `plist:"subt"`
		Body       string `plist:"body"`
		Category   string `plist:"cate"`
		Identifier string `plist:"iden"`
		UserInfo   []byte `plist:"usda"`
	} `plist:"req"`
}

func decode(data []byte) (Notification, error) {
	var p payload
	if _, err := plist.Unmarshal(data, &p); err != nil {
		return Notification{}, fmt.Errorf("undecodable plist: %w", err)
	}
	if p.Req == nil {
		return Notification{}, errors.New("plist has no req dictionary")
	}
	return Notification{
		Title: p.Req.Title, Subtitle: p.Req.Subtitle, Body: p.Req.Body, Category: p.Req.Category, Identifier: p.Req.Identifier,
		Thread: threadOf(p.Req.UserInfo),
	}, nil
}

func cocoaTime(seconds float64) time.Time {
	whole, fraction := math.Modf(seconds)
	return time.Unix(cocoaEpoch+int64(whole), int64(fraction*1e9)).UTC()
}
