package classify_test

import (
	"math"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

var t0 = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func newBooster(t *testing.T) (*classify.Booster, *store.Store, *testkit.FakeClock) {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	return classify.NewBooster(r, s), s, testkit.NewFakeClock(t0)
}

func seed(t *testing.T, s *store.Store, source, sender string, at time.Time) {
	t.Helper()
	threshold := 65
	_, inserted, err := s.InsertNotification(store.Notification{
		Source: source, Sender: sender, Body: "earlier message", Timestamp: at,
		Status: store.StatusBlocked, Score: 0.2, ThresholdAtDecision: &threshold,
	})
	if err != nil || !inserted {
		t.Fatalf("seed: inserted=%v err=%v", inserted, err)
	}
}

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestKeywordBoosts(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		want       float64
		wantReason string
	}{
		{"no match", "lunch menu for friday", 0, ""},
		{"empty text", "", 0, ""},
		{"outage", "Payments outage reported", 0.3, "outage"},
		{"urgent", "urgent: please review", 0.3, "urgent"},
		{"asap", "need this ASAP", 0.3, "urgent"},
		{"prod", "prod deploy finished", 0.2, "prod"},
		{"down", "the site is down", 0.2, "down"},
		{"now", "call me now", 0.1, "now"},
		{"deadline", "deadline is friday", 0.1, "deadline"},
		{"words only, not substrings", "knowledge of known issues", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, _ := newBooster(t)
			got, err := b.Boost(classify.BoostInput{Source: "Teams", Sender: "Ana", Text: tt.text, At: t0})
			if err != nil {
				t.Fatal(err)
			}
			if !near(got.Value, tt.want) {
				t.Errorf("Boost = %v, want %v", got.Value, tt.want)
			}
			if tt.wantReason == "" {
				if len(got.Reasons) != 0 {
					t.Errorf("reasons = %v, want none", got.Reasons)
				}
				return
			}
			if !slices.Contains(got.Reasons, "keyword: "+tt.wantReason) {
				t.Errorf("reasons = %v, want one for %q", got.Reasons, tt.wantReason)
			}
		})
	}
}

func TestKeywordMatchingIgnoresCase(t *testing.T) {
	b, _, _ := newBooster(t)
	lower, _ := b.Boost(classify.BoostInput{Text: "outage", At: t0})
	upper, _ := b.Boost(classify.BoostInput{Text: "OUTAGE", At: t0})
	if lower.Value == 0 || !near(lower.Value, upper.Value) {
		t.Fatalf("lower = %v, upper = %v, want equal and non-zero", lower.Value, upper.Value)
	}
}

func TestSeveralKeywordsAddUp(t *testing.T) {
	b, _, _ := newBooster(t)
	got, _ := b.Boost(classify.BoostInput{Text: "prod is down", At: t0}) // 0.2 + 0.2
	if !near(got.Value, 0.4) || len(got.Reasons) != 2 {
		t.Fatalf("Boost = %v with reasons %v, want 0.4 from two keywords", got.Value, got.Reasons)
	}
}

func TestBoostIsCappedAtPointFour(t *testing.T) {
	b, _, _ := newBooster(t)
	got, _ := b.Boost(classify.BoostInput{Text: "urgent production outage, system is down now", At: t0})
	if !near(got.Value, 0.4) {
		t.Fatalf("Boost = %v, want the 0.4 cap", got.Value)
	}
}

