package classify

import (
	"fmt"
	"math"
	"time"

	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
)

const (
	maxBoost = 0.4

	// Three messages from one sender within two minutes, counting the new one, look like someone chasing you.
	burstSize   = 3
	burstWindow = 2 * time.Minute
	burstBoost  = 0.1
)

type BoostInput struct {
	Source string
	Sender string
	// Text is everything worth scanning for urgency words: title, subtitle and body.
	Text string
	// At is when the notification was delivered; the burst window is measured back from it.
	At time.Time
}

type BoostResult struct {
	Value   float64
	Reasons []string
}

// Booster computes the time-sensitivity boost (plan.md section 5.4): urgency words from the rules plus same-sender bursts from stored history.
type Booster struct {
	rules rules.Rules
	store *store.Store
}

func NewBooster(r rules.Rules, s *store.Store) *Booster {
	return &Booster{rules: r, store: s}
}

// Boost returns a value between 0 and 0.4. The notification being scored must not be stored yet.
func (b *Booster) Boost(in BoostInput) (BoostResult, error) {
	var result BoostResult
	for _, u := range b.rules.Urgency {
		if u.Regexp.MatchString(in.Text) {
			result.Value += u.Boost
			result.Reasons = append(result.Reasons, "keyword: "+u.Name)
		}
	}
	if in.Sender != "" {
		earlier, err := b.store.ListNotifications(store.Filter{Source: in.Source, Sender: in.Sender, Since: in.At.Add(-burstWindow)})
		if err != nil {
			return BoostResult{}, err
		}
		recent := 0
		for _, n := range earlier {
			if !n.Timestamp.After(in.At) {
				recent++
			}
		}
		if recent+1 >= burstSize {
			result.Value += burstBoost
			result.Reasons = append(result.Reasons, fmt.Sprintf("burst: %d messages from %s within %v", recent+1, in.Sender, burstWindow))
		}
	}
	// Rounded so sums like 0.1+0.2 do not carry float noise into stored scores.
	result.Value = math.Round(min(result.Value, maxBoost)*1000) / 1000
	return result, nil
}
