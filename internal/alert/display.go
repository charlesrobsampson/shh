package alert

import (
	"errors"
	"fmt"

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
		Kind: a.Kind, Source: a.Source, BundleID: a.BundleID, Title: a.Title, Body: a.Body,
		Style: style, Width: width, Height: BannerHeight(width, a.Body),
	}
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