func TestBurstFromTheSameSender(t *testing.T) {
	type earlier struct {
		source, sender string
		ago            time.Duration
	}
	tests := []struct {
		name    string
		history []earlier
		want    float64
	}{
		{"first message", nil, 0},
		{"second message", []earlier{{"Teams", "Ana", 30 * time.Second}}, 0},
		{"third message within two minutes", []earlier{{"Teams", "Ana", 30 * time.Second}, {"Teams", "Ana", 90 * time.Second}}, 0.1},
		{"exactly two minutes ago still counts", []earlier{{"Teams", "Ana", time.Second}, {"Teams", "Ana", 2 * time.Minute}}, 0.1},
		{"one of them is too old", []earlier{{"Teams", "Ana", 30 * time.Second}, {"Teams", "Ana", 3 * time.Minute}}, 0},
		{"other senders do not count", []earlier{{"Teams", "Bo", 10 * time.Second}, {"Teams", "Cy", 20 * time.Second}}, 0},
		{"other apps do not count", []earlier{{"Outlook", "Ana", 10 * time.Second}, {"Slack", "Ana", 20 * time.Second}}, 0},
		{"a long burst gives the same single boost", []earlier{
			{"Teams", "Ana", 5 * time.Second}, {"Teams", "Ana", 10 * time.Second}, {"Teams", "Ana", 15 * time.Second}, {"Teams", "Ana", 20 * time.Second},
		}, 0.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, s, clock := newBooster(t)
			clock.Advance(time.Hour)
			now := clock.Now()
			for _, e := range tt.history {
				seed(t, s, e.source, e.sender, now.Add(-e.ago))
			}
			got, err := b.Boost(classify.BoostInput{Source: "Teams", Sender: "Ana", Text: "hello", At: now})
			if err != nil {
				t.Fatal(err)
			}
			if !near(got.Value, tt.want) {
				t.Errorf("Boost = %v, want %v", got.Value, tt.want)
			}
			if burst := slices.ContainsFunc(got.Reasons, func(r string) bool { return len(r) >= 5 && r[:5] == "burst" }); burst != (tt.want > 0) {
				t.Errorf("reasons = %v, burst reason present = %v, want %v", got.Reasons, burst, tt.want > 0)
			}
		})
	}
}

func TestBurstAndKeywordsCombineUpToTheCap(t *testing.T) {
	b, s, _ := newBooster(t)
	seed(t, s, "Teams", "Ana", t0.Add(-10*time.Second))
	seed(t, s, "Teams", "Ana", t0.Add(-20*time.Second))

	got, _ := b.Boost(classify.BoostInput{Source: "Teams", Sender: "Ana", Text: "it is down", At: t0}) // 0.2 + 0.1
	if !near(got.Value, 0.3) {
		t.Errorf("keyword plus burst = %v, want 0.3", got.Value)
	}
	got, _ = b.Boost(classify.BoostInput{Source: "Teams", Sender: "Ana", Text: "urgent outage", At: t0}) // 0.6 + 0.1, capped
	if !near(got.Value, 0.4) {
		t.Errorf("capped total = %v, want 0.4", got.Value)
	}
}

func TestBurstIgnoredWithoutASender(t *testing.T) {
	b, s, _ := newBooster(t)
	seed(t, s, "Teams", "", t0.Add(-10*time.Second))
	seed(t, s, "Teams", "", t0.Add(-20*time.Second))
	got, err := b.Boost(classify.BoostInput{Source: "Teams", Sender: "", Text: "hello", At: t0})
	if err != nil || got.Value != 0 {
		t.Fatalf("Boost = %v, %v; want 0 (unknown senders are not a burst)", got.Value, err)
	}
}

func TestBoostUsesTheRulesItIsGiven(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	custom := rules.Rules{Urgency: []rules.Urgency{{
		Pattern: rules.Pattern{Name: "payroll", Regexp: regexp.MustCompile(`(?i)\bpayroll\b`)},
		Boost:   0.15,
	}}}
	b := classify.NewBooster(custom, s)

	got, _ := b.Boost(classify.BoostInput{Text: "payroll is late", At: t0})
	if !near(got.Value, 0.15) {
		t.Errorf("custom rule boost = %v, want 0.15", got.Value)
	}
	got, _ = b.Boost(classify.BoostInput{Text: "urgent outage", At: t0})
	if got.Value != 0 {
		t.Errorf("a default keyword boosted with custom rules: %v", got.Value)
	}
}
