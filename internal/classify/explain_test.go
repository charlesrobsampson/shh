package classify_test

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charlesrobsampson/shh/internal/classify"
)

func TestExplainPicksTheFactorsThatPushedTheScore(t *testing.T) {
	factors := []classify.Contribution{
		{Label: "word: lunch", Value: -1.2},
		{Label: "vip sender", Value: 2.5},
		{Label: "word: outage", Value: 1.8},
		{Label: "app: teams", Value: 0.3},
		{Label: "word: menu", Value: -0.4},
		{Label: "phrase: server down", Value: 1.1},
		{Label: "question", Value: 0.9},
	}
	tests := []struct {
		name  string
		base  float64
		boost classify.BoostResult
		want  string
	}{
		{"an important score lists the biggest positive factors, three at most", 0.9, classify.BoostResult{},
			"important: vip sender, word: outage, phrase: server down"},
		{"an unimportant score lists the biggest negative factors", 0.1, classify.BoostResult{},
			"unimportant: word: lunch, word: menu"},
		{"the boost and its causes come last", 0.9, classify.BoostResult{Value: 0.3, Reasons: []string{"keyword: outage", "burst: 3 messages from Ana within 2m0s"}},
			"important: vip sender, word: outage, phrase: server down; boost +30 (keyword: outage, burst: 3 messages from Ana within 2m0s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classify.Explain(tt.base, factors, tt.boost); got != tt.want {
				t.Errorf("Explain = %q\nwant      %q", got, tt.want)
			}
		})
	}
}

func TestExplainACaseWithOnlyABoost(t *testing.T) {
	got := classify.Explain(0.5, nil, classify.BoostResult{Value: 0.1, Reasons: []string{"keyword: now"}})
	if want := "no strong signals; boost +10 (keyword: now)"; got != want {
		t.Errorf("Explain = %q, want %q", got, want)
	}
}

func TestExplainSkipsFunctionWordsAndRedundantUrgency(t *testing.T) {
	factors := []classify.Contribution{
		{Label: "word: are", Value: 2.0},
		{Label: "word: your", Value: 1.9},
		{Label: "urgency matched", Value: 1.8},
		{Label: "urgency: outage", Value: 1.5},
		{Label: "word: outage", Value: 1.0},
		{Label: "word: payments", Value: 0.9},
	}
	got := classify.Explain(0.9, factors, classify.BoostResult{})
	if want := "important: urgency: outage, word: outage, word: payments"; got != want {
		t.Errorf("Explain = %q, want %q", got, want)
	}
	// With no specific urgency factor the generic one is still the best explanation there is.
	got = classify.Explain(0.9, []classify.Contribution{{Label: "urgency matched", Value: 1.8}, {Label: "word: are", Value: 2}}, classify.BoostResult{})
	if want := "important: urgency matched"; got != want {
		t.Errorf("Explain = %q, want %q", got, want)
	}
}

func TestExplainIgnoresNoise(t *testing.T) {
	faint := []classify.Contribution{{Label: "word: a", Value: 0.01}, {Label: "word: b", Value: 0.04}}
	if got := classify.Explain(0.8, faint, classify.BoostResult{}); got != "no strong signals" {
		t.Errorf("Explain = %q, want no strong signals for faint factors", got)
	}
	if got := classify.Explain(0.8, nil, classify.BoostResult{}); got != "no strong signals" {
		t.Errorf("Explain = %q with no factors at all", got)
	}
}

func TestExplainOrderingIsStable(t *testing.T) {
	tied := []classify.Contribution{
		{Label: "word: zebra", Value: 1}, {Label: "word: apple", Value: 1}, {Label: "word: mango", Value: 1}, {Label: "word: kiwi", Value: 1},
	}
	want := "important: word: apple, word: kiwi, word: mango"
	for i := 0; i < 20; i++ {
		// Shuffle the input order; ties must always resolve alphabetically.
		rotated := append(append([]classify.Contribution{}, tied[i%4:]...), tied[:i%4]...)
		if got := classify.Explain(0.9, rotated, classify.BoostResult{}); got != want {
			t.Fatalf("rotation %d: %q, want %q", i, got, want)
		}
	}
}

