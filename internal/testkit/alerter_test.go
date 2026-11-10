package testkit_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/suppress"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var (
	_ alert.Alerter       = (*testkit.FakeAlerter)(nil)
	_ suppress.Suppressor = (*testkit.FakeSuppressor)(nil)
)

func TestFakeAlerterRecordsWhatItShows(t *testing.T) {
	a := testkit.NewFakeAlerter()
	if len(a.Shown()) != 0 {
		t.Fatal("a new alerter has already shown something")
	}
	first := alert.Alert{Kind: config.KindCalendar, Title: "Sync", Body: "starts soon"}
	second := alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", BundleID: "com.microsoft.teams2"}
	a.Show(first)
	a.Show(second)

	got := a.Shown()
	if len(got) != 2 || !reflect.DeepEqual(got[0], first) || !reflect.DeepEqual(got[1], second) {
		t.Fatalf("Shown = %+v, want both alerts in order", got)
	}
	got[0].Title = "changed"
	if a.Shown()[0].Title != "Sync" {
		t.Error("Shown must return a copy")
	}
}

func TestFakeAlerterCanFailAndRecover(t *testing.T) {
	a := testkit.NewFakeAlerter()
	boom := errors.New("display unavailable")
	a.FailWith(boom)
	if err := a.Show(alert.Alert{Title: "x"}); err != boom {
		t.Fatalf("err = %v, want the configured failure", err)
	}
	if len(a.Shown()) != 0 {
		t.Error("a failed Show must not count as shown")
	}
	a.FailWith(nil)
	if err := a.Show(alert.Alert{Title: "x"}); err != nil || len(a.Shown()) != 1 {
		t.Fatalf("after recovering: err=%v shown=%d", err, len(a.Shown()))
	}
}

func TestFakeSuppressorTracksItsState(t *testing.T) {
	s := testkit.NewFakeSuppressor()
	if s.Running() {
		t.Fatal("a new suppressor is running")
	}
	s.Start()
	s.Start() // idempotent
	if !s.Running() || s.Starts() != 1 {
		t.Fatalf("running=%v starts=%d, want running after one effective start", s.Running(), s.Starts())
	}
	s.Stop()
	s.Stop()
	if s.Running() || s.Stops() != 1 {
		t.Fatalf("running=%v stops=%d, want stopped after one effective stop", s.Running(), s.Stops())
	}
}

func TestFakeSuppressorCanFail(t *testing.T) {
	s := testkit.NewFakeSuppressor()
	boom := errors.New("no accessibility permission")
	s.FailWith(boom)
	if err := s.Start(); err != boom || s.Running() {
		t.Fatalf("Start err=%v running=%v, want the failure and not running", err, s.Running())
	}
}
