package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/session"
	"github.com/charlesrobsampson/shh/internal/store"
)

type trainCmd struct {
	IDs      []int64 `arg:"" optional:"" name:"id" help:"Notification ids to label (shh ls shows them), all with the same answer."`
	Yes      bool    `help:"Label them: these should have reached me."`
	No       bool    `help:"Label them: these were rightly held back."`
	Label    *int    `help:"Label them with an exact score, 0 to 100."`
	Undo     bool    `help:"Take back the most recent label."`
	Reset    bool    `help:"Forget everything learned from you and go back to the seeded model (--rollback undoes it)."`
	Rollback bool    `help:"Go back to the last checkpoint: a reset, or the seeded model. The notifications trained since are untrained again."`
	Export   bool    `help:"Write the notifications you have labelled, with their text, to --file."`
	Import   bool    `help:"Read labelled notifications from --file (as --export wrote them), store them and train on them."`
	File     string  `default:"private/training.jsonl" help:"The file for --export and --import. The text of your notifications is in it, so keep it under private/."`
}

// Run does one thing: label some notifications, undo a label, reset or roll back the model, or move labelled notifications in or out.
func (c *trainCmd) Run(env *Env) error {
	modes := 0
	for _, on := range []bool{len(c.IDs) > 0 || c.Yes || c.No || c.Label != nil, c.Undo, c.Reset, c.Rollback, c.Export, c.Import} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		return usagef("give notification ids with --yes, --no or --label N, or one of --undo, --reset, --rollback, --export, --import")
	}
	switch {
	case c.Export:
		return c.export(env)
	case c.Reset, c.Rollback, c.Undo, c.Import, len(c.IDs) > 0:
	default:
		return usagef("--yes, --no and --label need notification ids (shh ls shows them)")
	}
	if err := env.ensureWorker(); err != nil {
		return err
	}
	client := env.client()
	switch {
	case c.Reset:
		if err := client.ResetModel(); err != nil {
			return err
		}
		fmt.Fprintln(env.Stdout, "The model is back to the seeded one. shh train --rollback brings back what it had learned from you.")
	case c.Rollback:
		n, err := client.RollbackModel()
		if errors.Is(err, session.ErrNoCheckpoint) {
			return errors.New("there is no earlier state of the model to go back to")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "The model is back at its last checkpoint; %d notification(s) are untrained again.\n", n)
	case c.Undo:
		view, err := client.UndoTraining()
		if errors.Is(err, session.ErrNothingToUndo) {
			return errors.New("there is no label to take back")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Took back the label on #%d.\n", view.NotificationID)
	case c.Import:
		return c.importFile(env, client)
	default:
		return c.label(env, client)
	}
	return nil
}

func (c *trainCmd) label(env *Env, client *session.Client) error {
	answers := 0
	for _, on := range []bool{c.Yes, c.No, c.Label != nil} {
		if on {
			answers++
		}
	}
	if answers != 1 {
		return usagef("give exactly one of --yes, --no and --label N")
	}
	req := session.TrainRequest{Label: c.Label}
	if c.Yes {
		req.Feedback = "y"
	} else if c.No {
		req.Feedback = "n"
	}
	failed := 0
	for _, id := range c.IDs {
		req.NotificationID = id
		view, err := client.Train(req)
		if err != nil {
			fmt.Fprintf(env.Stderr, "shh: #%d: %v\n", id, err)
			failed++
			continue
		}
		fmt.Fprintf(env.Stdout, "#%d: score %d -> %d\n", id, view.Before, view.After)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d could not be labelled", failed, len(c.IDs))
	}
	return nil
}

// trainingRow is one labelled notification in an export: its text, when it came, the level it was decided at and the label.
type trainingRow struct {
	Source   string    `json:"source"`
	BundleID string    `json:"bundle_id,omitempty"`
	Sender   string    `json:"sender,omitempty"`
	Title    string    `json:"title,omitempty"`
	Subtitle string    `json:"subtitle,omitempty"`
	Body     string    `json:"body"`
	Category string    `json:"category,omitempty"`
	Thread   string    `json:"thread,omitempty"`
	At       time.Time `json:"at"`
	Level    int       `json:"level"`
	Feedback string    `json:"feedback,omitempty"`
	Label    *int      `json:"label,omitempty"`
}