func TestExplainLengthIsBounded(t *testing.T) {
	long := strings.Repeat("extraordinarily-long-feature-name ", 10)
	factors := []classify.Contribution{{Label: "phrase: " + long, Value: 3}, {Label: "word: " + long, Value: 2}, {Label: "app: " + long, Value: 1}}
	boost := classify.BoostResult{Value: 0.4, Reasons: []string{"keyword: " + long, "burst: " + long}}
	got := classify.Explain(0.9, factors, boost)
	if n := utf8.RuneCountInString(got); n > 140 {
		t.Errorf("reason is %d characters, want at most 140: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") && utf8.RuneCountInString(got) >= 140 {
		t.Errorf("a cut-off reason should end with an ellipsis: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation split a character: %q", got)
	}
}

func TestExplainShortensLongLabelsButKeepsShortOnes(t *testing.T) {
	got := classify.Explain(0.9, []classify.Contribution{{Label: "phrase: " + strings.Repeat("x", 100), Value: 2}, {Label: "vip sender", Value: 1}}, classify.BoostResult{})
	if !strings.Contains(got, "vip sender") || !strings.Contains(got, "…") {
		t.Errorf("reason = %q, want the long label shortened and the short one intact", got)
	}
}

// --- contributions from a real model ---

func TestContributionsAddUpToTheModelsScore(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	for _, tt := range heldOut {
		v := c.Features.Extract(tt.in)
		bias, _ := c.Model.Weights()
		z := bias
		for _, contribution := range c.Model.Contributions(v) {
			z += contribution.Value
		}
		if got, want := 1/(1+math.Exp(-z)), c.Model.Predict(v); math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: sigmoid(bias + contributions) = %v, Predict = %v", tt.name, got, want)
		}
	}
}

func TestContributionsMergeTheEmbeddingIntoOneFactor(t *testing.T) {
	s := memoryStore(t)
	f := embeddingFeatures(t)
	m, err := classify.NewModel(s, f)
	if err != nil {
		t.Fatal(err)
	}
	in := classify.Input{Body: "server down now"}
	for i := 0; i < 30; i++ {
		m.Train(0, f.Extract(in), classify.ExactLabel(95))
	}
	meaning := 0
	for _, c := range m.Contributions(f.Extract(in)) {
		if c.Label == "meaning" {
			meaning++
			if c.Value <= 0 {
				t.Errorf("the trained meaning factor is %v, want it to push the score up", c.Value)
			}
		}
	}
	if meaning != 1 {
		t.Errorf("%d meaning factors, want the dense entries merged into one", meaning)
	}
}

func TestEveryScoreComesWithAReason(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	for _, tt := range heldOut {
		score, err := c.Score(tt.in, t0)
		if err != nil {
			t.Fatal(err)
		}
		if score.Reason == "" {
			t.Errorf("%s: no reason", tt.name)
		}
		if n := utf8.RuneCountInString(score.Reason); n > 140 {
			t.Errorf("%s: reason has %d characters", tt.name, n)
		}
		prefix := "unimportant:"
		if tt.urgent {
			prefix = "important:"
		}
		if !strings.HasPrefix(score.Reason, prefix) && !strings.HasPrefix(score.Reason, "no strong signals") {
			t.Errorf("%s: reason %q should start with %q", tt.name, score.Reason, prefix)
		}
	}
}

func TestReasonsAreStable(t *testing.T) {
	a, b := newClassifier(t, memoryStore(t)), newClassifier(t, memoryStore(t))
	for _, tt := range heldOut {
		ra, _ := a.Score(tt.in, t0)
		rb, _ := b.Score(tt.in, t0)
		if ra.Reason != rb.Reason {
			t.Errorf("%s: %q vs %q", tt.name, ra.Reason, rb.Reason)
		}
	}
}

func TestTheReasonMentionsTheKeywordThatBoostedTheScore(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	score, _ := c.Score(classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "there is an outage"}, t0)
	if !strings.Contains(score.Reason, "boost +") || !strings.Contains(score.Reason, "keyword: outage") {
		t.Errorf("reason = %q, want the boost and the keyword behind it", score.Reason)
	}
}

func TestTheReasonMentionsALearnedPhrase(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	for i, filler := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliet"} {
		in := classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "red dragon " + filler + " " + filler + "x"}
		for round := 0; round < 3; round++ {
			c.Model.Train(0, c.Features.Extract(in), classify.ExactLabel(95))
		}
		_ = i
	}
	score, _ := c.Score(classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "update on the red dragon project"}, t0)
	if !strings.Contains(score.Reason, "dragon") {
		t.Errorf("reason = %q, want the learned phrase red dragon to show up", score.Reason)
	}
}

func TestTheReasonMentionsAVIPSender(t *testing.T) {
	r := defaultRules(t)
	vips := []string{"alice vip", "bruno vip", "chen vip", "dara vip", "emil vip", "fay vip", "gus vip", "hana vip"}
	unseen := "zed vip"
	r.VIPSenders = append(append([]string{}, vips...), unseen)
	c, err := classify.NewClassifier(memoryStore(t), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Each VIP is seen once, so only the shared "vip sender" flag learns from all of them.
	for i, name := range vips {
		in := classify.Input{Source: "Teams", Sender: name, Title: name, Body: "note number " + strings.Repeat("z", i+1)}
		for round := 0; round < 4; round++ {
			c.Model.Train(0, c.Features.Extract(in), classify.ExactLabel(95))
		}
	}
	score, err := c.Score(classify.Input{Source: "Teams", Sender: unseen, Title: unseen, Body: "following up on yesterday"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(score.Reason, "vip sender") {
		t.Errorf("reason = %q, want it to credit the VIP sender flag", score.Reason)
	}
}
