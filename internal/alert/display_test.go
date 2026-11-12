package alert_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/alert"
	"github.com/charlesrobsampson/shh/internal/config"
)

type recordingBackend struct {
	requests []alert.Request
	err      error
}

func (b *recordingBackend) Show(r alert.Request) error {
	b.requests = append(b.requests, r)
	return b.err
}

func loadConfig(t *testing.T, text string) config.Config {
	t.Helper()
	path := t.TempDir() + "/config.json"
	if err := writeFile(path, text); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestStyleIsResolvedBeforeItReachesTheBackend(t *testing.T) {
	cfg := loadConfig(t, `{
  "alert": {
    "color": 233,
    "size": "small",
    "duration_secs": 8,
    "calendar": {"color": 39, "sound": "Hero"},
    "source": {"Slack": {"sound": "none", "position": "bottom-left", "size": "large"}}
  }
}`)
	tests := []struct {
		name      string
		in        alert.Alert
		wantStyle config.Style
		wantW     int
		wantH     int
	}{
		{"a plain message uses the base style", alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "hi"},
			config.Style{Position: "top-right", Size: "small", Color: 233, TextColor: 15, ButtonColor: -1, ButtonTextColor: -1, DurationSecs: 8, Sound: "Glass"}, 280, 78},
		{"a calendar alert uses the kind override", alert.Alert{Kind: config.KindCalendar, Source: "Calendar", Title: "Sync"},
			config.Style{Position: "top-right", Size: "small", Color: 39, TextColor: 15, ButtonColor: -1, ButtonTextColor: -1, DurationSecs: 8, Sound: "Hero"}, 280, 78},
		{"a source override applies to its messages", alert.Alert{Kind: config.KindMessage, Source: "Slack", Title: "ops"},
			config.Style{Position: "bottom-left", Size: "large", Color: 233, TextColor: 15, ButtonColor: -1, ButtonTextColor: -1, DurationSecs: 8, Sound: "none"}, 460, 78},
		{"a longer body makes the banner taller", alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: strings.Repeat("word ", 20)},
			config.Style{Position: "top-right", Size: "small", Color: 233, TextColor: 15, ButtonColor: -1, ButtonTextColor: -1, DurationSecs: 8, Sound: "Glass"}, 280, 110},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := &recordingBackend{}
			d := alert.NewDisplay(cfg.Alert, primary, nil, nil)
			if err := d.Show(tt.in); err != nil {
				t.Fatal(err)
			}
			if len(primary.requests) != 1 {
				t.Fatalf("backend got %d requests, want 1", len(primary.requests))
			}
			got := primary.requests[0]
			if got.Style != tt.wantStyle || got.Width != tt.wantW || got.Height != tt.wantH {
				t.Errorf("request style %+v %dx%d, want %+v %dx%d", got.Style, got.Width, got.Height, tt.wantStyle, tt.wantW, tt.wantH)
			}
			if got.Title != tt.in.Title || got.Body != tt.in.Body || got.Source != tt.in.Source || got.BundleID != tt.in.BundleID || got.Kind != tt.in.Kind {
				t.Errorf("request lost the alert's content: %+v", got)
			}
		})
	}
}

func TestEverySizeNameHasAWidth(t *testing.T) {
	small, medium, large := alert.SizeWidth("small"), alert.SizeWidth("medium"), alert.SizeWidth("large")
	if small < 200 || !(small < medium && medium < large) {
		t.Errorf("widths %d, %d, %d: want readable and increasing", small, medium, large)
	}
	if alert.SizeWidth("nonsense") != medium {
		t.Error("an unknown size should fall back to medium")
	}
}

