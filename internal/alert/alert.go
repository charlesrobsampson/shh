package alert

import "github.com/charlesrobsampson/shh/internal/config"

// Alert is what shh wants to put in front of the user.
type Alert struct {
	// Kind selects the style (config.KindCalendar can override the base style).
	Kind config.Kind
	// Source is the friendly name of the app the notification came from, used for per-source styling.
	Source string
	// BundleID is the app to open when the alert is clicked; empty for none.
	BundleID string
	Title    string
	Body     string
}

// Alerter shows an alert on screen.
type Alerter interface {
	Show(Alert) error
}
