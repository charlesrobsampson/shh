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
	// NotificationID is the stored notification this alert is about; 0 when there is none. It is what lets a banner's Flag button say
	// which notification to mark for review.
	NotificationID int64
	// Actions are extra buttons this alert brings with it, after the configured ones: each does something in shh when pressed and then
	// dismisses the banner. ActionToken is handed back with the button's ID, so the receiver can tell which alert it was.
	Actions     []Action
	ActionToken string
}

// Action is a button that belongs to one alert, such as "Start next" on the banner that says a routine step is over.
type Action struct {
	ID    string
	Label string
}

// Alerter shows an alert on screen.
type Alerter interface {
	Show(Alert) error
}
