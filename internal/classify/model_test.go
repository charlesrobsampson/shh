package classify_test

import (
	"errors"
	"math"
	"path/filepath"
	"testing"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	outage = classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana",
		Body: "production server is down, customers cannot log in, please help now"}
	outageSimilar = classify.Input{Source: "Teams", Sender: "Ana", Title: "Ana",
		Body: "production server is down again, customers cannot log in"}
	newsletter = classify.Input{Source: "Mail", Sender: "Weekly News", Title: "Weekly digest",
		Body: "ten tips to boost your productivity this week, unsubscribe any time"}
)

type modelRig struct {
	model    *classify.Model
	features *classify.L0L1
	store    *store.Store
}

func newModelRig(t *testing.T, opts ...classify.ModelOption) modelRig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := extractor(t, defaultRules(t))
	m, err := classify.NewModel(s, f, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return modelRig{m, f, s}
}

func (r modelRig) vec(in classify.Input) classify.Vector { return r.features.Extract(in) }

func (r modelRig) predict(in classify.Input) float64 { return r.model.Predict(r.vec(in)) }

func (r modelRig) train(t *testing.T, in classify.Input, l classify.Label) classify.TrainResult {
	t.Helper()
	res, err := r.model.Train(0, r.vec(in), l)
	if err != nil {
		t.Fatalf("Train: %v", err)
	}
	return res
}

// steerTo trains exact labels until the input scores close to target.
func (r modelRig) steerTo(t *testing.T, in classify.Input, target float64) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if math.Abs(r.predict(in)-target) < 0.01 {
			return
		}
		r.train(t, in, classify.ExactLabel(int(math.Round(target*100))))
	}
	t.Fatalf("could not steer the score to %v (now %v)", target, r.predict(in))
}

// probes are scored before and after to compare model states exactly.
func (r modelRig) fingerprint() []float64 {
	var out []float64
	for _, in := range []classify.Input{outage, outageSimilar, newsletter, {Body: "something unrelated entirely"}} {
		out = append(out, r.predict(in))
	}
	return out
}

func sameFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAFreshModelIsUndecided(t *testing.T) {
	r := newModelRig(t)
	for _, in := range []classify.Input{outage, newsletter, {}} {
		if got := r.predict(in); got != 0.5 {
			t.Errorf("fresh score = %v, want 0.5", got)
		}
	}
}

func TestExactLabelsMoveSimilarItemsTowardTheLabel(t *testing.T) {
	tests := []struct {
		name  string
		label int
	}{{"label 80", 80}, {"label 10", 10}, {"label 0", 0}, {"label 100", 100}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newModelRig(t)
			before := r.predict(outageSimilar)
			res := r.train(t, outage, classify.ExactLabel(tt.label))
			after := r.predict(outageSimilar)
			target := float64(tt.label) / 100
			if math.Abs(after-target) >= math.Abs(before-target) {
				t.Errorf("a similar item went from %v to %v, want it closer to %v", before, after, target)
			}
			if !res.Updated || res.Before != before && res.Before != 0.5 {
				t.Errorf("result = %+v", res)
			}
			if r.predict(newsletter) == after {
				t.Error("an unrelated item moved as much as the similar one")
			}
		})
	}
}

func TestQuickYesRaisesAScoreBelowTheThreshold(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.40)
	res := r.train(t, outage, classify.QuickLabel("y", 65))
	if got := r.predict(outage); got <= 0.65 {
		t.Fatalf("score after 'y' at 65 = %v, want above 0.65", got)
	}
	if !res.Updated || res.Steps == 0 {
		t.Errorf("result = %+v, want an update", res)
	}
}

func TestQuickYesDoesNothingWhenAlreadyAboveTheThreshold(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.80)
	before := r.fingerprint()
	res := r.train(t, outage, classify.QuickLabel("y", 65))
	if res.Updated || res.Steps != 0 || !sameFloats(before, r.fingerprint()) {
		t.Fatalf("a satisfied constraint changed the model: %+v", res)
	}
}

