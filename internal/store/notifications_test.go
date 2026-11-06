package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/store"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func open(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr[T any](v T) *T { return &v }

func sample(mut func(*store.Notification)) store.Notification {
	n := store.Notification{
		Source: "Teams", Sender: "Ana", Title: "Ping", Body: "server is down",
		Timestamp: t0, Status: store.StatusBlocked, Score: 0.4, Boost: 0.1,
		ThresholdAtDecision: ptr(65), Reason: "keyword: down", BundleID: "com.microsoft.teams2",
	}
	if mut != nil {
		mut(&n)
	}
	return n
}

func insert(t *testing.T, s *store.Store, n store.Notification) int64 {
	t.Helper()
	id, inserted, err := s.InsertNotification(n)
	if err != nil || !inserted {
		t.Fatalf("InsertNotification: id=%d inserted=%v err=%v", id, inserted, err)
	}
	return id
}

func ids(list []store.Notification) []int64 {
	out := make([]int64, len(list))
	for i, n := range list {
		out[i] = n.ID
	}
	return out
}

func sameIDs(got []store.Notification, want ...int64) bool {
	g := ids(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

func TestInsertRoundTripKeepsEveryField(t *testing.T) {
	s := open(t)
	in := sample(func(n *store.Notification) {
		n.SourceRecID = 42
		n.Timestamp = t0.Add(1500 * time.Millisecond)
		n.Status = store.StatusAlerted
	})
	id := insert(t, s, in)

	got, err := s.GetNotification(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Source != "Teams" || got.Sender != "Ana" || got.Title != "Ping" || got.Body != "server is down" ||
		got.Status != store.StatusAlerted || got.Score != 0.4 || got.Boost != 0.1 || got.Reason != "keyword: down" ||
		got.BundleID != "com.microsoft.teams2" || got.SourceRecID != 42 {
		t.Errorf("round trip = %+v", got)
	}
	if !got.Timestamp.Equal(in.Timestamp) {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, in.Timestamp)
	}
	if got.ThresholdAtDecision == nil || *got.ThresholdAtDecision != 65 {
		t.Errorf("ThresholdAtDecision = %v, want 65", got.ThresholdAtDecision)
	}
	if got.IsRead || got.IsDismissedInbox || got.IsTrained || got.UserFeedback != "" || got.UserLabel != nil {
		t.Errorf("a new row must start unread, undismissed and untrained: %+v", got)
	}
}

func TestInsertRequiresTheThresholdAtDecision(t *testing.T) {
	s := open(t)
	_, _, err := s.InsertNotification(sample(func(n *store.Notification) { n.ThresholdAtDecision = nil }))
	if err == nil {
		t.Fatal("insert without a threshold succeeded")
	}
}

func TestInsertRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*store.Notification)
	}{
		{"unknown status", func(n *store.Notification) { n.Status = "maybe" }},
		{"score above 1", func(n *store.Notification) { n.Score = 1.2 }},
		{"boost above the 0.4 cap", func(n *store.Notification) { n.Boost = 0.5 }},
		{"threshold 100", func(n *store.Notification) { n.ThresholdAtDecision = ptr(100) }},
		{"negative threshold", func(n *store.Notification) { n.ThresholdAtDecision = ptr(-1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := open(t).InsertNotification(sample(tt.mut)); err == nil {
				t.Fatal("insert succeeded, want an error")
			}
		})
	}
}

func TestDuplicateInsertIsIgnored(t *testing.T) {
	tests := []struct {
		name         string
		second       func(*store.Notification)
		wantInserted bool
		wantRows     int
	}{
		{"same macOS record and delivery time", func(n *store.Notification) {}, false, 1},
		{"reused record id with a new delivery time is a different notification", func(n *store.Notification) { n.Timestamp = t0.Add(time.Hour) }, true, 2},
		{"different record id", func(n *store.Notification) { n.SourceRecID = 8 }, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			insert(t, s, sample(func(n *store.Notification) { n.SourceRecID = 7 }))
			second := sample(func(n *store.Notification) { n.SourceRecID = 7; tt.second(n) })
			_, inserted, err := s.InsertNotification(second)
			if err != nil || inserted != tt.wantInserted {
				t.Fatalf("inserted=%v err=%v, want inserted=%v", inserted, err, tt.wantInserted)
			}
			all, _ := s.ListNotifications(store.Filter{})
			if len(all) != tt.wantRows {
				t.Fatalf("rows = %d, want %d", len(all), tt.wantRows)
			}
		})
	}

	t.Run("rows without a macOS record id are never treated as duplicates", func(t *testing.T) {
		s := open(t)
		insert(t, s, sample(nil))
		insert(t, s, sample(nil))
	})
}