func TestTheBannerHeightFollowsTheText(t *testing.T) {
	tests := []struct {
		name  string
		width int
		body  string
		want  int
	}{
		{"no body still has one line of room", 360, "", 64},
		{"a short body is one line", 360, "Starts at 10:30", 64},
		{"a body just under a line", 360, strings.Repeat("a", 43), 64},
		{"a body just over a line wraps to two", 360, strings.Repeat("a", 44), 80},
		{"explicit line breaks count", 360, "one\ntwo\nthree", 96},
		{"blank lines do not add height", 360, "one\n\n\ntwo", 80},
		{"four lines is the most that is shown", 360, strings.Repeat("a", 43*4), 112},
		{"a huge message does not make a huge banner", 360, strings.Repeat("a very long message ", 500), 112},
		{"a wider banner fits more per line", 460, strings.Repeat("a", 100), 80},
		{"a narrow banner needs more lines for the same text", 280, strings.Repeat("a", 100), 112},
		{"text is counted in characters, not bytes", 360, strings.Repeat("é", 43), 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := alert.BannerHeight(tt.width, tt.body); got != tt.want {
				t.Errorf("BannerHeight(%d, %d chars) = %d, want %d", tt.width, len([]rune(tt.body)), got, tt.want)
			}
		})
	}
}

func TestTheFallbackIsUsedWhenTheOverlayIsUnavailable(t *testing.T) {
	cfg := loadConfig(t, "")
	primary := &recordingBackend{err: alert.ErrUnavailable}
	fallback := &recordingBackend{}
	var reported []error
	d := alert.NewDisplay(cfg.Alert, primary, fallback, func(err error) { reported = append(reported, err) })

	if err := d.Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "hi"}); err != nil {
		t.Fatalf("the alert was lost although a fallback exists: %v", err)
	}
	if len(fallback.requests) != 1 || fallback.requests[0].Title != "Ana" {
		t.Fatalf("fallback requests = %+v", fallback.requests)
	}
	if len(reported) != 1 || !errors.Is(reported[0], alert.ErrUnavailable) {
		t.Errorf("the overlay problem was not reported: %v", reported)
	}
}

func TestAnyOverlayFailureFallsBack(t *testing.T) {
	cfg := loadConfig(t, "")
	primary := &recordingBackend{err: errors.New("the helper crashed")}
	fallback := &recordingBackend{}
	d := alert.NewDisplay(cfg.Alert, primary, fallback, nil)
	if err := d.Show(alert.Alert{Title: "x"}); err != nil || len(fallback.requests) != 1 {
		t.Fatalf("err=%v fallback requests=%d, want the fallback to take over", err, len(fallback.requests))
	}
}

func TestAnAlertIsNotShownTwice(t *testing.T) {
	cfg := loadConfig(t, "")
	primary, fallback := &recordingBackend{}, &recordingBackend{}
	d := alert.NewDisplay(cfg.Alert, primary, fallback, nil)
	d.Show(alert.Alert{Title: "x"})
	if len(primary.requests) != 1 || len(fallback.requests) != 0 {
		t.Fatalf("primary %d, fallback %d; the fallback must only run when the overlay fails", len(primary.requests), len(fallback.requests))
	}
}

func TestWhenEverythingFailsTheErrorIsReturned(t *testing.T) {
	cfg := loadConfig(t, "")
	one, two := errors.New("overlay down"), errors.New("notifications down")
	d := alert.NewDisplay(cfg.Alert, &recordingBackend{err: one}, &recordingBackend{err: two}, nil)
	err := d.Show(alert.Alert{Title: "x"})
	if !errors.Is(err, one) || !errors.Is(err, two) {
		t.Fatalf("err = %v, want both failures", err)
	}
	if err := alert.NewDisplay(cfg.Alert, &recordingBackend{err: one}, nil, nil).Show(alert.Alert{Title: "x"}); !errors.Is(err, one) {
		t.Errorf("with no fallback err = %v, want the overlay failure", err)
	}
}

func TestWithNoBackendsAtAllShowReportsIt(t *testing.T) {
	cfg := loadConfig(t, "")
	if err := alert.NewDisplay(cfg.Alert, nil, nil, nil).Show(alert.Alert{Title: "x"}); err == nil || !strings.Contains(err.Error(), "no way") {
		t.Fatalf("err = %v, want a clear 'no way to show alerts' error", err)
	}
}

