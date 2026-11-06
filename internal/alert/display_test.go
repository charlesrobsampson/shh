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
    "color": "#111111",
    "size": "small",
    "duration_secs": 8,
    "calendar": {"color": "#0a84ff", "sound": "Hero"},
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
			config.Style{Position: "top-right", Size: "small", Color: "#111111", DurationSecs: 8, Sound: "Glass"}, 280, 64},
		{"a calendar alert uses the kind override", alert.Alert{Kind: config.KindCalendar, Source: "Calendar", Title: "Sync"},
			config.Style{Position: "top-right", Size: "small", Color: "#0a84ff", DurationSecs: 8, Sound: "Hero"}, 280, 64},
		{"a source override applies to its messages", alert.Alert{Kind: config.KindMessage, Source: "Slack", Title: "ops"},
			config.Style{Position: "bottom-left", Size: "large", Color: "#111111", DurationSecs: 8, Sound: "none"}, 460, 110},
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

func TestEverySizeNameHasPixels(t *testing.T) {
	for _, size := range []string{"small", "medium", "large"} {
		w, h := alert.SizePixels(size)
		if w < 200 || h < 40 {
			t.Errorf("size %q is %dx%d, too small to read", size, w, h)
		}
	}
	sw, _ := alert.SizePixels("small")
	mw, _ := alert.SizePixels("medium")
	lw, _ := alert.SizePixels("large")
	if !(sw < mw && mw < lw) {
		t.Errorf("widths %d, %d, %d are not increasing", sw, mw, lw)
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