func TestTabViewsFollowTheFlags(t *testing.T) {
	label := &store.Training{Label: ptr(80)}
	tests := []struct {
		name      string
		steps     []func(s *store.Store, a, b, c int64) error
		wantInbox []string // by letter, newest first
		wantTrain []string
	}{
		{"fresh rows are in both lists", nil, []string{"c", "b", "a"}, []string{"c", "b", "a"}},
		{"dismissing removes from the inbox only", []func(*store.Store, int64, int64, int64) error{
			func(s *store.Store, a, b, c int64) error { return s.SetDismissed(b, true) },
		}, []string{"c", "a"}, []string{"c", "b", "a"}},
		{"training removes from the train list only", []func(*store.Store, int64, int64, int64) error{
			func(s *store.Store, a, b, c int64) error { return s.Train(b, *label) },
		}, []string{"c", "b", "a"}, []string{"c", "a"}},
		{"both flags remove from both lists", []func(*store.Store, int64, int64, int64) error{
			func(s *store.Store, a, b, c int64) error { return s.SetDismissed(a, true) },
			func(s *store.Store, a, b, c int64) error { return s.Train(a, store.Training{Feedback: "y"}) },
		}, []string{"c", "b"}, []string{"c", "b"}},
		{"undoing both brings the row back", []func(*store.Store, int64, int64, int64) error{
			func(s *store.Store, a, b, c int64) error { return s.SetDismissed(a, true) },
			func(s *store.Store, a, b, c int64) error { return s.Train(a, store.Training{Feedback: "n"}) },
			func(s *store.Store, a, b, c int64) error { return s.SetDismissed(a, false) },
			func(s *store.Store, a, b, c int64) error { return s.Untrain(a) },
		}, []string{"c", "b", "a"}, []string{"c", "b", "a"}},
		{"reading changes neither list", []func(*store.Store, int64, int64, int64) error{
			func(s *store.Store, a, b, c int64) error { return s.SetRead(c, true) },
		}, []string{"c", "b", "a"}, []string{"c", "b", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			a := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0 }))
			b := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0.Add(time.Minute) }))
			c := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0.Add(2 * time.Minute) }))
			for _, do := range tt.steps {
				if err := do(s, a, b, c); err != nil {
					t.Fatal(err)
				}
			}
			byLetter := map[string]int64{"a": a, "b": b, "c": c}
			want := func(letters []string) []int64 {
				var out []int64
				for _, l := range letters {
					out = append(out, byLetter[l])
				}
				return out
			}
			inbox, _ := s.ListNotifications(store.Filter{View: store.ViewInbox})
			train, _ := s.ListNotifications(store.Filter{View: store.ViewTrain})
			if !sameIDs(inbox, want(tt.wantInbox)...) {
				t.Errorf("inbox = %v, want %v", ids(inbox), want(tt.wantInbox))
			}
			if !sameIDs(train, want(tt.wantTrain)...) {
				t.Errorf("train = %v, want %v", ids(train), want(tt.wantTrain))
			}
		})
	}
}

func TestFlagsAreIndependent(t *testing.T) {
	s := open(t)
	id := insert(t, s, sample(nil))
	if err := s.SetRead(id, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNotification(id)
	if !got.IsRead || got.IsDismissedInbox || got.IsTrained {
		t.Fatalf("after SetRead: %+v", got)
	}
	if err := s.SetDismissed(id, true); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetNotification(id)
	if !got.IsRead || !got.IsDismissedInbox || got.IsTrained {
		t.Fatalf("after SetDismissed: %+v", got)
	}
}

func TestTrainingStoresQuickFeedbackOrAnExactLabel(t *testing.T) {
	tests := []struct {
		name         string
		training     store.Training
		wantErr      bool
		wantFeedback string
		wantLabel    *int
	}{
		{"quick yes", store.Training{Feedback: "y"}, false, "y", nil},
		{"quick no", store.Training{Feedback: "n"}, false, "n", nil},
		{"exact label", store.Training{Label: ptr(80)}, false, "", ptr(80)},
		{"label 0", store.Training{Label: ptr(0)}, false, "", ptr(0)},
		{"label 100 is allowed", store.Training{Label: ptr(100)}, false, "", ptr(100)},
		{"label 101", store.Training{Label: ptr(101)}, true, "", nil},
		{"negative label", store.Training{Label: ptr(-1)}, true, "", nil},
		{"nothing given", store.Training{}, true, "", nil},
		{"both given", store.Training{Feedback: "y", Label: ptr(50)}, true, "", nil},
		{"unknown feedback", store.Training{Feedback: "maybe"}, true, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := open(t)
			id := insert(t, s, sample(nil))
			err := s.Train(id, tt.training)
			got, _ := s.GetNotification(id)
			if tt.wantErr {
				if err == nil || got.IsTrained {
					t.Fatalf("err=%v trained=%v, want an error and no change", err, got.IsTrained)
				}
				return
			}
			if err != nil || !got.IsTrained || got.UserFeedback != tt.wantFeedback {
				t.Fatalf("err=%v got=%+v", err, got)
			}
			if (got.UserLabel == nil) != (tt.wantLabel == nil) || (got.UserLabel != nil && *got.UserLabel != *tt.wantLabel) {
				t.Fatalf("UserLabel = %v, want %v", got.UserLabel, tt.wantLabel)
			}
		})
	}
}

