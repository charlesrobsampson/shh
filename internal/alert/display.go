package alert

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charlesrobsampson/shh/internal/config"
)

// Display is the Alerter shh uses: it resolves each alert's style from the configuration and draws it with the primary backend,
// falling back to the secondary one if that is unavailable or fails, so an alert is only lost when everything fails.
type Display struct {
	cfg      func() config.Alert
	primary  Backend
	fallback Backend
	onError  func(error)
}

// NewDisplay builds the alerter. Either backend may be nil; onError hears about primary failures that the fallback covered.
func NewDisplay(cfg config.Alert, primary, fallback Backend, onError func(error)) *Display {
	return NewLiveDisplay(func() config.Alert { return cfg }, primary, fallback, onError)
}

// NewLiveDisplay is NewDisplay for a configuration that can change while shh runs: it is asked for each alert, so an edit to
// config.json shows in the next banner.
func NewLiveDisplay(cfg func() config.Alert, primary, fallback Backend, onError func(error)) *Display {
	return &Display{cfg: cfg, primary: primary, fallback: fallback, onError: onError}
}

func (d *Display) Show(a Alert) error {
	acfg := d.cfg()
	style := acfg.ResolveWith(a.Kind, a.Source, a.Overrides...)
	width := SizeWidth(style.Size)
	req := Request{
		Kind: a.Kind, Source: a.Source, BundleID: a.BundleID, NotificationID: a.NotificationID, ActionToken: a.ActionToken, Title: a.Title, Body: a.Body,
		Style: style, Width: width, Height: BannerHeight(width, a.Body) + headerHeight(a),
	}
	d.addButtons(&req, a, acfg)
	if d.primary == nil && d.fallback == nil {
		return errors.New("no way to show alerts is configured")
	}
	var primaryErr error
	if d.primary != nil {
		if primaryErr = d.primary.Show(req); primaryErr == nil {
			return nil
		}
		if d.onError != nil && d.fallback != nil {
			d.onError(fmt.Errorf("showing the alert on screen: %w", primaryErr))
		}
	}
	if d.fallback == nil {
		return primaryErr
	}
	if err := d.fallback.Show(req); err != nil {
		return errors.Join(primaryErr, err)
	}
	return nil
}

// addButtons picks the buttons that are enabled and make sense for this alert, and grows the banner to fit their row.
func (d *Display) addButtons(req *Request, a Alert, acfg config.Alert) {
	req.DismissOnClick = true
	for _, b := range acfg.ButtonsFor(a.Kind, a.Source) {
		if !b.Enabled {
			continue
		}
		if b.Type == config.ButtonDismiss {
			req.DismissOnClick = false
		}
		if applies(b.Type, req.Width, a) {
			req.Buttons = append(req.Buttons, b)
		}
	}
	for _, act := range a.Actions {
		req.Buttons = append(req.Buttons, config.Button{Type: ButtonAction, Label: act.Label, ID: act.ID, Enabled: true})
	}
	if len(req.Buttons) == 0 {
		return
	}
	// The dismiss button sits in the top right corner. The rest are in rows at the bottom, as many as their words need; a button that
	// says something else once pressed gets room for the longer of the two.
	var words []string
	for _, b := range req.Buttons {
		if b.Type == config.ButtonDismiss {
			continue
		}
		longest := b.Label
		for _, other := range []string{b.CollapseLabel, b.FlaggedLabel, b.TrainedLabel} {
			if utf8.RuneCountInString(other) > utf8.RuneCountInString(longest) {
				longest = other
			}
		}
		words = append(words, longest)
	}
	req.ButtonRows = ButtonRows(req.Width, words)
	req.Height += req.ButtonRows * ButtonRowHeight
	for _, b := range req.Buttons {
		if b.Type == config.ButtonPreview {
			req.PreviewHeight = PreviewHeight(req.Width, a.Body) + headerHeight(a) + req.ButtonRows*ButtonRowHeight
		}
	}
}

// ButtonAction is the type of a button an alert brings with it.
const ButtonAction = "action"

// applies is where each button type says when it makes sense: goto needs an app to open, preview needs cut-off text, flag needs a
// stored notification to mark, and train (task 45) will need one too.
func applies(buttonType string, width int, a Alert) bool {
	switch buttonType {
	case config.ButtonDismiss:
		return true
	case config.ButtonGoto:
		return a.BundleID != ""
	case config.ButtonPreview:
		return Truncated(width, a.Body)
	case config.ButtonFlag, config.ButtonTrain:
		return a.NotificationID != 0
	}
	return false
}

// headerHeight is the room for the app name line: one line when the alert says where it is from, nothing otherwise.
func headerHeight(a Alert) int {
	if strings.TrimSpace(a.Source) == "" {
		return 0
	}
	return HeaderHeight
}
