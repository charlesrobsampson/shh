package calendar_test

import (
	"testing"
	"time"
	_ "time/tzdata" // the DST cases need real zone rules, wherever the tests run

	"github.com/charlesrobsampson/shh/internal/calendar"
	"github.com/charlesrobsampson/shh/internal/rules"
	"github.com/charlesrobsampson/shh/internal/store"
)

const reminderCategory = "OLAlertsNotificationRemindersWithJoinAndChatCategoryIdentifier"

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

type rig struct {
	parser *calendar.Parser
	store  *store.Store
	loc    *time.Location
}

func newRig(t *testing.T, tweak ...func(*rules.Rules)) rig {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	r, err := rules.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range tweak {
		f(&r)
	}
	loc := zone(t, "America/Denver")
	return rig{calendar.NewParser(r, loc, s), s, loc}
}

func at(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, loc)
}

func reminder(body string, delivered time.Time) calendar.Input {
	return calendar.Input{Category: reminderCategory, Title: "Weekly Sync", Subtitle: "Microsoft Teams Meeting", Body: body, Delivered: delivered}
}

func TestStartTimeFormats(t *testing.T) {
	r := newRig(t)
	denver := r.loc
	delivered := at(denver, 2026, time.November, 5, 8, 55)
	tests := []struct {
		name string
		body string
		want time.Time
	}{
		{"the real sample", "November 5, 2026 at 13:30", at(denver, 2026, time.November, 5, 13, 30)},
		{"12-hour clock with pm", "November 5, 2026 at 1:30 PM", at(denver, 2026, time.November, 5, 13, 30)},
		{"12-hour clock, lower case, no space", "November 5, 2026 at 1:30pm", at(denver, 2026, time.November, 5, 13, 30)},
		{"abbreviated month", "Nov 5, 2026 at 13:30", at(denver, 2026, time.November, 5, 13, 30)},
		{"September abbreviated four ways", "Sept 9, 2026 at 09:15", at(denver, 2026, time.September, 9, 9, 15)},
		{"another year", "January 1, 2027 at 00:00", at(denver, 2027, time.January, 1, 0, 0)},
		{"today with am", "Today at 9:00 AM", at(denver, 2026, time.November, 5, 9, 0)},
		{"today with pm", "Today at 9:00 pm", at(denver, 2026, time.November, 5, 21, 0)},
		{"today, noon is 12 PM", "Today at 12:15 PM", at(denver, 2026, time.November, 5, 12, 15)},
		{"today, midnight is 12 AM", "today at 12:05 AM", at(denver, 2026, time.November, 5, 0, 5)},
		{"today on a 24-hour clock", "Today at 15:45", at(denver, 2026, time.November, 5, 15, 45)},
		{"in minutes", "in 5 minutes", delivered.Add(5 * time.Minute)},
		{"in one minute", "In 1 minute", delivered.Add(time.Minute)},
		{"in minutes, abbreviated", "in 15 min", delivered.Add(15 * time.Minute)},
		{"the text can say more than the time", "Starts in 10 minutes - join now", delivered.Add(10 * time.Minute)},
		{"a start in the past still parses", "November 5, 2026 at 08:00", at(denver, 2026, time.November, 5, 8, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := r.parser.Parse(reminder(tt.body, delivered))
			if err != nil || !ok {
				t.Fatalf("Parse = %+v, ok=%v, err=%v", got, ok, err)
			}
			if got.Unparsed || got.AllDay || !got.Start.Equal(tt.want) {
				t.Fatalf("reminder = %+v, want start %v", got, tt.want)
			}
		})
	}
}

func TestTitleAndSubtitleAreCarriedOver(t *testing.T) {
	r := newRig(t)
	got, _, _ := r.parser.Parse(reminder("in 5 minutes", at(r.loc, 2026, time.November, 5, 9, 0)))
	if got.Title != "Weekly Sync" || got.Subtitle != "Microsoft Teams Meeting" {
		t.Errorf("reminder = %+v", got)
	}
}

func TestAllDayRemindersAlertNowWithoutBeingRecordedAsUnparsed(t *testing.T) {
	r := newRig(t)
	for _, body := range []string{"All day", "Today (all-day event)"} {
		got, ok, err := r.parser.Parse(reminder(body, at(r.loc, 2026, time.November, 5, 9, 0)))
		if err != nil || !ok || !got.AllDay || got.Unparsed || !got.AlertNow() {
			t.Errorf("%q: reminder = %+v ok=%v err=%v, want an all-day reminder that alerts now", body, got, ok, err)
		}
	}
	if rows, _ := r.store.UnparsedCalendar(); len(rows) != 0 {
		t.Errorf("an understood all-day format was recorded as unparsed: %+v", rows)
	}
}

