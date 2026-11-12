package classify_test

import (
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/store"
)

func newClassifier(t *testing.T, s *store.Store) *classify.Classifier {
	t.Helper()
	c, err := classify.NewClassifier(s, defaultRules(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func memoryStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTheSeedSetCoversTheMainSourcesAndBothEnds(t *testing.T) {
	examples, err := classify.SeedExamples()
	if err != nil {
		t.Fatal(err)
	}
	if len(examples) < 100 {
		t.Fatalf("seed set has %d examples, want at least 100", len(examples))
	}
	sources := map[string]int{}
	var high, low int
	for _, ex := range examples {
		if ex.Label < 0 || ex.Label > 100 {
			t.Errorf("label %d outside 0-100: %+v", ex.Label, ex.Input)
		}
		if ex.Input.Body == "" && ex.Input.Title == "" {
			t.Errorf("an example has no text: %+v", ex)
		}
		sources[ex.Input.Source]++
		switch {
		case ex.Label >= 70:
			high++
		case ex.Label <= 30:
			low++
		}
	}
	for _, source := range []string{"Teams", "Outlook", "Slack", "Mail", "System"} {
		if sources[source] < 5 {
			t.Errorf("only %d seed examples from %s", sources[source], source)
		}
	}
	if high < 25 || low < 25 {
		t.Errorf("seed set has %d clearly urgent and %d clearly unimportant examples, want at least 25 of each", high, low)
	}
}

// Written separately from the seed set, so these test generalisation, not memorisation.
var heldOut = []struct {
	name   string
	in     classify.Input
	urgent bool
}{
	{"site down, asap", classify.Input{Source: "Teams", Sender: "Lena", Title: "Lena", Body: "the website is down again, can you check ASAP?"}, true},
	{"incident bridge", classify.Input{Source: "Outlook", Sender: "Ops Lead", Title: "Re: outage", Body: "Production database is unreachable, join the incident call now"}, true},
	{"payments failing", classify.Input{Source: "Slack", Sender: "ops", Title: "ops", Body: "urgent: payments are failing for all customers"}, true},
	{"customer waiting", classify.Input{Source: "Teams", Sender: "Marco", Title: "Marco", Body: "are you available right now? a customer is waiting on a call"}, true},
	{"security alert", classify.Input{Source: "Mail", Sender: "Bank", Title: "Security alert", Body: "Unusual sign-in to your account, verify immediately"}, true},
	{"blocked release", classify.Input{Source: "Teams", Sender: "Priya", Title: "Priya", Body: "can you approve the release now? we are blocked"}, true},

	{"flash sale", classify.Input{Source: "Mail", Sender: "Daily Deals", Title: "Flash sale", Body: "30% off everything today only, unsubscribe at any time"}, false},
	{"hr newsletter", classify.Input{Source: "Outlook", Sender: "HR Newsletter", Title: "This week at the company", Body: "New hires, office events and the lunch menu"}, false},
	{"fyi slides", classify.Input{Source: "Teams", Sender: "Sam", Title: "Sam", Body: "FYI the slides from the town hall are posted"}, false},
	{"software update", classify.Input{Source: "System", Sender: "Software Update", Title: "Software Update", Body: "A new update is available, install tonight"}, false},
	{"funny video", classify.Input{Source: "Slack", Sender: "random", Title: "#random", Body: "check out this funny video of a dog"}, false},
	{"wiki changed", classify.Input{Source: "Outlook", Sender: "Wiki", Title: "Page updated", Body: "A page you follow was updated"}, false},
}

func TestSeededClassifierSeparatesUrgentFromUnimportantMessages(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	for _, tt := range heldOut {
		t.Run(tt.name, func(t *testing.T) {
			score, err := c.Score(tt.in, t0)
			if err != nil {
				t.Fatal(err)
			}
			points := score.Base * 100
			if tt.urgent && points <= 65 {
				t.Errorf("urgent message scored %.0f, want above 65 so it interrupts at medium", points)
			}
			if !tt.urgent && points > 35 {
				t.Errorf("unimportant message scored %.0f, want at most 35 so it is muted at light", points)
			}
		})
	}
}

func TestSeedingIsDeterministic(t *testing.T) {
	a, b := newClassifier(t, memoryStore(t)), newClassifier(t, memoryStore(t))
	for _, tt := range heldOut {
		sa, _ := a.Score(tt.in, t0)
		sb, _ := b.Score(tt.in, t0)
		if sa.Base != sb.Base {
			t.Fatalf("%s: %v vs %v", tt.name, sa.Base, sb.Base)
		}
	}
}

func TestTheSeededWeightsArePersistedAndNotRepeated(t *testing.T) {
	s := memoryStore(t)
	first := newClassifier(t, s)
	want, _ := first.Score(heldOut[0].in, t0)

	if _, ok, _ := s.LoadWeights(first.Features.Version()); !ok {
		t.Fatal("the seeded weights were not saved")
	}
	second := newClassifier(t, s)
	got, _ := second.Score(heldOut[0].in, t0)
	if got.Base != want.Base {
		t.Errorf("score after restart = %v, want the saved %v", got.Base, want.Base)
	}
}

func TestTheSeedIsNotUndoableButItsStateIsACheckpoint(t *testing.T) {
	s := memoryStore(t)
	c := newClassifier(t, s)
	if err := c.Model.Undo(1); err != classify.ErrNothingToUndo {
		t.Fatalf("Undo right after seeding: %v, want ErrNothingToUndo (the seed must not be undoable one example at a time)", err)
	}
	seeded := c.Model.Predict(c.Features.Extract(heldOut[0].in))

	c.Model.Train(0, c.Features.Extract(heldOut[0].in), classify.ExactLabel(0))
	if c.Model.Predict(c.Features.Extract(heldOut[0].in)) == seeded {
		t.Fatal("test setup: training changed nothing")
	}
	if err := c.Model.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := c.Model.Predict(c.Features.Extract(heldOut[0].in)); got != seeded {
		t.Errorf("rollback went to %v, want the seeded state %v", got, seeded)
	}
}

func TestScoreIncludesTheBoost(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	plain, _ := c.Score(classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "lunch menu for friday"}, t0)
	urgent, _ := c.Score(classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "prod outage, need you now"}, t0)
	if plain.Boost != 0 || urgent.Boost <= 0 {
		t.Fatalf("boosts = %v and %v, want 0 and a positive boost for the urgency words", plain.Boost, urgent.Boost)
	}
	if len(urgent.BoostReasons) == 0 {
		t.Error("a boosted score should say why")
	}
}

func TestBurstsBoostTheScore(t *testing.T) {
	s := memoryStore(t)
	c := newClassifier(t, s)
	for _, ago := range []time.Duration{20 * time.Second, 40 * time.Second} {
		seed(t, s, "Teams", "Ana", t0.Add(-ago))
	}
	score, _ := c.Score(classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "hello"}, t0)
	if score.Boost != 0.1 {
		t.Fatalf("boost = %v, want 0.1 for the third message from Ana within two minutes", score.Boost)
	}
}

