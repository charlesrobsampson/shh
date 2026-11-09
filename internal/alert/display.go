package alert

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charlesrobsampson/shh/internal/config"
)

// Display is the Alerter shh uses: it resolves each alert's style from the configuration and draws it with the primary backend,
// falling back to the secondary one if that is unavailable or fails, so an alert is only lost when everything fails.
type Display struct {
	cfg      config.Alert
	primary  Backend
	fallback Backend
	onError  func(error)
}

// NewDisplay builds the alerter. Either backend may be nil; onError hears about primary failures that the fallback covered.
func NewDisplay(cfg config.Alert, primary, fallback Backend, onError func(error)) *Display {
	return &Display{cfg: cfg, primary: primary, fallback: fallback, onError: onError}
}

func (d *Display) Show(a Alert) error {
	style := d.cfg.Resolve(a.Kind, a.Source)
	width := SizeWidth(style.Size)
	req := Request{
		Kind: a.Kind, Source: a.Source, BundleID: a.BundleID, NotificationID: a.NotificationID, Title: a.Title, Body: a.Body,
		Style: style, Width: width, Height: BannerHeight(width, a.Body) + headerHeight(a),
	}
	d.addButtons(&req, a)
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
func (d *Display) addButtons(req *Request, a Alert) {
	req.DismissOnClick = true
	for _, b := range d.cfg.ButtonsFor(a.Kind, a.Source) {
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
	if len(req.Buttons) == 0 {
		return
	}
	req.Height += ButtonRowHeight
	for _, b := range req.Buttons {
		if b.Type == config.ButtonPreview {
			req.PreviewHeight = PreviewHeight(req.Width, a.Body) + headerHeight(a) + ButtonRowHeight
		}
	}
}

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
	case config.ButtonFlag:
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