func TestDaylightSavingTimeUsesTheRightOffset(t *testing.T) {
	ny := zone(t, "America/New_York")
	s, _ := store.Open(":memory:")
	defer s.Close()
	rl, _ := rules.Defaults()
	p := calendar.NewParser(rl, ny, s)
	utc := func(y int, mo time.Month, d, h, m int) time.Time { return time.Date(y, mo, d, h, m, 0, 0, time.UTC) }

	tests := []struct {
		name      string
		body      string
		delivered time.Time
		want      time.Time
	}{
		{"before the change (EST, UTC-5)", "March 7, 2026 at 9:00 AM", utc(2026, 3, 7, 12, 0), utc(2026, 3, 7, 14, 0)},
		{"after the change (EDT, UTC-4)", "March 9, 2026 at 9:00 AM", utc(2026, 3, 9, 12, 0), utc(2026, 3, 9, 13, 0)},
		{"on the change day, after 2am", "March 8, 2026 at 3:30 AM", utc(2026, 3, 8, 5, 0), utc(2026, 3, 8, 7, 30)},
		{"a relative start across the change is an exact duration", "in 120 minutes", utc(2026, 3, 8, 5, 30), utc(2026, 3, 8, 7, 30)},
		{"after the clocks go back", "November 2, 2026 at 9:00 AM", utc(2026, 11, 2, 12, 0), utc(2026, 11, 2, 14, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := p.Parse(reminder(tt.body, tt.delivered))
			if err != nil || !ok || got.Unparsed || !got.Start.Equal(tt.want) {
				t.Fatalf("reminder = %+v ok=%v err=%v, want start %v", got, ok, err, tt.want)
			}
		})
	}
}

func TestATimeThatDoesNotExistStillGetsAStart(t *testing.T) {
	// 2:30 AM on the spring-forward day never happens in New York; the parser must still schedule something close, not give up.
	ny := zone(t, "America/New_York")
	rl, _ := rules.Defaults()
	got, ok, err := calendar.NewParser(rl, ny, nil).Parse(reminder("March 8, 2026 at 2:30 AM", time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)))
	if err != nil || !ok || got.Unparsed {
		t.Fatalf("Parse = %+v ok=%v err=%v", got, ok, err)
	}
	wallClock := time.Date(2026, 3, 8, 2, 30, 0, 0, time.UTC)
	if diff := time.Date(got.Start.Year(), got.Start.Month(), got.Start.Day(), got.Start.Hour(), got.Start.Minute(), 0, 0, time.UTC).Sub(wallClock).Abs(); diff > time.Hour {
		t.Errorf("start %v is %v away from the requested 2:30", got.Start, diff)
	}
}

func TestUnreadableTimesAlertNowAndAreRecorded(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"an unknown format", "sometime after lunch"},
		{"an empty body", ""},
		{"a day that does not exist", "November 31, 2026 at 13:30"},
		{"an hour that does not exist", "November 5, 2026 at 25:00"},
		{"a minute that does not exist", "November 5, 2026 at 13:75"},
		{"a month nobody has heard of", "Smarch 5, 2026 at 13:30"},
		{"12-hour clock with hour 13", "November 5, 2026 at 13:30 PM"},
		{"12-hour clock with hour 0", "Today at 0:30 AM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			id := insertNotification(t, r.store)
			in := reminder(tt.body, at(r.loc, 2026, time.November, 5, 9, 0))
			in.NotificationID = id
			got, ok, err := r.parser.Parse(in)
			if err != nil || !ok {
				t.Fatalf("Parse = %+v ok=%v err=%v", got, ok, err)
			}
			if !got.Unparsed || !got.AlertNow() || got.Title != "Weekly Sync" {
				t.Errorf("reminder = %+v, want an alert-now result that still carries the title", got)
			}
			rows, _ := r.store.UnparsedCalendar()
			if len(rows) != 1 {
				t.Fatalf("recorded %d unparsed rows, want 1", len(rows))
			}
			row := rows[0]
			if row.NotificationID != id || row.Category != reminderCategory || row.Title != "Weekly Sync" || row.Subtitle != "Microsoft Teams Meeting" {
				t.Errorf("recorded row = %+v", row)
			}
			if tt.body != "" && row.RawText != tt.body {
				t.Errorf("RawText = %q, want the text that failed: %q", row.RawText, tt.body)
			}
			if row.RawText == "" {
				t.Error("an empty body must still leave something to review")
			}
		})
	}
}