func shown(t *testing.T, cfgText string, in alert.Alert) alert.Request {
	t.Helper()
	cfg := loadConfig(t, cfgText)
	primary := &recordingBackend{}
	if err := alert.NewDisplay(cfg.Alert, primary, nil, nil).Show(in); err != nil {
		t.Fatal(err)
	}
	return primary.requests[0]
}

func buttonTypes(r alert.Request) string {
	var out []string
	for _, b := range r.Buttons {
		out = append(out, b.Type)
	}
	return strings.Join(out, ",")
}

const longBody = "This message is long enough that the collapsed banner has to cut it off: a b c d e f g h i j k l m n o p q r s t u v w x y z a b c d e f g h i j k l m n o p q r s t u v w x y z"

func TestButtonsThatDoNotApplyAreHidden(t *testing.T) {
	tests := []struct {
		name string
		in   alert.Alert
		want string
	}{
		{"short text with an app: no preview", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"}, "goto,dismiss"},
		{"cut-off text with an app: everything", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: longBody}, "preview,goto,dismiss"},
		{"no app to open: no go to", alert.Alert{Kind: config.KindMessage, Title: "t", Body: longBody}, "preview,dismiss"},
		{"nothing to preview and no app", alert.Alert{Kind: config.KindCalendar, Title: "t", Body: "Starts at 10"}, "dismiss"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buttonTypes(shown(t, "", tt.in)); got != tt.want {
				t.Errorf("buttons = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTheTrainButtonStaysOutUntilAlertsCanBeTrained(t *testing.T) {
	r := shown(t, `{"alert": {"buttons": [{"type": "train"}]}}`, alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"})
	if strings.Contains(buttonTypes(r), "train") {
		t.Errorf("buttons = %q; an alert without a stored notification cannot be trained", buttonTypes(r))
	}
}

func TestButtonOrderAndLabelsFollowTheConfig(t *testing.T) {
	r := shown(t, `{"alert": {"buttons": [{"type": "dismiss", "label": "Close"}, {"type": "goto", "label": "Open"}]}}`,
		alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: longBody})
	if buttonTypes(r) != "dismiss,goto" || r.Buttons[0].Label != "Close" || r.Buttons[1].Label != "Open" {
		t.Errorf("buttons = %+v, want dismiss(Close) then goto(Open), no preview (not listed)", r.Buttons)
	}
}

func TestABannerWithButtonsIsTallerByOneRowAndOneWithoutIsNot(t *testing.T) {
	in := alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"}
	with := shown(t, "", in)
	without := shown(t, `{"alert": {"buttons": [{"type": "goto", "enabled": false}, {"type": "dismiss", "enabled": false}]}}`, in)
	if with.Height != without.Height+alert.ButtonRowHeight || without.Height != alert.BannerHeight(with.Width, "hi") {
		t.Errorf("heights with %d, without %d; want the row (%d) on top of %d", with.Height, without.Height, alert.ButtonRowHeight, alert.BannerHeight(with.Width, "hi"))
	}
}

func TestWithoutADismissButtonAClickAnywhereDismisses(t *testing.T) {
	in := alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"}
	if shown(t, "", in).DismissOnClick {
		t.Error("with a Dismiss button the background should not dismiss")
	}
	if r := shown(t, `{"alert": {"buttons": [{"type": "goto"}]}}`, in); !r.DismissOnClick {
		t.Error("with Dismiss off, a click on the background should dismiss")
	}
	// An app-less alert with Dismiss on still shows the button, so the background does nothing.
	if shown(t, "", alert.Alert{Kind: config.KindCalendar, Title: "t", Body: "hi"}).DismissOnClick {
		t.Error("Dismiss is enabled, so no background dismissal")
	}
}

func TestPreviewGrowsTheBannerToTheTextButNotWithoutLimit(t *testing.T) {
	r := shown(t, "", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: longBody})
	if r.PreviewHeight <= r.Height {
		t.Errorf("preview height %d should exceed the collapsed %d", r.PreviewHeight, r.Height)
	}
	huge := shown(t, "", alert.Alert{Kind: config.KindMessage, Title: "t", Body: strings.Repeat("a very long message ", 500)})
	if want := alert.PreviewHeight(huge.Width, "x") + (alert.MaxPreviewLines-1)*16 + alert.ButtonRowHeight; huge.PreviewHeight != want {
		t.Errorf("huge preview height %d, want the %d-line cap %d", huge.PreviewHeight, alert.MaxPreviewLines, want)
	}
	none := shown(t, "", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"})
	if none.PreviewHeight != 0 {
		t.Errorf("no preview button, but PreviewHeight = %d", none.PreviewHeight)
	}
}

func TestTheHeaderNamingTheAppAddsOneLineOnlyWhenThereIsASource(t *testing.T) {
	with := shown(t, "", alert.Alert{Kind: config.KindMessage, Source: "Teams", BundleID: "com.x", Title: "t", Body: "hi"})
	without := shown(t, "", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: "hi"})
	blank := shown(t, "", alert.Alert{Kind: config.KindMessage, Source: "   ", BundleID: "com.x", Title: "t", Body: "hi"})
	if with.Height != without.Height+alert.HeaderHeight {
		t.Errorf("height with a source %d, without %d: want one header line (%d) more", with.Height, without.Height, alert.HeaderHeight)
	}
	if blank.Height != without.Height {
		t.Errorf("a blank source took room: %d vs %d", blank.Height, without.Height)
	}
}

func TestPreviewHeightIncludesTheHeader(t *testing.T) {
	with := shown(t, "", alert.Alert{Kind: config.KindMessage, Source: "Teams", BundleID: "com.x", Title: "t", Body: longBody})
	without := shown(t, "", alert.Alert{Kind: config.KindMessage, BundleID: "com.x", Title: "t", Body: longBody})
	if with.PreviewHeight != without.PreviewHeight+alert.HeaderHeight {
		t.Errorf("preview height %d with a source, %d without; want a header line's difference", with.PreviewHeight, without.PreviewHeight)
	}
}

func TestTheFlagButtonNeedsAStoredNotificationToMark(t *testing.T) {
	with := shown(t, "", alert.Alert{Kind: config.KindMessage, Source: "Teams", BundleID: "com.x", NotificationID: 42, Title: "t", Body: "hi"})
	without := shown(t, "", alert.Alert{Kind: config.KindMessage, Source: "Teams", BundleID: "com.x", Title: "t", Body: "hi"})
	if got := buttonTypes(with); got != "goto,dismiss,flag" {
		t.Errorf("buttons with a notification = %q, want flag after dismiss", got)
	}
	if got := buttonTypes(without); got != "goto,dismiss" {
		t.Errorf("buttons with no notification = %q, want no flag: there is nothing to mark", got)
	}
	if with.NotificationID != 42 {
		t.Errorf("the request lost the notification id: %d", with.NotificationID)
	}
}

func TestTheFlagButtonCanBeTurnedOffOrRelabelled(t *testing.T) {
	in := alert.Alert{Kind: config.KindMessage, BundleID: "com.x", NotificationID: 7, Title: "t", Body: "hi"}
	off := shown(t, `{"alert": {"buttons": [{"type": "goto"}, {"type": "dismiss"}]}}`, in)
	if got := buttonTypes(off); got != "goto,dismiss" {
		t.Errorf("buttons = %q, want flag off when it is not listed", got)
	}
	custom := shown(t, `{"alert": {"buttons": [{"type": "flag", "label": "Review", "flagged_label": "Noted"}, {"type": "dismiss"}]}}`, in)
	if custom.Buttons[0].Type != "flag" || custom.Buttons[0].Label != "Review" || custom.Buttons[0].FlaggedLabel != "Noted" {
		t.Errorf("buttons = %+v", custom.Buttons)
	}
}

func TestAnAlertsOwnActionsBecomeButtonsAfterTheConfiguredOnes(t *testing.T) {
	primary := &recordingBackend{}
	d := alert.NewDisplay(loadConfig(t, "{}").Alert, primary, nil, nil)
	if err := d.Show(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: "work is over", Body: "Next: break",
		ActionToken: "9:2", Actions: []alert.Action{{ID: "next", Label: "Start next"}}}); err != nil {
		t.Fatal(err)
	}
	req := primary.requests[0]
	if req.ActionToken != "9:2" {
		t.Errorf("token = %q", req.ActionToken)
	}
	last := req.Buttons[len(req.Buttons)-1]
	if last.Type != alert.ButtonAction || last.ID != "next" || last.Label != "Start next" {
		t.Errorf("buttons = %+v", req.Buttons)
	}
	// and the banner has room for the button row even when it has no other button
	plain := &recordingBackend{}
	bare := alert.NewDisplay(config.Alert{}, plain, nil, nil)
	_ = bare.Show(alert.Alert{Title: "t", Body: "b", Actions: []alert.Action{{ID: "x", Label: "X"}}})
	if len(plain.requests) == 1 && plain.requests[0].Height < alert.ButtonRowHeight {
		t.Errorf("height = %d", plain.requests[0].Height)
	}
}

func TestOverridesChangeTheResolvedStyleInOrder(t *testing.T) {
	primary := &recordingBackend{}
	d := alert.NewDisplay(loadConfig(t, `{"alert": {"color": 208, "size": "medium", "source": {"Routine": {"sound": "Hero"}}}}`).Alert, primary, nil, nil)
	one, two := 33, 196
	size, pos := "large", "bottom-left"
	if err := d.Show(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: "t", Body: "b",
		Overrides: []config.StyleOverride{{Color: &one, Size: &size}, {Color: &two, Position: &pos}}}); err != nil {
		t.Fatal(err)
	}
	got := primary.requests[0]
	if got.Style.Color != 196 || got.Style.Size != "large" || got.Style.Position != "bottom-left" || got.Style.Sound != "Hero" || got.Width != 460 {
		t.Errorf("style = %+v width %d: the later override wins, the source's sound stays, a larger size widens the banner", got.Style, got.Width)
	}
}

func TestALiveDisplayAsksForTheConfigurationOfEachAlert(t *testing.T) {
	primary := &recordingBackend{}
	cfg := loadConfig(t, `{"alert": {"color": 33}}`).Alert
	d := alert.NewLiveDisplay(func() config.Alert { return cfg }, primary, nil, nil)
	_ = d.Show(alert.Alert{Kind: config.KindMessage, Title: "one", Body: "b"})
	cfg = loadConfig(t, `{"alert": {"color": 196, "size": "large"}}`).Alert
	_ = d.Show(alert.Alert{Kind: config.KindMessage, Title: "two", Body: "b"})
	if len(primary.requests) != 2 || primary.requests[0].Style.Color != 33 || primary.requests[1].Style.Color != 196 || primary.requests[1].Width != 460 {
		t.Errorf("requests = %+v", primary.requests)
	}
}

func TestTheTrainButtonShowsOnlyForAlertsAboutAStoredNotificationAndOnlyWhenEnabled(t *testing.T) {
	primary := &recordingBackend{}
	on := alert.NewDisplay(loadConfig(t, `{"alert": {"buttons": [{"type": "goto"}, {"type": "train"}]}}`).Alert, primary, nil, nil)
	_ = on.Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "b", BundleID: "com.x", NotificationID: 7})
	_ = on.Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "no id", Body: "b", BundleID: "com.x"})
	types := func(r alert.Request) string {
		var out []string
		for _, b := range r.Buttons {
			out = append(out, b.Type+":"+b.Label)
		}
		return strings.Join(out, ",")
	}
	if got := types(primary.requests[0]); got != "goto:Go to,train:Not important" {
		t.Errorf("an alert about a stored notification: %s", got)
	}
	if got := types(primary.requests[1]); got != "goto:Go to" {
		t.Errorf("with nothing stored there is nothing to train: %s", got)
	}
	off := &recordingBackend{}
	_ = alert.NewDisplay(loadConfig(t, `{}`).Alert, off, nil, nil).Show(alert.Alert{Kind: config.KindMessage, Title: "x", Body: "b", NotificationID: 7})
	for _, b := range off.requests[0].Buttons {
		if b.Type == "train" {
			t.Error("the train button is off until it is asked for")
		}
	}
}

