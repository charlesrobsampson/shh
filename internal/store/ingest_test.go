package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestIngestCursorStartsAbsentAndRoundTrips(t *testing.T) {
	s := open(t)
	if _, ok, err := s.IngestCursor(); err != nil || ok {
		t.Fatalf("fresh store: ok=%v err=%v, want no cursor", ok, err)
	}
	at := t0.Add(1500 * time.Millisecond)
	if err := s.SetIngestCursor(at); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.IngestCursor()
	if err != nil || !ok || !got.Equal(at) {
		t.Fatalf("cursor = %v, %v, %v; want %v", got, ok, err, at)
	}
	later := at.Add(time.Hour)
	s.SetIngestCursor(later)
	if got, _, _ := s.IngestCursor(); !got.Equal(later) {
		t.Fatalf("cursor after update = %v, want %v", got, later)
	}
}

func TestSeenRowsAreKeyedOnRecordIDAndDeliveryTime(t *testing.T) {
	s := open(t)
	if err := s.MarkSeen(7, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSeen(7, t0); err != nil {
		t.Fatalf("marking twice must be harmless: %v", err)
	}
	tests := []struct {
		name      string
		id        int64
		delivered time.Time
		want      bool
	}{
		{"the same row", 7, t0, true},
		{"the same id delivered later is a different notification", 7, t0.Add(time.Minute), false},
		{"another id at the same time", 8, t0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.IsSeen(tt.id, tt.delivered)
			if err != nil || got != tt.want {
				t.Fatalf("IsSeen = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestPruneSeenDropsOnlyOlderRowsThatAreNotKept(t *testing.T) {
	seen := func(s *store.Store) map[int64]bool {
		out := map[int64]bool{}
		for id := int64(1); id <= 4; id++ {
			out[id], _ = s.IsSeen(id, t0.Add(time.Duration(id-1)*time.Minute))
		}
		return out
	}
	tests := []struct {
		name string
		keep func(int64, time.Time) bool
		want map[int64]bool
	}{
		{"nothing kept", nil, map[int64]bool{1: false, 2: false, 3: true, 4: true}},
		{"a kept row survives even though it is old", func(id int64, _ time.Time) bool { return id == 1 }, map[int64]bool{1: true, 2: false, 3: true, 4: true}},
		{"keep is not asked about newer rows", func(id int64, _ time.Time) bool { return id >= 3 }, map[int64]bool{1: false, 2: false, 3: true, 4: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			for id := int64(1); id <= 4; id++ {
				s.MarkSeen(id, t0.Add(time.Duration(id-1)*time.Minute))
			}
			if err := s.PruneSeen(t0.Add(2*time.Minute), tt.keep); err != nil { // rows 1 and 2 are older than the cut-off
				t.Fatal(err)
			}
			got := seen(s)
			for id, want := range tt.want {
				if got[id] != want {
					t.Errorf("row %d seen = %v, want %v (cut-off is exclusive)", id, got[id], want)
				}
			}
		})
	}
}

func TestIngestStateSurvivesReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first.SetIngestCursor(t0)
	first.MarkSeen(5, t0)
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got, ok, _ := second.IngestCursor(); !ok || !got.Equal(t0) {
		t.Errorf("cursor after reopen = %v, %v", got, ok)
	}
	if seen, _ := second.IsSeen(5, t0); !seen {
		t.Error("seen row lost after reopen")
	}
}
