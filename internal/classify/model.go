package classify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/charlesrobsampson/shh/internal/store"
)

var (
	ErrNothingToUndo = errors.New("no training to undo")
	ErrNoCheckpoint  = errors.New("no checkpoint to roll back to")
)

const (
	defaultLearningRate = 0.1
	defaultL2           = 1e-4
	defaultClip         = 5.0
	defaultMargin       = 0.1
	// maxQuickSteps bounds the SGD steps spent satisfying one quick y/n constraint.
	maxQuickSteps = 200
	// tolerance matches decide: a score within this of the threshold counts as equal to it.
	tolerance = 1e-9

	checkpointLabel = "checkpoint"
	resetLabel      = "reset"
)

// Label is feedback on a notification: an exact 0-100 score, or a quick y/n relative to the threshold it was decided at.
type Label struct {
	Exact     *int
	Feedback  string // "y" (should have alerted) or "n" (rightly blocked)
	Threshold int    // the effective threshold at decision time, 0-99, for quick feedback
}

func ExactLabel(score int) Label { return Label{Exact: &score} }

func QuickLabel(feedback string, threshold int) Label {
	return Label{Feedback: feedback, Threshold: threshold}
}

type TrainResult struct {
	Before  float64
	After   float64
	Updated bool
	Steps   int
}

type ModelOption func(*Model)

func WithLearningRate(rate float64) ModelOption { return func(m *Model) { m.rate = rate } }

// Model is a logistic regression over sparse feature vectors: p = sigmoid(w.x + b), trained by SGD on soft targets (plan.md 5.3).
//
// Weights are saved per feature version after every change. Each training call also writes a training_log row holding the values
// it overwrote, so Undo restores weights exactly; checkpoint rows in the same log mark the points Rollback returns to.
type Model struct {
	mu      sync.Mutex
	store   *store.Store
	version string
	dim     int

	// fresh is true until the model has saved weights, so a new install can be seeded once.
	fresh bool

	bias   float64
	w      []float64
	rate   float64
	l2     float64
	clip   float64
	margin float64
}

// NewModel loads the weights saved for the extractor's feature version. A version with no saved weights starts fresh (and the
// undo history, which belongs to an earlier version, is dropped); saved weights that cannot be read are an error.
func NewModel(s *store.Store, f Features, opts ...ModelOption) (*Model, error) {
	m := &Model{store: s, version: f.Version(), dim: f.Dim(), w: make([]float64, f.Dim()), fresh: true,
		rate: defaultLearningRate, l2: defaultL2, clip: defaultClip, margin: defaultMargin}
	for _, opt := range opts {
		opt(m)
	}
	blob, ok, err := s.LoadWeights(m.version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return m, s.ClearTrainingLog()
	}
	if m.bias, err = decodeWeights(blob, m.w); err != nil {
		return nil, fmt.Errorf("saved weights for %s: %w", m.version, err)
	}
	m.fresh = false
	return m, nil
}

// Fresh reports whether no weights have ever been saved for this feature version.
func (m *Model) Fresh() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fresh
}

func (m *Model) Predict(v Vector) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.predict(v)
}

func (m *Model) predict(v Vector) float64 {
	z := m.bias
	for i, idx := range v.Indices {
		if idx >= 0 && idx < m.dim {
			z += m.w[idx] * v.Values[i]
		}
	}
	return sigmoid(z)
}

// Weights returns the bias and every non-zero weight, for export and inspection.
func (m *Model) Weights() (bias float64, weights map[int]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	weights = map[int]float64{}
	for i, w := range m.w {
		if w != 0 {
			weights[i] = w
		}
	}
	return m.bias, weights
}

// Train applies one label to the model and logs it. Exact labels take a single SGD step toward N/100. A quick label changes
// nothing if its constraint already holds; otherwise it steps toward just past the threshold until the constraint is met.
func (m *Model) Train(notificationID int64, v Vector, l Label) (TrainResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkVector(v); err != nil {
		return TrainResult{}, err
	}
	name, err := validateLabel(l)
	if err != nil {
		return TrainResult{}, err
	}

	result := TrainResult{Before: m.predict(v)}
	before := m.touch(v)
	switch {
	case l.Exact != nil:
		m.step(v, float64(*l.Exact)/100)
		result.Steps = 1
	case l.Feedback == "y":
		limit := float64(l.Threshold) / 100
		// Capped at 1, not 0.99: at lockdown the constraint is S > 0.99, which a target of 0.99 can only approach from below.
		target := math.Min(1, limit+m.margin)
		for result.Before+tolerance <= limit && m.predict(v) <= limit+tolerance && result.Steps < maxQuickSteps {
			m.step(v, target)
			result.Steps++
		}
	default:
		limit := float64(l.Threshold) / 100
		target := math.Max(0, limit-m.margin)
		for m.predict(v) > limit+tolerance && result.Steps < maxQuickSteps {
			m.step(v, target)
			result.Steps++
		}
	}
	result.After = m.predict(v)
	delta, changed := m.diff(before)
	result.Updated = changed

	entry := store.TrainingLogEntry{NotificationID: notificationID, Label: name, Predicted: result.Before, Delta: delta}
	if err := m.store.UpdateModel(m.version, m.encode(), []store.TrainingLogEntry{entry}, nil); err != nil {
		m.restore(before)
		return TrainResult{}, err
	}
	m.fresh = false
	return result, nil
}