func TestPretrainFitsTheExamplesWithoutLoggingEachOne(t *testing.T) {
	s := memoryStore(t)
	f := extractor(t, defaultRules(t))
	m, err := classify.NewModel(s, f)
	if err != nil {
		t.Fatal(err)
	}
	hi := classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana", Body: "everything is on fire"}
	lo := classify.Input{Source: "Mail", Sender: "Promo", Title: "Promo", Body: "weekly offers inside"}
	examples := []classify.Example{{Vector: f.Extract(hi), Label: 95}, {Vector: f.Extract(lo), Label: 5}}
	if err := m.Pretrain(examples, 200); err != nil {
		t.Fatal(err)
	}
	if got := m.Predict(f.Extract(hi)); got < 0.85 {
		t.Errorf("score for the high example = %v, want close to 0.95", got)
	}
	if got := m.Predict(f.Extract(lo)); got > 0.15 {
		t.Errorf("score for the low example = %v, want close to 0.05", got)
	}
	log, _ := s.TrainingLog(0)
	for _, e := range log {
		if e.Label != "checkpoint" {
			t.Fatalf("pretraining wrote an undoable log row: %+v", e)
		}
	}
	if len(log) != 1 {
		t.Errorf("log has %d rows, want a single checkpoint marking the pretrained state", len(log))
	}
}

func TestPretrainRejectsBadExamples(t *testing.T) {
	s := memoryStore(t)
	f := extractor(t, defaultRules(t))
	m, _ := classify.NewModel(s, f)
	good := f.Extract(classify.Input{Body: "x y"})
	tests := map[string][]classify.Example{
		"label above 100": {{Vector: good, Label: 101}},
		"negative label":  {{Vector: good, Label: -1}},
		"bad index":       {{Vector: classify.Vector{Indices: []int{f.Dim() + 1}, Values: []float64{1}, Labels: []string{"x"}}, Label: 50}},
	}
	for name, examples := range tests {
		if err := m.Pretrain(examples, 1); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := m.Pretrain(nil, 5); err != nil {
		t.Errorf("pretraining on nothing should be a harmless no-op: %v", err)
	}
}

func TestResetToSeedGoesBackToTheSeedAndRollbackBringsBackWhatWasLearned(t *testing.T) {
	c := newClassifier(t, memoryStore(t))
	vec := c.Features.Extract(heldOut[0].in)
	seeded := c.Model.Predict(vec)
	opposite := 0
	if seeded < 0.5 {
		opposite = 100
	}
	c.Model.Train(0, vec, classify.ExactLabel(opposite))
	learned := c.Model.Predict(vec)
	if learned == seeded {
		t.Fatal("test setup: training changed nothing")
	}
	if err := c.ResetToSeed(); err != nil {
		t.Fatal(err)
	}
	if got := c.Model.Predict(vec); got != seeded {
		t.Errorf("after ResetToSeed %v, want the seeded %v", got, seeded)
	}
	entries, err := c.Model.RollbackEntries()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Model.Predict(vec); got != learned {
		t.Errorf("after rollback %v, want what was learned %v", got, learned)
	}
	_ = entries
}
