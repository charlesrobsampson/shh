package decide

import "math"

// boostCeiling is the highest a boosted score can reach, so a boost alone never pierces lockdown (threshold 99).
const boostCeiling = 0.99

// epsilon absorbs float noise such as 0.07*100 = 7.000000000000001, so a score equal to the threshold is blocked.
const epsilon = 1e-9

type Decision struct {
	Interrupt bool
	// Effective is the score compared with the threshold, on the stored 0-1 scale.
	Effective float64
}

// Decide applies plan.md section 4.3. threshold is the focus level (0-99), base the model score and boost the time-sensitivity
// boost, both 0-1. The notification interrupts when a calendar reminder pre-empts, or when its effective score is strictly above
// the threshold. A base score above 0.99 (a label of 100) pierces even lockdown; a boost cannot.
func Decide(threshold int, base, boost float64, calendarPreempt bool) Decision {
	effective := math.Max(base, math.Min(boostCeiling, base+boost))
	return Decision{
		Interrupt: calendarPreempt || effective*100 > float64(threshold)+epsilon,
		Effective: effective,
	}
}
