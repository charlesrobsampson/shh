package classify

import (
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
)

// Classifier scores notifications: features, the trained model and the time-sensitivity boost, wired together.
type Classifier struct {
	Features *L0L1
	Model    *Model
	Booster  *Booster
}

// Score is the model's view of one notification. The decision compares Base plus Boost with the threshold (see decide).
type Score struct {
	Base         float64
	Boost        float64
	BoostReasons []string
	Vector       Vector
}

// NewClassifier builds the classifier over the store. A new install, with no saved weights for this feature version, is trained
// from the built-in seed set once. embedder may be nil, which leaves layer 2 off.
func NewClassifier(s *store.Store, r rules.Rules, embedder Embedder) (*Classifier, error) {
	var opts []Option
	if embedder != nil {
		opts = append(opts, WithEmbedder(embedder))
	}
	features, err := NewL0L1(r, opts...)
	if err != nil {
		return nil, err
	}
	model, err := NewModel(s, features)
	if err != nil {
		return nil, err
	}
	if model.Fresh() {
		seed, err := SeedExamples()
		if err != nil {
			return nil, err
		}
		examples := make([]Example, len(seed))
		for i, ex := range seed {
			examples[i] = Example{Vector: features.Extract(ex.Input), Label: ex.Label}
		}
		if err := model.Pretrain(examples, seedEpochs); err != nil {
			return nil, err
		}
	}
	return &Classifier{Features: features, Model: model, Booster: NewBooster(r, s)}, nil
}

// Score classifies a notification delivered at the given time. Call it before the notification is stored, so a burst is not
// counted against itself.
func (c *Classifier) Score(in Input, at time.Time) (Score, error) {
	v := c.Features.Extract(in)
	boost, err := c.Booster.Boost(BoostInput{
		Source: in.Source, Sender: in.Sender, At: at,
		Text: strings.Join([]string{in.Title, in.Subtitle, in.Body}, " "),
	})
	if err != nil {
		return Score{}, err
	}
	return Score{Base: c.Model.Predict(v), Boost: boost.Value, BoostReasons: boost.Reasons, Vector: v}, nil
}
