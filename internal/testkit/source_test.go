package testkit_test

import (
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/ingest"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var _ ingest.Source = (*testkit.FakeSource)(nil)

func recIDs(t *testing.T, s *testkit.FakeSource, since int64) []int64 {
	t.Helper()
	batch, err := s.Since(since)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int64, len(batch.Notifications))
	for i, n := range batch.Notifications {
		out[i] = n.RecID
	}
	return out
}

func equal(got []int64, want ...int64) bool {
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

func TestFakeSourceAssignsIncreasingRecordIDs(t *testing.T) {
	s := testkit.NewFakeSource()
	first := s.Add(ingest.Notification{Title: "a"})
	second := s.Add(ingest.Notification{Title: "b"})
	if first != 1 || second != 2 {
		t.Fatalf("ids = %d, %d, want 1, 2", first, second)
	}
}

func TestFakeSourceSinceReturnsLaterRowsOldestFirst(t *testing.T) {
	s := testkit.NewFakeSource()
	for i := 0; i < 4; i++ {
		s.Add(ingest.Notification{Body: "x"})
	}
	tests := []struct {
		since int64
		want  []int64
	}{{0, []int64{1, 2, 3, 4}}, {2, []int64{3, 4}}, {4, nil}}
	for _, tt := range tests {
		if got := recIDs(t, s, tt.since); !equal(got, tt.want...) {
			t.Errorf("Since(%d) = %v, want %v", tt.since, got, tt.want)
		}
	}
}

func TestFakeSourceRemovedRowsDisappear(t *testing.T) {
	s := testkit.NewFakeSource()
	s.Add(ingest.Notification{})
	s.Add(ingest.Notification{})
	s.Add(ingest.Notification{})
	s.Remove(2)
	if got := recIDs(t, s, 0); !equal(got, 1, 3) {
		t.Fatalf("after Remove(2): %v, want [1 3]", got)
	}
	s.Remove(99) // removing something that is not there is harmless
}

func TestFakeSourceReusesTheTopRecordIDLikeSQLite(t *testing.T) {
	s := testkit.NewFakeSource()
	s.Add(ingest.Notification{Title: "old three"})
	s.Add(ingest.Notification{Title: "old two"})
	top := s.Add(ingest.Notification{Title: "old top"})
	s.Remove(top)

	reused := s.Add(ingest.Notification{Title: "new"})
	if reused != top {
		t.Fatalf("new id = %d, want the removed top id %d to be reused", reused, top)
	}

	s.Remove(1) // a removed lower id is not reused
	next := s.Add(ingest.Notification{Title: "later"})
	if next != top+1 {
		t.Fatalf("next id = %d, want %d", next, top+1)
	}
}

func TestFakeSourceKeepsTheNotificationFields(t *testing.T) {
	s := testkit.NewFakeSource()
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	id := s.Add(ingest.Notification{BundleID: "com.microsoft.teams2", Title: "t", Subtitle: "s", Body: "b", Category: "c", Delivered: at})
	batch, _ := s.Since(0)
	got := batch.Notifications[0]
	if got.RecID != id || got.BundleID != "com.microsoft.teams2" || got.Title != "t" || got.Body != "b" || !got.Delivered.Equal(at) {
		t.Fatalf("notification = %+v", got)
	}
}

func TestFakeSourceCanFail(t *testing.T) {
	s := testkit.NewFakeSource()
	s.FailWith(ingest.ErrNoDatabase)
	if _, err := s.Since(0); err != ingest.ErrNoDatabase {
		t.Fatalf("err = %v, want ErrNoDatabase", err)
	}
	s.FailWith(nil)
	if _, err := s.Since(0); err != nil {
		t.Fatalf("after clearing the failure: %v", err)
	}
}