func TestButtonsGetAsManyRowsAsTheirWordsNeedAndDismissIsInTheCornerNotInARow(t *testing.T) {
	// the buttons as they are by default plus the train button, with the app name and a long message so that Preview applies
	cfg := loadConfig(t, `{"alert": {"size": "small", "buttons": [{"type": "preview"}, {"type": "goto"}, {"type": "dismiss"}, {"type": "flag"}, {"type": "train"}]}}`).Alert
	long := alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: strings.Repeat("word ", 40), BundleID: "com.x", NotificationID: 5}
	primary := &recordingBackend{}
	d := alert.NewDisplay(cfg, primary, nil, nil)
	if err := d.Show(long); err != nil {
		t.Fatal(err)
	}
	small := primary.requests[0]
	if small.ButtonRows < 2 {
		t.Errorf("Preview, Go to, Flag and Not important do not fit one row of a small banner: rows = %d", small.ButtonRows)
	}
	base := alert.BannerHeight(small.Width, long.Body) + alert.HeaderHeight
	if small.Height != base+small.ButtonRows*alert.ButtonRowHeight {
		t.Errorf("height %d, want %d plus %d rows of %d", small.Height, base, small.ButtonRows, alert.ButtonRowHeight)
	}
	if want := alert.PreviewHeight(small.Width, long.Body) + alert.HeaderHeight + small.ButtonRows*alert.ButtonRowHeight; small.PreviewHeight != want {
		t.Errorf("preview height %d, want %d", small.PreviewHeight, want)
	}
	cfgLarge := loadConfig(t, `{"alert": {"size": "large", "buttons": [{"type": "goto"}, {"type": "dismiss"}, {"type": "flag"}, {"type": "train"}]}}`).Alert
	primary = &recordingBackend{}
	if err := alert.NewDisplay(cfgLarge, primary, nil, nil).Show(long); err != nil {
		t.Fatal(err)
	}
	if got := primary.requests[0].ButtonRows; got != 1 {
		t.Errorf("Go to, Flag and Not important fit one row of a large banner: rows = %d", got)
	}
	onlyDismiss := loadConfig(t, `{"alert": {"buttons": [{"type": "dismiss"}]}}`).Alert
	primary = &recordingBackend{}
	if err := alert.NewDisplay(onlyDismiss, primary, nil, nil).Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	got := primary.requests[0]
	if got.ButtonRows != 0 || got.Height != alert.BannerHeight(got.Width, "hi")+alert.HeaderHeight || len(got.Buttons) != 1 {
		t.Errorf("a banner with only the dismiss button has no row for it: rows %d height %d buttons %d", got.ButtonRows, got.Height, len(got.Buttons))
	}
}