func TestUntrainClearsTheFeedback(t *testing.T) {
	s := open(t)
	id := insert(t, s, sample(nil))
	s.Train(id, store.Training{Label: ptr(70)})
	if err := s.Untrain(id); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNotification(id)
	if got.IsTrained || got.UserFeedback != "" || got.UserLabel != nil {
		t.Fatalf("after Untrain: %+v", got)
	}
}

func TestUpdatesOnUnknownRowsReportNotFound(t *testing.T) {
	s := open(t)
	updates := map[string]error{
		"get":       func() error { _, err := s.GetNotification(99); return err }(),
		"read":      s.SetRead(99, true),
		"dismiss":   s.SetDismissed(99, true),
		"train":     s.Train(99, store.Training{Feedback: "y"}),
		"untrain":   s.Untrain(99),
		"summarize": s.SetSummary(99, "x"),
	}
	for name, err := range updates {
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestSummaryIsStored(t *testing.T) {
	s := open(t)
	id := insert(t, s, sample(nil))
	if err := s.SetSummary(id, "outage in payments"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNotification(id)
	if got.Summary != "outage in payments" {
		t.Fatalf("Summary = %q", got.Summary)
	}
}

func TestListFilters(t *testing.T) {
	s := open(t)
	mk := func(source string, minutes int, status store.Status, score float64) int64 {
		return insert(t, s, sample(func(n *store.Notification) {
			n.Source, n.Status, n.Score = source, status, score
			n.Timestamp = t0.Add(time.Duration(minutes) * time.Minute)
		}))
	}
	teamsOld := mk("Teams", 0, store.StatusBlocked, 0.10)
	outlookMid := mk("Outlook", 10, store.StatusAlerted, 0.65)
	teamsMid := mk("Teams", 20, store.StatusAlerted, 0.57)
	outlookNew := mk("Outlook", 30, store.StatusBlocked, 0.99)

	tests := []struct {
		name   string
		filter store.Filter
		want   []int64
	}{
		{"no filter, newest first", store.Filter{}, []int64{outlookNew, teamsMid, outlookMid, teamsOld}},
		{"by source", store.Filter{Source: "Teams"}, []int64{teamsMid, teamsOld}},
		{"by status", store.Filter{Status: store.StatusAlerted}, []int64{teamsMid, outlookMid}},
		{"since is inclusive", store.Filter{Since: t0.Add(10 * time.Minute)}, []int64{outlookNew, teamsMid, outlookMid}},
		{"min score uses the 0-100 scale", store.Filter{MinScore: 65}, []int64{outlookNew, outlookMid}},
		{"min score rounds like the display (0.57 is 57)", store.Filter{MinScore: 57}, []int64{outlookNew, teamsMid, outlookMid}},
		{"filters combine", store.Filter{Source: "Outlook", Status: store.StatusBlocked, MinScore: 50}, []int64{outlookNew}},
		{"combined filters can match nothing", store.Filter{Source: "Teams", MinScore: 90}, nil},
		{"limit keeps the newest", store.Filter{Limit: 2}, []int64{outlookNew, teamsMid}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListNotifications(tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !sameIDs(got, tt.want...) {
				t.Fatalf("got %v, want %v", ids(got), tt.want)
			}
		})
	}
}

func TestViewsCombineWithFilters(t *testing.T) {
	s := open(t)
	teams := insert(t, s, sample(func(n *store.Notification) { n.Source = "Teams"; n.Timestamp = t0 }))
	outlook := insert(t, s, sample(func(n *store.Notification) { n.Source = "Outlook"; n.Timestamp = t0.Add(time.Minute) }))
	s.SetDismissed(teams, true)

	got, _ := s.ListNotifications(store.Filter{View: store.ViewInbox, Source: "Teams"})
	if len(got) != 0 {
		t.Errorf("dismissed Teams row still in the filtered inbox: %v", ids(got))
	}
	got, _ = s.ListNotifications(store.Filter{View: store.ViewTrain, Source: "Teams"})
	if !sameIDs(got, teams) {
		t.Errorf("train view for Teams = %v, want [%d]", ids(got), teams)
	}
	got, _ = s.ListNotifications(store.Filter{View: store.ViewInbox})
	if !sameIDs(got, outlook) {
		t.Errorf("inbox = %v, want [%d]", ids(got), outlook)
	}
}

func TestOrderingNewestFirstWithStableTies(t *testing.T) {
	s := open(t)
	first := insert(t, s, sample(nil))
	second := insert(t, s, sample(nil))
	got, _ := s.ListNotifications(store.Filter{})
	if !sameIDs(got, second, first) {
		t.Fatalf("got %v, want the later insert first when timestamps tie", ids(got))
	}
}

func TestTimestampsKeepSubSecondOrder(t *testing.T) {
	s := open(t)
	early := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0.Add(100 * time.Millisecond) }))
	late := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0.Add(900 * time.Millisecond) }))
	exact := insert(t, s, sample(func(n *store.Notification) { n.Timestamp = t0.Add(2 * time.Second) }))
	got, _ := s.ListNotifications(store.Filter{})
	if !sameIDs(got, exact, late, early) {
		t.Fatalf("got %v, want [%d %d %d]", ids(got), exact, late, early)
	}
}