func TestQuickNoLowersAnAlertedItemToTheThresholdOrBelow(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, newsletter, 0.80)
	res := r.train(t, newsletter, classify.QuickLabel("n", 65))
	if got := r.predict(newsletter); got > 0.65 {
		t.Fatalf("score after 'n' at 65 = %v, want at most 0.65", got)
	}
	if !res.Updated {
		t.Errorf("result = %+v, want an update", res)
	}
}

func TestQuickNoDoesNothingWhenAlreadyBelowTheThreshold(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, newsletter, 0.30)
	before := r.fingerprint()
	res := r.train(t, newsletter, classify.QuickLabel("n", 65))
	if res.Updated || !sameFloats(before, r.fingerprint()) {
		t.Fatalf("a satisfied constraint changed the model: %+v", res)
	}
}

func TestQuickLabelsAtTheBoundaries(t *testing.T) {
	t.Run("y at lockdown", func(t *testing.T) {
		r := newModelRig(t)
		r.train(t, outage, classify.QuickLabel("y", 99))
		if got := r.predict(outage); got <= 0.99 {
			t.Errorf("score = %v, want above 0.99", got)
		}
	})
	t.Run("n at threshold 0", func(t *testing.T) {
		r := newModelRig(t)
		r.train(t, newsletter, classify.QuickLabel("n", 0))
		if got := r.predict(newsletter); got > 0.05 {
			t.Errorf("score = %v, want it driven to nearly 0 (exactly 0 is unreachable)", got)
		}
	})
}

func TestInvalidLabelsAreRejectedWithoutChangingAnything(t *testing.T) {
	ptr := func(n int) *int { return &n }
	tests := []struct {
		name  string
		label classify.Label
	}{
		{"exact above 100", classify.Label{Exact: ptr(101)}},
		{"exact below 0", classify.Label{Exact: ptr(-1)}},
		{"unknown feedback", classify.Label{Feedback: "maybe", Threshold: 65}},
		{"threshold above 99", classify.Label{Feedback: "y", Threshold: 100}},
		{"negative threshold", classify.Label{Feedback: "n", Threshold: -1}},
		{"empty label", classify.Label{}},
		{"both kinds", classify.Label{Exact: ptr(50), Feedback: "y", Threshold: 65}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newModelRig(t)
			before := r.fingerprint()
			if _, err := r.model.Train(0, r.vec(outage), tt.label); err == nil {
				t.Fatal("Train accepted the label")
			}
			if !sameFloats(before, r.fingerprint()) {
				t.Error("a rejected label changed the model")
			}
			if log, _ := r.store.TrainingLog(0); len(log) != 0 {
				t.Errorf("a rejected label was logged: %+v", log)
			}
		})
	}
	t.Run("a vector from a different-sized space", func(t *testing.T) {
		r := newModelRig(t)
		bad := classify.Vector{Indices: []int{r.features.Dim() + 5}, Values: []float64{1}, Labels: []string{"x"}}
		if _, err := r.model.Train(0, bad, classify.ExactLabel(50)); err == nil {
			t.Fatal("Train accepted an index outside the vector space")
		}
	})
}

func TestScoresStayInsideZeroAndOne(t *testing.T) {
	r := newModelRig(t)
	for i := 0; i < 300; i++ {
		r.train(t, outage, classify.ExactLabel(100))
	}
	for _, in := range []classify.Input{outage, outageSimilar, newsletter} {
		if p := r.predict(in); p < 0 || p > 1 || math.IsNaN(p) {
			t.Errorf("score %v is outside [0, 1]", p)
		}
	}
}

func TestClippingBoundsTheWeights(t *testing.T) {
	// A huge learning rate makes every step overshoot, so only clipping keeps the weights bounded.
	r := newModelRig(t, classify.WithLearningRate(100))
	for i := 0; i < 20; i++ {
		r.train(t, outage, classify.ExactLabel(100))
		r.train(t, newsletter, classify.ExactLabel(0))
	}
	bias, weights := r.model.Weights()
	if math.Abs(bias) > 5 {
		t.Errorf("bias %v exceeds the clip", bias)
	}
	var biggest float64
	for _, w := range weights {
		biggest = math.Max(biggest, math.Abs(w))
	}
	if biggest > 5 {
		t.Errorf("largest weight %v exceeds the clip of 5", biggest)
	}
	if biggest < 3 {
		t.Errorf("largest weight %v: the overshooting steps should have been clipped near 5, not damped", biggest)
	}
}