func TestARoutineBannerWithTwoOrMoreActionButtonsGrowsARowWhenTheyNeedIt(t *testing.T) {
	primary := &recordingBackend{}
	d := alert.NewDisplay(loadConfig(t, `{"alert": {"size": "small", "buttons": [{"type": "dismiss"}]}}`).Alert, primary, nil, nil)
	_ = d.Show(alert.Alert{Kind: config.KindMessage, Source: "Routine", Title: "t", Body: "b",
		Actions: []alert.Action{{ID: "a", Label: "Start now"}, {ID: "b", Label: "Skip this one"}, {ID: "c", Label: "Review notifications"}}})
	if got := primary.requests[0].ButtonRows; got < 2 {
		t.Errorf("three long action buttons on a small banner: rows = %d", got)
	}
}

func TestButtonRowsPacksLeftToRightAndALongWordTakesARowToItself(t *testing.T) {
	for _, tt := range []struct {
		width int
		words []string
		want  int
	}{
		{360, nil, 0},
		{360, []string{"Go to"}, 1},
		{360, []string{"Preview", "Go to", "Flag"}, 1},
		{280, []string{"Preview", "Go to", "Flag", "Not important"}, 2},
		{280, []string{"a very very long label indeed, longer than the banner is wide"}, 1},
		{280, []string{"Not important", "Not important", "Not important"}, 2}, // two fit side by side on a small banner
		{280, []string{"Not important", "Not important", "Not important", "Not important", "Not important"}, 3},
		{360, []string{"Preview", "Go to", "Flag", "Not important"}, 1}, // the four buttons of a default banner with the train button on
	} {
		if got := alert.ButtonRows(tt.width, tt.words); got != tt.want {
			t.Errorf("ButtonRows(%d, %q) = %d, want %d", tt.width, tt.words, got, tt.want)
		}
	}
}

