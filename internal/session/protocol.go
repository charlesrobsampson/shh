package session

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
)

// The worker speaks JSON lines over a unix socket. A client sends one Request per line and gets one Response per line. After a
// "subscribe" request is acknowledged the connection carries only Event lines, until the client hangs up or the worker stops.

var (
	// ErrNotRunning means no worker is listening on the socket.
	ErrNotRunning = errors.New("shh is not running")
	// ErrAlreadyRunning means a live worker already owns the socket.
	ErrAlreadyRunning = errors.New("shh is already running")
)

// BuildID identifies the running executable; main sets it at start-up. Empty when unknown.
var BuildID string

type Request struct {
	Cmd          string `json:"cmd"`
	Level        *int   `json:"level,omitempty"`
	DurationSecs int    `json:"duration_secs,omitempty"`
	Then         *int   `json:"then,omitempty"`
	Note         string `json:"note,omitempty"`
	All          bool   `json:"all,omitempty"`
	DeltaSecs    int    `json:"delta_secs,omitempty"`
	// NotificationID, Label and Feedback are what the "train" command labels: a stored notification, and either an exact 0 to 100 label or
	// y/n.
	NotificationID int64  `json:"notification_id,omitempty"`
	Label          *int   `json:"label,omitempty"`
	Feedback       string `json:"feedback,omitempty"`
	// SessionAlert is how "focus" announces the end of the session: none, sound or banner.
	SessionAlert string `json:"session_alert,omitempty"`
	// Name is the routine that "routine_run" starts.
	Name string `json:"name,omitempty"`
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
	// NotificationID is the stored notification the alert is about; a negative number is a demonstration (the `shh alert test`
	// samples), which shows the Flag button without there being anything to flag.
	NotificationID int64 `json:"notification_id,omitempty"`
}

type Response struct {
	OK     bool        `json:"ok"`
	Code   string      `json:"code,omitempty"`
	Error  string      `json:"error,omitempty"`
	Status *StatusView `json:"status,omitempty"`
	// Train is the outcome of a "train" or "undo" command.
	Train *TrainView `json:"train,omitempty"`
	// Routine answers the routine commands: the run in progress, if any, and the last one that ended.
	Routine *RoutineInfo `json:"routine,omitempty"`
}

// RoutineView is the routine run in progress, as clients see it.
type RoutineView struct {
	Name string `json:"name"`
	// Position is the step in force or, while Waiting, the step waiting to be started; counting from 0. Total is how many steps there are.
	Position int `json:"position"`
	Total    int `json:"total"`
	// Step is that step as it is labelled on screen ("work (2/4)"); Previous is the step that just ended while waiting, and Next the
	// one after Step ("" when Step is the last).
	Step     string `json:"step"`
	Previous string `json:"previous,omitempty"`
	Next     string `json:"next,omitempty"`
	Waiting  bool   `json:"waiting"`
	// Level and LengthSecs are those of Step, so a client can say "break: 5m at off" for a step that has not started.
	Level      int       `json:"level"`
	LengthSecs int       `json:"length_secs"`
	Started    time.Time `json:"started"`
}

// RoutineSummary is what a finished run did.
type RoutineSummary struct {
	Name   string `json:"name"`
	Reason string `json:"reason"` // finished, stopped, replaced or missing
	// Steps is how many steps were started, out of Total.
	Steps       int       `json:"steps"`
	Total       int       `json:"total"`
	Started     time.Time `json:"started"`
	Ended       time.Time `json:"ended"`
	ElapsedSecs int       `json:"elapsed_secs"`
	Held        int       `json:"held"`
	Let         int       `json:"let"`
	// Where is the places the routine says its summary should be shown: banner, menu, ui.
	Where []string `json:"where,omitempty"`
}

// Sentence is the summary as one sentence: how many steps, how long, and what was held back and let through.
func (s RoutineSummary) Sentence() string {
	took := decide.FormatRemaining(s.ElapsedSecs)
	held := fmt.Sprintf("held back %d notifications", s.Held)
	if s.Held == 1 {
		held = "held back 1 notification"
	}
	return fmt.Sprintf("%d steps in %s. %s, let through %d.", s.Steps, took, strings.ToUpper(held[:1])+held[1:], s.Let)
}

// Line is the summary with the routine and how it ended in front: "pomodoro finished: 4 steps in 1h15m. ...".
func (s RoutineSummary) Line() string {
	return fmt.Sprintf("%s %s: %s", s.Name, s.Reason, s.Sentence())
}

// Shows says whether the routine asked for its summary to be shown there.
func (s RoutineSummary) Shows(where string) bool { return slices.Contains(s.Where, where) }

// RoutineInfo is the answer to the routine commands.
type RoutineInfo struct {
	Active *RoutineView    `json:"active,omitempty"`
	Last   *RoutineSummary `json:"last,omitempty"`
}

// TrainView is a TrainOutcome on the wire.
type TrainView struct {
	NotificationID int64 `json:"notification_id"`
	Before         int   `json:"before"`
	After          int   `json:"after"`
	Updated        bool  `json:"updated"`
}

// StatusView is the stack of focus levels as clients see it.
type StatusView struct {
	// Level is the effective threshold, 0-99; 0 means focus is off.
	Level  int         `json:"level"`
	Layers []LayerView `json:"layers"`
	Now    time.Time   `json:"now"`
	// Routine is the routine run in progress, if there is one.
	Routine *RoutineView `json:"routine,omitempty"`
	// LastRoutine is the run that ended most recently, while none is in progress, for the places its summary is to be shown (see
	// RoutineSummary.Where).
	LastRoutine *RoutineSummary `json:"last_routine,omitempty"`
	// Build identifies the executable the worker is running (see BuildID), so a command can tell when the worker predates the
	// binary it was typed with, which would otherwise be easy to mistake for a bug in the new code.
	Build string `json:"build,omitempty"`
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
	// Alert is how the end of the session is announced ("sound" or "banner"); empty for none.
	Alert string `json:"alert,omitempty"`
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
	"already_trained": ErrAlreadyTrained,
	"nothing_to_undo": ErrNothingToUndo,
	"not_found":       ErrNoSuchNotification,
	"routine_running": ErrRoutineRunning,
	"no_routine":      ErrNoRoutine,
	"unknown_routine": ErrUnknownRoutine,
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
	view := StatusView{Level: st.Level, Now: now, Layers: []LayerView{}, Build: BuildID}
	for _, l := range st.Layers {
		view.Layers = append(view.Layers, LayerView{
			ID: l.ID, Kind: string(l.Kind), Percent: l.Percent, ThenLevel: l.ThenLevel, Note: l.Note, Started: l.StartTime,
			Timed: l.Timed, Paused: l.Paused, RemainingSecs: int(l.Remaining / time.Second), ExpiresAt: l.ExpiresAt,
			Blocked: l.Blocked, Allowed: l.Allowed, Alert: l.Alert,
		})
	}
	return view
}
