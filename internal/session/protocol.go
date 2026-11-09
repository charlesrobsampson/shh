package session

import (
	"errors"
	"time"
)

// The worker speaks JSON lines over a unix socket. A client sends one Request per line and gets one Response per line. After a
// "subscribe" request is acknowledged the connection carries only Event lines, until the client hangs up or the worker stops.

var (
	// ErrNotRunning means no worker is listening on the socket.
	ErrNotRunning = errors.New("shh is not running")
	// ErrAlreadyRunning means a live worker already owns the socket.
	ErrAlreadyRunning = errors.New("shh is already running")
)

type Request struct {
	Cmd          string `json:"cmd"`
	Level        *int   `json:"level,omitempty"`
	DurationSecs int    `json:"duration_secs,omitempty"`
	Then         *int   `json:"then,omitempty"`
	Note         string `json:"note,omitempty"`
	All          bool   `json:"all,omitempty"`
	DeltaSecs    int    `json:"delta_secs,omitempty"`
	// Alert is what the "alert" command shows, through the same alerter the worker uses, so that every banner on screen is
	// laid out by one process.
	Alert *AlertMsg `json:"alert,omitempty"`
}

// AlertMsg is an alert.Alert on the wire.
type AlertMsg struct {
	Kind     string `json:"kind"`
	Source   string `json:"source,omitempty"`
	BundleID string `json:"bundle_id,omitempty"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

type Response struct {
	OK     bool        `json:"ok"`
	Code   string      `json:"code,omitempty"`
	Error  string      `json:"error,omitempty"`
	Status *StatusView `json:"status,omitempty"`
}

// StatusView is the stack of focus levels as clients see it.
type StatusView struct {
	// Level is the effective threshold, 0-99; 0 means focus is off.
	Level  int         `json:"level"`
	Layers []LayerView `json:"layers"`
	Now    time.Time   `json:"now"`
}

type LayerView struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"` // "baseline" or "escalation"
	Percent   int       `json:"percent"`
	ThenLevel *int      `json:"then_level,omitempty"`
	Note      string    `json:"note,omitempty"`
	Started   time.Time `json:"started"`
	// Timed layers have a countdown; Paused ones have it frozen. RemainingSecs is only meaningful when Timed.
	Timed         bool       `json:"timed"`
	Paused        bool       `json:"paused"`
	RemainingSecs int        `json:"remaining_secs"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Blocked       int        `json:"blocked"`
	Allowed       int        `json:"allowed"`
}

// Event is one line of a subscription.
type Event struct {
	// Type is "decision".
	Type         string           `json:"type"`
	Notification NotificationView `json:"notification"`
	// Alerted is true when shh put an alert up right now; calendar reminders alert later.
	Alerted bool `json:"alerted"`
}

type NotificationView struct {
	ID     int64     `json:"id"`
	Source string    `json:"source"`
	Sender string    `json:"sender"`
	Title  string    `json:"title"`
	Body   string    `json:"body"`
	Time   time.Time `json:"time"`
	Status string    `json:"status"` // "alerted" or "blocked"
	Score  float64   `json:"score"`
	Boost  float64   `json:"boost"`
	Reason string    `json:"reason"`
}

// RemoteError is an error the worker reported. errors.Is matches it against the session errors, such as ErrNothingToStop.
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string { return e.Message }

var codes = map[string]error{
	"nothing_to_stop": ErrNothingToStop,
	"no_timer":        ErrNoTimer,
	"not_paused":      ErrNotPaused,
	"no_session":      ErrNoSession,
	"invalid":         ErrInvalid,
}

func (e *RemoteError) Is(target error) bool {
	known, ok := codes[e.Code]
	return ok && known == target
}

func codeFor(err error) string {
	for code, known := range codes {
		if errors.Is(err, known) {
			return code
		}
	}
	return "internal"
}

func statusView(st Status, now time.Time) StatusView {
	view := StatusView{Level: st.Level, Now: now, Layers: []LayerView{}}
	for _, l := range st.Layers {
		view.Layers = append(view.Layers, LayerView{
			ID: l.ID, Kind: string(l.Kind), Percent: l.Percent, ThenLevel: l.ThenLevel, Note: l.Note, Started: l.StartTime,
			Timed: l.Timed, Paused: l.Paused, RemainingSecs: int(l.Remaining / time.Second), ExpiresAt: l.ExpiresAt,
			Blocked: l.Blocked, Allowed: l.Allowed,
		})
	}
	return view
}