func TestEveryTrainingCallIsLogged(t *testing.T) {
	r := newModelRig(t)
	s := r.store
	id := insertForModel(t, s)
	if _, err := r.model.Train(id, r.vec(outage), classify.ExactLabel(80)); err != nil {
		t.Fatal(err)
	}
	r.steerTo(t, newsletter, 0.8)
	r.train(t, newsletter, classify.QuickLabel("y", 65)) // already above: a no-op that is still logged

	log, _ := s.TrainingLog(0)
	last := log[0]
	if last.Label != "y@65" || len(log) < 3 {
		t.Fatalf("log = %+v", log)
	}
	first := log[len(log)-1]
	if first.Label != "exact:80" || first.NotificationID != id || first.Predicted != 0.5 || len(first.Delta) == 0 {
		t.Errorf("first entry = %+v, want the label, notification, score before and the weight delta", first)
	}
}

func insertForModel(t *testing.T, s *store.Store) int64 {
	t.Helper()
	threshold := 65
	id, _, err := s.InsertNotification(store.Notification{
		Source: "Teams", Sender: "Ana", Body: "x", Timestamp: t0, Status: store.StatusBlocked, Score: 0.4, ThresholdAtDecision: &threshold,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUndoRestoresTheExactWeights(t *testing.T) {
	r := newModelRig(t)
	r.train(t, outage, classify.ExactLabel(90))
	afterOne := r.fingerprint()
	r.train(t, newsletter, classify.ExactLabel(5))
	r.train(t, outageSimilar, classify.ExactLabel(70))
	afterThree := r.fingerprint()
	if sameFloats(afterOne, afterThree) {
		t.Fatal("test setup: training changed nothing")
	}

	if err := r.model.Undo(1); err != nil {
		t.Fatal(err)
	}
	if sameFloats(afterThree, r.fingerprint()) {
		t.Error("Undo(1) changed nothing")
	}
	if err := r.model.Undo(2); err != nil {
		t.Fatal(err)
	}
	fresh := newModelRig(t)
	if !sameFloats(fresh.fingerprint(), r.fingerprint()) {
		t.Errorf("after undoing everything the scores are %v, want a fresh model's %v exactly", r.fingerprint(), fresh.fingerprint())
	}
	if log, _ := r.store.TrainingLog(0); len(log) != 0 {
		t.Errorf("undone updates are still in the log: %+v", log)
	}
}

func TestUndoGoesBackThroughNoOpLabelsOneAtATime(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.8)
	stateWithUpdate := r.fingerprint()
	r.train(t, outage, classify.QuickLabel("y", 65)) // satisfied: logged, no weight change

	if err := r.model.Undo(1); err != nil {
		t.Fatal(err)
	}
	if !sameFloats(stateWithUpdate, r.fingerprint()) {
		t.Fatal("undoing a label that changed nothing must not revert the update before it")
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	r := newModelRig(t)
	if err := r.model.Undo(1); !errors.Is(err, classify.ErrNothingToUndo) {
		t.Fatalf("err = %v, want ErrNothingToUndo", err)
	}
	if err := r.model.Undo(0); err == nil {
		t.Error("Undo(0) accepted")
	}
}

func TestRollbackReturnsToTheLastCheckpoint(t *testing.T) {
	r := newModelRig(t)
	r.train(t, outage, classify.ExactLabel(90))
	atCheckpoint := r.fingerprint()
	if err := r.model.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.train(t, newsletter, classify.ExactLabel(0))
		r.train(t, outageSimilar, classify.ExactLabel(30))
	}

	if err := r.model.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !sameFloats(atCheckpoint, r.fingerprint()) {
		t.Fatal("Rollback did not return to the checkpoint exactly")
	}
	if err := r.model.Rollback(); !errors.Is(err, classify.ErrNoCheckpoint) {
		t.Fatalf("second Rollback err = %v, want ErrNoCheckpoint (the checkpoint was used up)", err)
	}
}

func TestRollbackWalksBackThroughCheckpoints(t *testing.T) {
	r := newModelRig(t)
	state0 := r.fingerprint()
	r.model.Checkpoint() // A
	r.train(t, outage, classify.ExactLabel(90))
	r.model.Checkpoint() // B, with nothing trained since
	if err := r.model.Rollback(); err != nil {
		t.Fatal(err)
	}
	// With nothing trained since B, the first rollback goes back to A, undoing the training in between.
	if !sameFloats(state0, r.fingerprint()) {
		t.Fatalf("rollback with no training since the newest checkpoint should reach the previous one: %v", r.fingerprint())
	}
}

func TestResetStartsOverAndCanBeRolledBack(t *testing.T) {
	r := newModelRig(t)
	r.train(t, outage, classify.ExactLabel(90))
	r.train(t, newsletter, classify.ExactLabel(5))
	trained := r.fingerprint()

	if err := r.model.Reset(); err != nil {
		t.Fatal(err)
	}
	if !sameFloats(newModelRig(t).fingerprint(), r.fingerprint()) {
		t.Fatalf("after Reset scores are %v, want a fresh model", r.fingerprint())
	}
	bias, weights := r.model.Weights()
	if bias != 0 || len(weights) != 0 {
		t.Errorf("Reset left weights behind: bias %v, %d weights", bias, len(weights))
	}

	if err := r.model.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !sameFloats(trained, r.fingerprint()) {
		t.Fatal("rolling back a reset did not restore the trained weights exactly")
	}
}

func TestWeightsPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	f := extractor(t, defaultRules(t))
	m1, _ := classify.NewModel(first, f)
	m1.Train(0, f.Extract(outage), classify.ExactLabel(90))
	m1.Train(0, f.Extract(newsletter), classify.ExactLabel(5))
	want := []float64{m1.Predict(f.Extract(outage)), m1.Predict(f.Extract(newsletter)), m1.Predict(f.Extract(outageSimilar))}
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	m2, err := classify.NewModel(second, f)
	if err != nil {
		t.Fatal(err)
	}
	got := []float64{m2.Predict(f.Extract(outage)), m2.Predict(f.Extract(newsletter)), m2.Predict(f.Extract(outageSimilar))}
	if !sameFloats(want, got) {
		t.Fatalf("scores after restart = %v, want exactly %v", got, want)
	}
	if err := m2.Undo(1); err != nil {
		t.Errorf("undo history did not survive the restart: %v", err)
	}
}

func TestAChangedFeatureVersionStartsFresh(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	r1 := defaultRules(t)
	f1 := extractor(t, r1)
	m1, _ := classify.NewModel(s, f1)
	m1.Train(0, f1.Extract(outage), classify.ExactLabel(95))
	trained := m1.Predict(f1.Extract(outage))

	r2 := defaultRules(t)
	r2.VIPSenders = append(r2.VIPSenders, "someone new")
	f2 := extractor(t, r2)
	if f1.Version() == f2.Version() {
		t.Fatal("test setup: the versions are equal")
	}
	m2, err := classify.NewModel(s, f2)
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.Predict(f2.Extract(outage)); got != 0.5 {
		t.Errorf("model for a new feature version scores %v, want a fresh 0.5 and not weights trained on other features", got)
	}
	if err := m2.Undo(1); !errors.Is(err, classify.ErrNothingToUndo) {
		t.Errorf("undo history from the old version leaked into the new one: %v", err)
	}

	m3, _ := classify.NewModel(s, f1)
	if got := m3.Predict(f1.Extract(outage)); got != trained {
		t.Errorf("going back to the old feature version scores %v, want its weights %v kept", got, trained)
	}
}

func TestACorruptWeightsBlobIsAnErrorNotAFreshModel(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	f := extractor(t, defaultRules(t))
	s.UpdateModel(f.Version(), []byte("garbage"), nil, nil)
	if _, err := classify.NewModel(s, f); err == nil {
		t.Fatal("NewModel loaded garbage silently; that would throw away a user's training")
	}
}

func TestModelOptionsChangeLearning(t *testing.T) {
	slow := newModelRig(t, classify.WithLearningRate(0.01))
	fast := newModelRig(t, classify.WithLearningRate(0.5))
	slow.train(t, outage, classify.ExactLabel(100))
	fast.train(t, outage, classify.ExactLabel(100))
	if slowP, fastP := slow.predict(outage), fast.predict(outage); fastP <= slowP {
		t.Errorf("a higher learning rate moved the score %v, a lower one %v; want the higher to move more", fastP, slowP)
	}
}

func TestQuickYesOnARightCallNearTheThresholdPushesItOutToTheMargin(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.68) // alerted, but only just: the model was right, and a y confirms it
	res := r.train(t, outage, classify.QuickLabel("y", 65))
	got := r.predict(outage)
	if !res.Updated || res.Steps == 0 {
		t.Fatalf("result = %+v, want the right call near the line to be reinforced", res)
	}
	if got < 0.75-1e-6 {
		t.Errorf("score after y = %v, want it moved out to the 10 point margin (0.75)", got)
	}
	if got > 0.90 {
		t.Errorf("score after y = %v: reinforcing should stop at the margin, not run away", got)
	}
}

