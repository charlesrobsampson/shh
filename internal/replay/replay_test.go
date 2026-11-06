package replay_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/replay"
	"github.com/charlesrobsampson/shh/internal/rules"
)

var update = flag.Bool("update", false, "rewrite the golden file")

func run(t *testing.T, input string, threshold int) string {
	t.Helper()
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := replay.Run(strings.NewReader(input), &out, replay.Options{Threshold: threshold, Rules: r}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func TestOutputIsStableForTheFixtureFile(t *testing.T) {
	input, err := os.ReadFile(filepath.Join("testdata", "fixture.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	got := run(t, string(input), 65)

	golden := filepath.Join("testdata", "fixture.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("replay output changed (run `go test ./internal/replay -update` if that is intended):\n--- got\n%s--- want\n%s", got, want)
	}
}

func lines(out string) []string {
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

func TestEachNotificationGetsADecisionAndASummaryFollows(t *testing.T) {
	out := run(t, `{"source":"Teams","sender":"Ana","title":"Ana","body":"prod is down, need you now"}
{"source":"Mail","sender":"Shop","title":"Sale","body":"20% off, unsubscribe any time"}
`, 65)
	got := lines(out)
	if len(got) != 3 {
		t.Fatalf("output has %d lines, want 2 decisions and a summary:\n%s", len(got), out)
	}
	if !strings.Contains(got[0], "ALERT") || !strings.Contains(got[0], "Teams") || !strings.Contains(got[0], "prod is down") {
		t.Errorf("first line = %q, want an alert for the Teams outage", got[0])
	}
	if !strings.Contains(got[1], "muted") || !strings.Contains(got[1], "Mail") {
		t.Errorf("second line = %q, want the promotion muted", got[1])
	}
	if want := "2 notifications: 1 alerted, 1 muted at threshold 65%"; got[2] != want {
		t.Errorf("summary = %q, want %q", got[2], want)
	}
}

func TestTheThresholdChangesTheDecisions(t *testing.T) {
	const input = `{"source":"Teams","sender":"Ana","title":"Ana","body":"can you review my PR today?"}
`
	tests := []struct {
		threshold int
		want      string
	}{{0, "ALERT"}, {35, "ALERT"}, {99, "muted"}}
	for _, tt := range tests {
		out := run(t, input, tt.threshold)
		if !strings.Contains(lines(out)[0], tt.want) {
			t.Errorf("at threshold %d the first line is %q, want %s", tt.threshold, lines(out)[0], tt.want)
		}
	}
}

func TestBurstsShowTheirBoost(t *testing.T) {
	out := run(t, `{"source":"Teams","sender":"Ana","title":"Ana","body":"hello","at":"2026-01-01T10:00:00Z"}
{"source":"Teams","sender":"Ana","title":"Ana","body":"are you there","at":"2026-01-01T10:00:20Z"}
{"source":"Teams","sender":"Ana","title":"Ana","body":"ping","at":"2026-01-01T10:00:40Z"}
`, 65)
	got := lines(out)
	if strings.Contains(got[0], "+10") || strings.Contains(got[1], "+10") {
		t.Errorf("the first two messages are not a burst yet:\n%s", out)
	}
	if !strings.Contains(got[2], "+10") {
		t.Errorf("the third message in 40 seconds should carry the +10 burst boost:\n%s", out)
	}
}

func TestLinesWithoutATimeAreSpacedApartSoTheyDoNotBurst(t *testing.T) {
	out := run(t, strings.Repeat(`{"source":"Teams","sender":"Ana","title":"Ana","body":"hello"}`+"\n", 4), 65)
	if strings.Contains(out, "+10") {
		t.Errorf("untimed lines formed a burst:\n%s", out)
	}
}

func TestLabelsAreShownNextToTheScore(t *testing.T) {
	out := run(t, `{"source":"Teams","sender":"Ana","title":"Ana","body":"prod is down, need you now","label":95}
`, 65)
	if !strings.Contains(lines(out)[0], "label 95") {
		t.Errorf("line = %q, want the label shown", lines(out)[0])
	}
}

func TestLongTextIsShortened(t *testing.T) {
	out := run(t, `{"source":"Teams","sender":"Ana","title":"Ana","body":"`+strings.Repeat("word ", 100)+`"}
`, 65)
	if first := lines(out)[0]; len(first) > 160 || !strings.Contains(first, "…") {
		t.Errorf("a 500-character body was not shortened (line is %d long): %q", len(first), first)
	}
}

func TestBadInputIsReportedWithItsLineNumber(t *testing.T) {
	r, _ := rules.Defaults()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"not json", `{"source":"Teams","body":"fine"}` + "\n" + `this is not json` + "\n", "line 2"},
		{"unknown field", `{"source":"Teams","titel":"typo","body":"x"}` + "\n", "titel"},
		{"bad time", `{"source":"Teams","body":"x","at":"yesterday"}` + "\n", "line 1"},
		{"label out of range", `{"source":"Teams","body":"x","label":150}` + "\n", "label"},
		{"no text at all", `{"source":"Teams"}` + "\n", "line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := replay.Run(strings.NewReader(tt.input), &out, replay.Options{Threshold: 65, Rules: r})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}

func TestAnEmptyFileJustSummarises(t *testing.T) {
	out := run(t, "\n\n", 65)
	if got := strings.TrimSpace(out); got != "0 notifications: 0 alerted, 0 muted at threshold 65%" {
		t.Errorf("output = %q", got)
	}
}

func TestInvalidThresholdsAreRejected(t *testing.T) {
	r, _ := rules.Defaults()
	for _, threshold := range []int{-1, 100} {
		if err := replay.Run(strings.NewReader(""), &bytes.Buffer{}, replay.Options{Threshold: threshold, Rules: r}); err == nil {
			t.Errorf("threshold %d accepted", threshold)
		}
	}
}