// Undo reverses the last n training calls exactly, newest first. Checkpoints are not training and are skipped.
func (m *Model) Undo(n int) error {
	if n < 1 {
		return fmt.Errorf("cannot undo %d steps", n)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	log, err := m.store.TrainingLog(0)
	if err != nil {
		return err
	}
	var undo []store.TrainingLogEntry
	for _, e := range log {
		if e.Label != checkpointLabel && len(undo) < n {
			undo = append(undo, e)
		}
	}
	if len(undo) == 0 {
		return ErrNothingToUndo
	}
	return m.revert(undo, log, undo[len(undo)-1].ID)
}

// Checkpoint marks the current weights as a point Rollback can return to.
func (m *Model) Checkpoint() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store.UpdateModel(m.version, m.encode(), []store.TrainingLogEntry{{Label: checkpointLabel}}, nil)
}

// Rollback returns to the most recent checkpoint that differs from the current weights, using the checkpoint up. A checkpoint
// with no training after it is skipped over to the one before.
func (m *Model) Rollback() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	log, err := m.store.TrainingLog(0)
	if err != nil {
		return err
	}
	var since []store.TrainingLogEntry
	for _, e := range log {
		if e.Label != checkpointLabel {
			since = append(since, e)
			continue
		}
		if len(since) == 0 {
			continue // nothing happened after this checkpoint, so it is the current state
		}
		return m.revert(since, log, e.ID-1)
	}
	return ErrNoCheckpoint
}

// Reset zeroes every weight. A checkpoint is taken first, so Rollback can bring the old weights back.
func (m *Model) Reset() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := make(map[int]float64)
	for i, w := range m.w {
		if w != 0 {
			before[i] = w
		}
	}
	biasBefore := m.bias
	delta := encodeDelta(biasBefore, 0, before, nil)
	m.bias = 0
	clear(m.w)
	entries := []store.TrainingLogEntry{{Label: checkpointLabel}, {Label: resetLabel, Delta: delta}}
	if err := m.store.UpdateModel(m.version, m.encode(), entries, nil); err != nil {
		m.bias = biasBefore
		for i, w := range before {
			m.w[i] = w
		}
		return err
	}
	return nil
}

// revert undoes the given entries (newest first) and removes them from the log, along with any checkpoint newer than the
// oldest log id that is still reachable (floor): those checkpoints describe states that no longer exist.
func (m *Model) revert(entries, log []store.TrainingLogEntry, floor int64) error {
	oldest := entries[len(entries)-1].ID
	bias, w := m.bias, append([]float64(nil), m.w...)
	for _, e := range entries {
		b, restored, err := decodeBefore(e.Delta)
		if err != nil {
			return fmt.Errorf("training log row %d: %w", e.ID, err)
		}
		bias = b
		for i, v := range restored {
			if i >= len(w) {
				return fmt.Errorf("training log row %d refers to feature %d outside the vector space", e.ID, i)
			}
			w[i] = v
		}
	}
	var remove []int64
	for _, e := range entries {
		remove = append(remove, e.ID)
	}
	for _, e := range log {
		if e.Label == checkpointLabel && e.ID > oldest && e.ID > floor {
			remove = append(remove, e.ID)
		}
	}
	oldBias, oldW := m.bias, m.w
	m.bias, m.w = bias, w
	if err := m.store.UpdateModel(m.version, m.encode(), nil, remove); err != nil {
		m.bias, m.w = oldBias, oldW
		return err
	}
	return nil
}

func (m *Model) step(v Vector, target float64) {
	gradient := target - m.predict(v)
	for i, idx := range v.Indices {
		w := m.w[idx] + m.rate*gradient*v.Values[i]
		w *= 1 - m.rate*m.l2
		m.w[idx] = math.Max(-m.clip, math.Min(m.clip, w))
	}
	m.bias = math.Max(-m.clip, math.Min(m.clip, m.bias+m.rate*gradient))
}

func (m *Model) checkVector(v Vector) error {
	if len(v.Indices) != len(v.Values) {
		return errors.New("vector indices and values differ in length")
	}
	for _, idx := range v.Indices {
		if idx < 0 || idx >= m.dim {
			return fmt.Errorf("feature index %d is outside the vector space of %d", idx, m.dim)
		}
	}
	return nil
}