func (c *trainCmd) export(env *Env) error {
	if env.WithStore == nil {
		return errors.New("the notifications cannot be read from here")
	}
	var buf bytes.Buffer
	count := 0
	err := env.WithStore(func(st *store.Store) error {
		all, err := st.ListNotifications(store.Filter{})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		for i := len(all) - 1; i >= 0; i-- { // oldest first, so an import trains in the order it happened
			n := all[i]
			if !n.IsTrained || n.ThresholdAtDecision == nil {
				continue
			}
			if err := enc.Encode(trainingRow{Source: n.Source, BundleID: n.BundleID, Sender: n.Sender, Title: n.Title, Subtitle: n.Subtitle,
				Body: n.Body, Category: n.Category, Thread: n.Thread, At: n.Timestamp.UTC(), Level: *n.ThresholdAtDecision,
				Feedback: n.UserFeedback, Label: n.UserLabel}); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.File), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(c.File, buf.Bytes(), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Wrote %d labelled notification(s) to %s.\n", count, c.File)
	warnIfNotPrivate(env.Stderr, c.File)
	return nil
}

// warnIfNotPrivate says so when a file with the text of the user's notifications is somewhere other than a folder called private,
// which the repository keeps out of git.
func warnIfNotPrivate(w io.Writer, path string) {
	abs, err := filepath.Abs(path)
	if err == nil {
		for dir := filepath.Dir(abs); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if filepath.Base(dir) == "private" {
				return
			}
		}
	}
	fmt.Fprintf(w, "shh: warning: %s has the text of your notifications in it and is not under a private/ folder. Keep it out of git and out of anything you share.\n", path)
}

func (c *trainCmd) importFile(env *Env, client *session.Client) error {
	if env.WithStore == nil {
		return errors.New("the notifications cannot be stored from here")
	}
	f, err := os.Open(c.File)
	if err != nil {
		return err
	}
	defer f.Close()
	var rows []trainingRow
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(text))
		dec.DisallowUnknownFields()
		var row trainingRow
		if err := dec.Decode(&row); err != nil {
			return fmt.Errorf("%s line %d: %w", c.File, line, err)
		}
		if row.Source == "" || row.At.IsZero() || (row.Label == nil) == (row.Feedback == "") {
			return fmt.Errorf("%s line %d: a row needs a source, a time (at), and either a label or feedback (y or n)", c.File, line)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	var trained, skipped, stored int
	err = env.WithStore(func(st *store.Store) error {
		for _, row := range rows {
			id, found, err := st.FindNotification(row.Source, row.Title, row.Body, row.At)
			if err != nil {
				return err
			}
			if found {
				if n, err := st.GetNotification(id); err == nil && n.IsTrained {
					skipped++
					continue
				}
			} else {
				level := row.Level
				score := 0.5
				if row.Label != nil {
					score = float64(*row.Label) / 100
				}
				id, _, err = st.InsertNotification(store.Notification{Source: row.Source, BundleID: row.BundleID, Sender: row.Sender, Title: row.Title,
					Subtitle: row.Subtitle, Body: row.Body, Category: row.Category, Thread: row.Thread, Timestamp: row.At, Status: store.StatusBlocked,
					Score: score, Reason: "imported", ThresholdAtDecision: &level})
				if err != nil {
					return err
				}
				stored++
			}
			if _, err := client.Train(session.TrainRequest{NotificationID: id, Label: row.Label, Feedback: row.Feedback}); err != nil {
				return fmt.Errorf("training on the notification from %s at %s: %w", row.Source, row.At.Format(time.RFC3339), err)
			}
			trained++
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Trained on %d notification(s) (%d new to the history); %d were already trained here and were left alone.\n", trained, stored, skipped)
	return nil
}