func TestQuickNoOnARightCallNearTheThresholdPushesItDownToTheMargin(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, newsletter, 0.62) // blocked, but only just
	res := r.train(t, newsletter, classify.QuickLabel("n", 65))
	got := r.predict(newsletter)
	if !res.Updated {
		t.Fatalf("result = %+v, want a reinforcement", res)
	}
	if got > 0.55+1e-6 || got < 0.40 {
		t.Errorf("score after n = %v, want it moved down to the margin (0.55) and no further", got)
	}
}

func TestAScoreClearOfTheMarginIsLeftAloneByARightCall(t *testing.T) {
	for _, tt := range []struct {
		name  string
		score float64
		label classify.Label
	}{
		{"y well above", 0.80, classify.QuickLabel("y", 65)},
		{"n well below", 0.40, classify.QuickLabel("n", 65)},
		{"y exactly at the margin", 0.75, classify.QuickLabel("y", 65)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newModelRig(t)
			r.steerTo(t, outage, tt.score)
			before := r.fingerprint()
			if res := r.train(t, outage, tt.label); res.Updated && !sameFloats(before, r.fingerprint()) && tt.score != 0.75 {
				t.Errorf("a right call clear of the margin changed the model: %+v", res)
			}
		})
	}
}

func TestReinforcingNeverCrossesTheThresholdTheWrongWay(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, newsletter, 0.64)
	r.train(t, newsletter, classify.QuickLabel("n", 65))
	if got := r.predict(newsletter); got > 0.65 {
		t.Errorf("an n at 65 left the score at %v, above the threshold", got)
	}
	r2 := newModelRig(t)
	r2.steerTo(t, outage, 0.66)
	r2.train(t, outage, classify.QuickLabel("y", 65))
	if got := r2.predict(outage); got <= 0.65 {
		t.Errorf("a y at 65 left the score at %v, at or below the threshold", got)
	}
}

func TestReinforcingAtLockdownStopsShortOfCertainty(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.9945)
	if r.predict(outage) <= 0.99 {
		t.Skip("could not steer the score above lockdown")
	}
	before := r.predict(outage)
	res := r.train(t, outage, classify.QuickLabel("y", 99))
	got := r.predict(outage)
	if got < before-1e-9 || got > 0.9999 || got < 0.995-1e-6 {
		t.Errorf("score %v -> %v (%+v): a right call at lockdown should nudge towards 0.995 without chasing 1", before, got, res)
	}
	if res.Steps >= 200 {
		t.Errorf("%d steps: the reinforcement ran to the step limit", res.Steps)
	}
}

func TestAReinforcementIsUndoneExactly(t *testing.T) {
	r := newModelRig(t)
	r.steerTo(t, outage, 0.68)
	before := r.fingerprint()
	r.train(t, outage, classify.QuickLabel("y", 65))
	if sameFloats(before, r.fingerprint()) {
		t.Fatal("setup: the reinforcement changed nothing")
	}
	if err := r.model.Undo(1); err != nil {
		t.Fatal(err)
	}
	if !sameFloats(before, r.fingerprint()) {
		t.Error("undo did not restore the weights from before the reinforcement")
	}
}
