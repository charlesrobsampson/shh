package alert_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
)

var _ alert.Alerter = (*alert.Log)(nil)

func TestLogWritesOneLinePerAlert(t *testing.T) {
	var out bytes.Buffer
	l := alert.NewLog(&out)
	if err := l.Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "prod is down\nplease join"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Show(alert.Alert{Kind: config.KindCalendar, Source: "Calendar", Title: "Weekly Sync", Body: "Starts at 09:10"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), out.String())
	}
	for i, want := range [][]string{{"message", "Teams", "Ana", "prod is down please join"}, {"calendar", "Calendar", "Weekly Sync", "Starts at 09:10"}} {
		for _, w := range want {
			if !strings.Contains(lines[i], w) {
				t.Errorf("line %d %q is missing %q (newlines inside a body must not break the line)", i, lines[i], w)
			}
		}
	}
}

func TestLogIsSafeForConcurrentUse(t *testing.T) {
	var out bytes.Buffer
	l := alert.NewLog(&out)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); l.Show(alert.Alert{Title: "x"}) }()
	}
	wg.Wait()
	if got := strings.Count(out.String(), "\n"); got != 20 {
		t.Fatalf("%d lines, want 20", got)
	}
}