func validateLabel(l Label) (string, error) {
	switch {
	case l.Exact != nil && l.Feedback != "":
		return "", errors.New("a label is either exact or quick feedback, not both")
	case l.Exact != nil:
		if *l.Exact < 0 || *l.Exact > 100 {
			return "", fmt.Errorf("exact label %d is outside 0-100", *l.Exact)
		}
		return fmt.Sprintf("exact:%d", *l.Exact), nil
	case l.Feedback != "y" && l.Feedback != "n":
		return "", fmt.Errorf("feedback %q is neither y nor n", l.Feedback)
	case l.Threshold < 0 || l.Threshold > 99:
		return "", fmt.Errorf("threshold %d is outside 0-99", l.Threshold)
	}
	return fmt.Sprintf("%s@%d", l.Feedback, l.Threshold), nil
}

type snapshot struct {
	bias    float64
	weights map[int]float64
}

// touch remembers the current values of everything v can change.
func (m *Model) touch(v Vector) snapshot {
	s := snapshot{bias: m.bias, weights: make(map[int]float64, len(v.Indices))}
	for _, idx := range v.Indices {
		s.weights[idx] = m.w[idx]
	}
	return s
}

func (m *Model) restore(s snapshot) {
	m.bias = s.bias
	for idx, w := range s.weights {
		m.w[idx] = w
	}
}

// diff encodes what changed since the snapshot, as before and after values.
func (m *Model) diff(before snapshot) (delta []byte, changed bool) {
	after := map[int]float64{}
	old := map[int]float64{}
	for idx, was := range before.weights {
		if now := m.w[idx]; now != was {
			old[idx], after[idx] = was, now
		}
	}
	changed = len(old) > 0 || m.bias != before.bias
	return encodeDelta(before.bias, m.bias, old, after), changed
}

func sigmoid(z float64) float64 {
	return 1 / (1 + math.Exp(-z))
}

// Weights blob: dimension (uint32), bias (float64), count (uint32), then count x (index uint32, weight float64).
func (m *Model) encode() []byte {
	var indices []int
	for i, w := range m.w {
		if w != 0 {
			indices = append(indices, i)
		}
	}
	buf := make([]byte, 0, 16+12*len(indices))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(m.dim))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(m.bias))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(indices)))
	for _, i := range indices {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(i))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(m.w[i]))
	}
	return buf
}

func decodeWeights(blob []byte, into []float64) (bias float64, err error) {
	if len(blob) < 16 {
		return 0, errors.New("too short")
	}
	if dim := int(binary.LittleEndian.Uint32(blob)); dim != len(into) {
		return 0, fmt.Errorf("dimension %d, want %d", dim, len(into))
	}
	bias = math.Float64frombits(binary.LittleEndian.Uint64(blob[4:]))
	count := int(binary.LittleEndian.Uint32(blob[12:]))
	if len(blob) != 16+12*count {
		return 0, errors.New("length does not match its entry count")
	}
	for i := 0; i < count; i++ {
		entry := blob[16+12*i:]
		idx := int(binary.LittleEndian.Uint32(entry))
		if idx >= len(into) {
			return 0, fmt.Errorf("feature index %d out of range", idx)
		}
		into[idx] = math.Float64frombits(binary.LittleEndian.Uint64(entry[4:]))
	}
	return bias, nil
}

// Delta blob: bias before (float64), bias after (float64), count (uint32), then count x (index uint32, before, after float64).
func encodeDelta(biasBefore, biasAfter float64, before, after map[int]float64) []byte {
	indices := make([]int, 0, len(before))
	for i := range before {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	buf := make([]byte, 0, 20+20*len(indices))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(biasBefore))
	buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(biasAfter))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(indices)))
	for _, i := range indices {
		buf = binary.LittleEndian.AppendUint32(buf, uint32(i))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(before[i]))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(after[i]))
	}
	return buf
}

// decodeBefore reads the values a training call overwrote.
func decodeBefore(delta []byte) (bias float64, weights map[int]float64, err error) {
	if len(delta) < 20 {
		return 0, nil, errors.New("delta too short")
	}
	bias = math.Float64frombits(binary.LittleEndian.Uint64(delta))
	count := int(binary.LittleEndian.Uint32(delta[16:]))
	if len(delta) != 20+20*count {
		return 0, nil, errors.New("delta length does not match its entry count")
	}
	weights = make(map[int]float64, count)
	for i := 0; i < count; i++ {
		entry := delta[20+20*i:]
		weights[int(binary.LittleEndian.Uint32(entry))] = math.Float64frombits(binary.LittleEndian.Uint64(entry[4:]))
	}
	return bias, weights, nil
}