func TestTheEstimatedWidthOfWordsMatchesWhatTheSystemMeasuresWithin2Points(t *testing.T) {
	// measured with NSString sizeWithAttributes: at 12 points in the system font, on the Mac this was built on
	for words, measured := range map[string]float64{"Not important": 78.58, "Dismiss": 43.82} {
		est := alert.TextWidthForTest(words)
		if d := est - measured; d < -2 || d > 2 {
			t.Errorf("%q: estimated %.2f, measured %.2f", words, est, measured)
		}
	}
}

// The same words and banner widths as tools/banner_layout/check.py, which lays real banners out headlessly and checks they fit the rows
// reserved here: if these numbers change, change them there.
func TestTheRowsReservedForTheLayoutCheckScenarios(t *testing.T) {
	standard := []string{"Collapse", "Go to", "Flagged", "Not important"}
	actions := []string{"Start now", "Skip this one", "Review notifications"}
	for name, tt := range map[string]struct {
		width int
		words []string
		want  int
	}{
		"small standard": {280, standard, 2}, "medium standard": {360, standard, 1}, "large standard": {460, standard, 1},
		"small actions": {280, actions, 2}, "medium actions": {360, actions, 1},
	} {
		if got := alert.ButtonRows(tt.width, tt.words); got != tt.want {
			t.Errorf("%s: %d rows, want %d", name, got, tt.want)
		}
	}
}

func TestTheTrainButtonIsNeverOnACalendarReminder(t *testing.T) {
	primary := &recordingBackend{}
	d := alert.NewDisplay(loadConfig(t, `{"alert": {"buttons": [{"type": "goto"}, {"type": "flag"}, {"type": "train"}]}}`).Alert, primary, nil, nil)
	_ = d.Show(alert.Alert{Kind: config.KindCalendar, Source: "Calendar", Title: "Standup in 5 minutes", Body: "b", BundleID: "com.x", NotificationID: 9})
	_ = d.Show(alert.Alert{Kind: config.KindMessage, Source: "Teams", Title: "Ana", Body: "b", BundleID: "com.x", NotificationID: 10})
	has := func(r alert.Request, kind string) bool {
		for _, b := range r.Buttons {
			if b.Type == kind {
				return true
			}
		}
		return false
	}
	if has(primary.requests[0], "train") {
		t.Error("a calendar reminder is not scored by the model, so there is nothing to teach it by marking it not important")
	}
	if !has(primary.requests[0], "flag") {
		t.Error("flagging a reminder for review is still possible")
	}
	if !has(primary.requests[1], "train") {
		t.Error("a message the model scored has the train button")
	}
}
