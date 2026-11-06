package classify

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maxFactors = 3
	// minFactor is the smallest contribution, in logit units, worth mentioning.
	minFactor   = 0.05
	maxLabel    = 32
	maxReason   = 140
	ellipsis    = "…"
	noSignals   = "no strong signals"
	important   = "important: "
	unimportant = "unimportant: "
)

// Explain writes the short reason shown next to a score: up to three features that pushed it the way it went, then the boost
// and what caused it. Ties go alphabetically so the same inputs always read the same.
func Explain(base float64, factors []Contribution, boost BoostResult) string {
	high := base >= 0.5
	var pushing []Contribution
	specificUrgency := false
	for _, f := range factors {
		specificUrgency = specificUrgency || strings.HasPrefix(f.Label, "urgency: ")
	}
	for _, f := range factors {
		if math.Abs(f.Value) >= minFactor && (f.Value > 0) == high && worthMentioning(f.Label, specificUrgency) {
			pushing = append(pushing, f)
		}
	}
	sort.Slice(pushing, func(i, j int) bool {
		if a, b := math.Abs(pushing[i].Value), math.Abs(pushing[j].Value); a != b {
			return a > b
		}
		return pushing[i].Label < pushing[j].Label
	})

	reason := noSignals
	if len(pushing) > 0 {
		names := make([]string, 0, maxFactors)
		for _, f := range pushing[:min(len(pushing), maxFactors)] {
			names = append(names, shorten(f.Label, maxLabel))
		}
		prefix := unimportant
		if high {
			prefix = important
		}
		reason = prefix + strings.Join(names, ", ")
	}
	if boost.Value > 0 {
		reason += fmt.Sprintf("; boost +%d", int(math.Round(boost.Value*100)))
		if len(boost.Reasons) > 0 {
			reason += " (" + strings.Join(boost.Reasons, ", ") + ")"
		}
	}
	return shorten(reason, maxReason)
}

func shorten(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit-1]) + ellipsis
}

// functionWords are too common to explain anything, even when a model has learned a weight for them.
var functionWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "for": true, "from": true,
	"has": true, "have": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "that": true, "the": true,
	"this": true, "to": true, "we": true, "with": true, "you": true, "your": true,
}

func worthMentioning(label string, specificUrgency bool) bool {
	if word, ok := strings.CutPrefix(label, "word: "); ok && functionWords[word] {
		return false
	}
	return !(label == "urgency matched" && specificUrgency)
}