func insertNotification(t *testing.T, s *store.Store) int64 {
	t.Helper()
	threshold := 65
	id, _, err := s.InsertNotification(store.Notification{
		Source: "Outlook", Sender: "Calendar", Body: "x", Timestamp: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC),
		Status: store.StatusAlerted, Score: 0.5, ThresholdAtDecision: &threshold,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestOnlyCalendarCategoriesAreReminders(t *testing.T) {
	r := newRig(t)
	delivered := at(r.loc, 2026, time.November, 5, 9, 0)
	tests := []struct {
		name     string
		category string
	}{
		{"an Outlook mail notification", "OLAlertsNotificationCategorydentifier"},
		{"no category at all", ""},
		{"some other category", "com.example.chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := calendar.Input{Category: tt.category, Title: "Re: lunch", Body: "in 5 minutes", Delivered: delivered}
			if _, ok, err := r.parser.Parse(in); ok || err != nil {
				t.Fatalf("Parse ok=%v err=%v, want it ignored", ok, err)
			}
		})
	}
	if rows, _ := r.store.UnparsedCalendar(); len(rows) != 0 {
		t.Errorf("ignored notifications were recorded: %+v", rows)
	}
}

func TestMoreCategoriesCanBeAddedInTheRules(t *testing.T) {
	r := newRig(t, func(r *rules.Rules) {
		extra, err := rules.Load(writeRules(t, "[[calendar.category]]\nname = \"teams-meeting\"\npattern = '^TeamsMeeting'\n"))
		if err != nil {
			t.Fatal(err)
		}
		r.CalendarCategories = extra.CalendarCategories
	})
	delivered := at(r.loc, 2026, time.November, 5, 9, 0)
	in := calendar.Input{Category: "TeamsMeetingReminder", Title: "Design review", Subtitle: "Microsoft Teams", Body: "Starts in 3 minutes", Delivered: delivered}
	got, ok, err := r.parser.Parse(in)
	if err != nil || !ok || got.Unparsed || !got.Start.Equal(delivered.Add(3*time.Minute)) {
		t.Fatalf("Parse = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestTheTimeCanBeInTheSubtitleOrTitle(t *testing.T) {
	r := newRig(t)
	delivered := at(r.loc, 2026, time.November, 5, 9, 0)
	tests := []struct {
		name string
		in   calendar.Input
	}{
		{"subtitle", calendar.Input{Category: reminderCategory, Title: "Sync", Subtitle: "Starts in 10 minutes", Delivered: delivered}},
		{"title", calendar.Input{Category: reminderCategory, Title: "Sync starts in 10 minutes", Delivered: delivered}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := r.parser.Parse(tt.in)
			if err != nil || !ok || got.Unparsed || !got.Start.Equal(delivered.Add(10*time.Minute)) {
				t.Fatalf("Parse = %+v ok=%v err=%v", got, ok, err)
			}
		})
	}
}

func TestPatternsAreTriedInOrder(t *testing.T) {
	r := newRig(t)
	delivered := at(r.loc, 2026, time.November, 5, 9, 0)
	got, _, _ := r.parser.Parse(reminder("November 5, 2026 at 13:30 (in 3 minutes)", delivered))
	if want := at(r.loc, 2026, time.November, 5, 13, 30); !got.Start.Equal(want) {
		t.Fatalf("start = %v, want the absolute time %v, which comes before the relative pattern", got.Start, want)
	}
}

func TestParsingWorksWithoutAStore(t *testing.T) {
	r, _ := rules.Defaults()
	p := calendar.NewParser(r, time.UTC, nil)
	got, ok, err := p.Parse(reminder("sometime", time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)))
	if err != nil || !ok || !got.Unparsed {
		t.Fatalf("Parse = %+v ok=%v err=%v, want an unparsed result and no crash", got, ok, err)
	}
}

func TestTheTimeZoneIsTheOneTheParserWasGiven(t *testing.T) {
	r, _ := rules.Defaults()
	denver, tokyo := zone(t, "America/Denver"), zone(t, "Asia/Tokyo")
	delivered := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)
	a, _, _ := calendar.NewParser(r, denver, nil).Parse(reminder("Today at 9:00 AM", delivered))
	b, _, _ := calendar.NewParser(r, tokyo, nil).Parse(reminder("Today at 9:00 AM", delivered))
	if a.Start.Equal(b.Start) || a.Start.Location() != denver || b.Start.Location() != tokyo {
		t.Errorf("starts = %v and %v, want the same wall-clock time in different zones", a.Start, b.Start)
	}
}
