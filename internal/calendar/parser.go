package calendar

import (
	"strconv"
	"strings"
	"time"

	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
)

// Input is the part of a notification the parser needs.
type Input struct {
	NotificationID int64 // for the unparsed record; 0 when unknown
	Category       string
	Title          string
	Subtitle       string
	Body           string
	Delivered      time.Time
}

// Reminder is a recognised calendar reminder.
type Reminder struct {
	Title    string
	Subtitle string
	// Start is when the meeting begins, in the parser's time zone. It is only meaningful when the reminder is timed.
	Start time.Time
	// AllDay marks an untimed (all-day) reminder.
	AllDay bool
	// Unparsed means the start time could not be read.
	Unparsed bool
}

// AlertNow reports whether there is no start time to schedule against, so the alert should fire right away. Alerting early beats
// missing the meeting (plan.md section 4.2).
func (r Reminder) AlertNow() bool { return r.AllDay || r.Unparsed }

// Parser recognises calendar reminders and reads their start time with the patterns from the rules.
type Parser struct {
	categories []rules.Pattern
	dates      []rules.Pattern
	loc        *time.Location
	store      *store.Store
}

// NewParser reads start times in loc. store records reminders it cannot read; it may be nil.
func NewParser(r rules.Rules, loc *time.Location, s *store.Store) *Parser {
	return &Parser{categories: r.CalendarCategories, dates: r.DatePatterns, loc: loc, store: s}
}

// Parse reports ok=false for notifications that are not calendar reminders. A reminder whose time cannot be read comes back as
// Unparsed (and is recorded for `shh rules review`); err is only ever a failure to record it.
func (p *Parser) Parse(in Input) (reminder Reminder, ok bool, err error) {
	if !p.isReminder(in.Category) {
		return Reminder{}, false, nil
	}
	reminder = Reminder{Title: in.Title, Subtitle: in.Subtitle}
	delivered := in.Delivered.In(p.loc)
	for _, text := range []string{in.Body, in.Subtitle, in.Title} {
		for _, pattern := range p.dates {
			match := pattern.Regexp.FindStringSubmatch(text)
			if match == nil {
				continue
			}
			if start, allDay, understood := p.read(pattern, match, delivered); understood {
				reminder.Start, reminder.AllDay = start, allDay
				return reminder, true, nil
			}
		}
	}
	reminder.Unparsed = true
	return reminder, true, p.record(in)
}

func (p *Parser) isReminder(category string) bool {
	for _, pattern := range p.categories {
		if pattern.Regexp.MatchString(category) {
			return true
		}
	}
	return false
}

func (p *Parser) record(in Input) error {
	if p.store == nil {
		return nil
	}
	raw := in.Body
	if raw == "" {
		raw = in.Subtitle
	}
	if raw == "" {
		raw = in.Title
	}
	if raw == "" {
		raw = "(empty notification)"
	}
	return p.store.AddUnparsedCalendar(store.UnparsedCalendar{
		NotificationID: in.NotificationID, Category: in.Category, Title: in.Title, Subtitle: in.Subtitle, RawText: raw,
	})
}

// read turns one pattern match into a start time. The pattern's named groups say what it found: minutes (relative to delivery),
// hour and minute with optional ampm (today, or the date given by year, month and day). A match with neither means the
// wording is untimed, such as "all day". understood is false when the values do not form a real time.
func (p *Parser) read(pattern rules.Pattern, match []string, delivered time.Time) (start time.Time, allDay, understood bool) {
	group := func(name string) string {
		i := pattern.Regexp.SubexpIndex(name)
		if i < 0 {
			return ""
		}
		return match[i]
	}
	if minutes := group("minutes"); minutes != "" {
		n, err := strconv.Atoi(minutes)
		if err != nil {
			return time.Time{}, false, false
		}
		return delivered.Add(time.Duration(n) * time.Minute), false, true
	}
	if group("hour") == "" {
		return time.Time{}, true, true
	}

	hour, err1 := strconv.Atoi(group("hour"))
	minute, err2 := strconv.Atoi(group("minute"))
	if err1 != nil || err2 != nil || minute < 0 || minute > 59 {
		return time.Time{}, false, false
	}
	switch ampm := strings.ToLower(group("ampm")); {
	case ampm != "" && (hour < 1 || hour > 12):
		return time.Time{}, false, false
	case ampm == "pm" && hour < 12:
		hour += 12
	case ampm == "am" && hour == 12:
		hour = 0
	case hour > 23:
		return time.Time{}, false, false
	}

	year, month, day := delivered.Year(), delivered.Month(), delivered.Day()
	if name := group("month"); name != "" {
		var ok bool
		if month, ok = monthByName(name); !ok {
			return time.Time{}, false, false
		}
		if day, err1 = strconv.Atoi(group("day")); err1 != nil {
			return time.Time{}, false, false
		}
		if y := group("year"); y != "" {
			if year, err2 = strconv.Atoi(y); err2 != nil {
				return time.Time{}, false, false
			}
		}
	}
	start = time.Date(year, month, day, hour, minute, 0, 0, p.loc)
	if start.Year() != year || start.Month() != month || start.Day() != day {
		return time.Time{}, false, false // for example November 31, which time.Date would roll into December
	}
	return start, false, true
}

func monthByName(name string) (time.Month, bool) {
	name = strings.ToLower(name)
	if len(name) < 3 {
		return 0, false
	}
	for m := time.January; m <= time.December; m++ {
		if strings.HasPrefix(strings.ToLower(m.String()), name[:3]) {
			return m, true
		}
	}
	return 0, false
}
