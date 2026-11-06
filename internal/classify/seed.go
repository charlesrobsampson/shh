package classify

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand/v2"

	"github.com/charlesrobsampson/shh/internal/store"
)

// seedEpochs is how many passes the day-one training makes over the seed set.
const seedEpochs = 60

// The seed set is synthetic: invented notifications with labels on the same 0-100 scale as user labels. It is never real data.
//
//go:embed seed/seed.jsonl
var seedJSONL []byte

type SeedExample struct {
	Input Input
	Label int
}

// SeedExamples returns the built-in training examples.
func SeedExamples() ([]SeedExample, error) {
	var out []SeedExample
	scanner := bufio.NewScanner(bytes.NewReader(seedJSONL))
	for line := 1; scanner.Scan(); line++ {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var row struct {
			Source   string `json:"source"`
			Sender   string `json:"sender"`
			Title    string `json:"title"`
			Subtitle string `json:"subtitle"`
			Body     string `json:"body"`
			Category string `json:"category"`
			Label    int    `json:"label"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("seed line %d: %w", line, err)
		}
		out = append(out, SeedExample{
			Input: Input{Source: row.Source, Sender: row.Sender, Title: row.Title, Subtitle: row.Subtitle, Body: row.Body, Category: row.Category},
			Label: row.Label,
		})
	}
	return out, scanner.Err()
}

// Example is a feature vector with an exact 0-100 label.
type Example struct {
	Vector Vector
	Label  int
}

// Pretrain fits the model to examples over several passes in a fixed order, saves the result once and marks it with a checkpoint.
// Unlike Train it writes no per-example undo rows, so a seed cannot be undone one example at a time.
func (m *Model) Pretrain(examples []Example, epochs int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range examples {
		if ex.Label < 0 || ex.Label > 100 {
			return fmt.Errorf("label %d is outside 0-100", ex.Label)
		}
		if err := m.checkVector(ex.Vector); err != nil {
			return err
		}
	}
	if len(examples) == 0 {
		return nil
	}
	oldBias, oldW := m.bias, append([]float64(nil), m.w...)
	order := rand.New(rand.NewPCG(1, 2))
	shuffled := append([]Example(nil), examples...)
	for epoch := 0; epoch < epochs; epoch++ {
		order.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, ex := range shuffled {
			m.step(ex.Vector, float64(ex.Label)/100)
		}
	}
	if err := m.store.UpdateModel(m.version, m.encode(), []store.TrainingLogEntry{{Label: checkpointLabel}}, nil); err != nil {
		m.bias, m.w = oldBias, oldW
		return err
	}
	m.fresh = false
	return nil
}
