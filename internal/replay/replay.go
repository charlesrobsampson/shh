// Package replay runs the classifier over recorded notifications and prints what it would have decided.
package replay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/classify"
	"github.com/charlesrobsampson/shh/internal/decide"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
)

const (
	maxTextWidth   = 60
	untimedSpacing = 5 * time.Minute
)

type Options struct {
	// Threshold is the focus level to decide at, 0-99.
	Threshold int
	Rules     rules.Rules
	// Embedder enables layer 2; nil leaves it off.
	Embedder classify.Embedder
}

type line struct {
	Source   string `json:"source"`
	Sender   string `json:"sender"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Body     string `json:"body"`
	Category string `json:"category"`
	// Thread is optional: direct, group, meeting or channel, as the app's metadata would say.
	Thread string `json:"thread"`
	At     string `json:"at"`
	Label  *int   `json:"label"`
}

// Run reads JSON lines (source, sender, title, subtitle, body, category, and optionally at as RFC 3339 and label as 0-100) and
// writes one decision per line, then a summary. The classifier is seeded fresh in memory, so nothing on disk is read or changed.
// Lines without a time are spaced five minutes apart, well outside the burst window; give them times to exercise burst detection.
func Run(in io.Reader, out io.Writer, opt Options) error {
	if opt.Threshold < 0 || opt.Threshold > decide.MaxLevel {
		return fmt.Errorf("threshold %d is outside 0-%d", opt.Threshold, decide.MaxLevel)
	}
	s, err := store.Open(":memory:")
	if err != nil {
		return err
	}
	defer s.Close()
	classifier, err := classify.NewClassifier(s, opt.Rules, opt.Embedder)
	if err != nil {
		return err
	}

	origin := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var total, alerted int
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		row, err := parse(text, lineNumber)
		if err != nil {
			return err
		}
		at := origin.Add(time.Duration(total) * untimedSpacing)
		if row.At != "" {
			if at, err = time.Parse(time.RFC3339, row.At); err != nil {
				return fmt.Errorf("line %d: bad time %q: %w", lineNumber, row.At, err)
			}
		}
		input := classify.Input{Source: row.Source, Sender: row.Sender, Title: row.Title, Subtitle: row.Subtitle, Body: row.Body, Category: row.Category, Thread: row.Thread}
		score, err := classifier.Score(input, at)
		if err != nil {
			return err
		}
		decision := decide.Decide(opt.Threshold, score.Base, score.Boost, false)
		status := store.StatusBlocked
		if decision.Interrupt {
			status = store.StatusAlerted
			alerted++
		}
		threshold := opt.Threshold
		if _, _, err := s.InsertNotification(store.Notification{
			Source: row.Source, Sender: row.Sender, Title: row.Title, Subtitle: row.Subtitle, Category: row.Category, Body: row.Body, Timestamp: at,
			Status: status, Score: score.Base, Boost: score.Boost, ThresholdAtDecision: &threshold, Thread: row.Thread,
		}); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		total++
		fmt.Fprintln(out, format(row, decision, score))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "%d notifications: %d alerted, %d muted at threshold %d%%\n", total, alerted, total-alerted, opt.Threshold)
	return err
}

func parse(text string, lineNumber int) (line, error) {
	var row line
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&row); err != nil {
		return line{}, fmt.Errorf("line %d: %w", lineNumber, err)
	}
	if row.Title == "" && row.Subtitle == "" && row.Body == "" {
		return line{}, fmt.Errorf("line %d: needs a title, subtitle or body", lineNumber)
	}
	if row.Label != nil && (*row.Label < 0 || *row.Label > 100) {
		return line{}, fmt.Errorf("line %d: label %d is outside 0-100", lineNumber, *row.Label)
	}
	return row, nil
}

func format(row line, d decide.Decision, score classify.Score) string {
	word := "muted"
	if d.Interrupt {
		word = "ALERT"
	}
	who := row.Sender
	if who == "" {
		who = row.Title
	}
	snippet := row.Body
	if snippet == "" {
		snippet = row.Title
	}
	label := ""
	if row.Label != nil {
		label = fmt.Sprintf("  [label %d]", *row.Label)
	}
	return fmt.Sprintf("%-5s %3d +%-2d = %3d  %-8s %s: %s%s", word, points(score.Base), points(score.Boost), points(d.Effective), row.Source, who, shorten(snippet), label)
}

func points(score float64) int { return int(math.Round(score * 100)) }

func shorten(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) <= maxTextWidth {
		return string(runes)
	}
	return string(runes[:maxTextWidth-1]) + "…"
}
