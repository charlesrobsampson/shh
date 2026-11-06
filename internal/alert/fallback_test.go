package alert_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
)

func TestTheFallbackPostsAnOwnedNotification(t *testing.T) {
	var gotName string
	var gotArgs []string
	f := alert.NewFallback(alert.FallbackConfig{
		OwnPrefix: "[shh]",
		Run:       func(name string, args ...string) error { gotName, gotArgs = name, args; return nil },
	})
	req := alert.Request{Title: "Ana", Body: "prod is down\nplease join", Kind: config.KindMessage}
	if err := f.Show(req); err != nil {
		t.Fatal(err)
	}
	if gotName != "osascript" {
		t.Errorf("command = %q, want osascript", gotName)
	}
	joined := strings.Join(gotArgs, "\x00")
	if !strings.Contains(joined, "[shh] Ana") {
		t.Errorf("arguments %q: the title must start with the own-banner prefix so the suppressor leaves it alone", gotArgs)
	}
	if !strings.Contains(joined, "prod is down\nplease join") {
		t.Errorf("arguments %q lost the body", gotArgs)
	}
}

func TestTheFallbackPassesTextAsArgumentsNotAsScriptSource(t *testing.T) {
	var args []string
	f := alert.NewFallback(alert.FallbackConfig{OwnPrefix: "[shh]", Run: func(_ string, a ...string) error { args = a; return nil }})
	nasty := `"; do shell script "touch /tmp/pwned" --` + "\n" + `'quote'`
	if err := f.Show(alert.Request{Title: nasty, Body: nasty}); err != nil {
		t.Fatal(err)
	}
	for i, a := range args {
		if a == "-e" && i+1 < len(args) && strings.Contains(args[i+1], "pwned") {
			t.Fatalf("notification text ended up inside the AppleScript source: %q", args[i+1])
		}
	}
	found := 0
	for _, a := range args {
		if strings.Contains(a, "pwned") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("the title and body should each travel as a plain argument; found %d in %q", found, args)
	}
}

func TestTheFallbackReportsAFailedCommand(t *testing.T) {
	boom := errors.New("osascript: not allowed")
	f := alert.NewFallback(alert.FallbackConfig{OwnPrefix: "[shh]", Run: func(string, ...string) error { return boom }})
	if err := f.Show(alert.Request{Title: "x"}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the command failure", err)
	}
}

func TestTheFallbackDefaultsToTheOwnPrefix(t *testing.T) {
	var args []string
	f := alert.NewFallback(alert.FallbackConfig{Run: func(_ string, a ...string) error { args = a; return nil }})
	f.Show(alert.Request{Title: "Ana"})
	if !strings.Contains(strings.Join(args, " "), "[shh] Ana") {
		t.Errorf("arguments %q should use the default [shh] prefix", args)
	}
}
