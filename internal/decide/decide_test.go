package decide_test

import (
	"math"
	"testing"

	"github.com/charlesrobsampson/shh/internal/decide"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name          string
		threshold     int
		base, boost   float64
		preempt       bool
		wantInterrupt bool
		wantEffective float64
	}{
		{"equal to the threshold is blocked", 65, 0.65, 0, false, false, 0.65},
		{"one point below is blocked", 65, 0.64, 0, false, false, 0.64},
		{"one point above passes", 65, 0.66, 0, false, true, 0.66},
		{"boost lifts a score over the threshold", 65, 0.60, 0.10, false, true, 0.70},
		{"boost that only reaches the threshold is still blocked", 65, 0.55, 0.10, false, false, 0.65},
		{"boost alone cannot pierce lockdown", 99, 0.95, 0.40, false, false, 0.99},
		{"boost is capped at 0.99", 90, 0.80, 0.40, false, true, 0.99},
		{"a base score of 1.0 pierces lockdown", 99, 1.0, 0, false, true, 1.0},
		{"a base score above 0.99 pierces lockdown even with a boost", 99, 0.995, 0.4, false, true, 0.995},
		{"a base score of exactly 0.99 does not pierce lockdown", 99, 0.99, 0, false, false, 0.99},
		{"zero score is blocked at threshold 0", 0, 0, 0, false, false, 0},
		{"zero score is blocked at a middle threshold", 35, 0, 0, false, false, 0},
		{"zero score is blocked at lockdown", 99, 0, 0, false, false, 0},
		{"any positive score passes threshold 0", 0, 0.01, 0, false, true, 0.01},
		{"calendar pre-empt passes at lockdown", 99, 0, 0, true, true, 0},
		{"calendar pre-empt passes whatever the score", 90, 0.2, 0.1, true, true, 0.3},
		{"float noise: 0.07 is 7 points, equal to threshold 7", 7, 0.07, 0, false, false, 0.07},
		{"float noise: 0.57 is 57 points, equal to threshold 57", 57, 0.57, 0, false, false, 0.57},
		{"float noise: 0.29 is 29 points, equal to threshold 29", 29, 0.29, 0, false, false, 0.29},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decide.Decide(tt.threshold, tt.base, tt.boost, tt.preempt)
			if got.Interrupt != tt.wantInterrupt {
				t.Errorf("Interrupt = %v, want %v (effective %v)", got.Interrupt, tt.wantInterrupt, got.Effective)
			}
			if math.Abs(got.Effective-tt.wantEffective) > 1e-9 {
				t.Errorf("Effective = %v, want %v", got.Effective, tt.wantEffective)
			}
		})
	}
}

// Every whole-number score against every threshold: a score of N points interrupts exactly when N is above the threshold.
func TestDecideOnEveryWholePoint(t *testing.T) {
	for threshold := 0; threshold <= decide.MaxLevel; threshold++ {
		for points := 0; points <= 100; points++ {
			base := float64(points) / 100
			want := points > threshold
			if got := decide.Decide(threshold, base, 0, false).Interrupt; got != want {
				t.Fatalf("score %d at threshold %d: Interrupt = %v, want %v", points, threshold, got, want)
			}
		}
	}
}

func TestBoostNeverLowersAScore(t *testing.T) {
	for _, base := range []float64{0, 0.3, 0.9, 0.99, 1.0} {
		for _, boost := range []float64{0, 0.1, 0.4} {
			if got := decide.Decide(50, base, boost, false).Effective; got < base {
				t.Errorf("base %v boost %v: effective %v is below the base score", base, boost, got)
			}
		}
	}
}
