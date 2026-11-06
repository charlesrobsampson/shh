package alert

import (
	"errors"
	"strings"

	"github.com/charlesrobsampson/shh/internal/config"
)

// Request is an alert with its style resolved, ready for a backend to draw.
type Request struct {
	Kind     config.Kind
	Source   string
	BundleID string
	Title    string
	Body     string
	Style    config.Style
	// Width comes from Style.Size and Height from the body text and the button row, both in points.
	Width, Height int
	// Buttons are the buttons to draw, left to right: enabled in the configuration and applicable to this alert.
	Buttons []config.Button
	// DismissOnClick is set when the Dismiss button is turned off: a click anywhere outside the other buttons then dismisses.
	DismissOnClick bool
	// PreviewHeight is the height the banner grows to when Preview is pressed; 0 when there is no Preview button.
	PreviewHeight int
}

// Backend draws a request. Show returns once the banner is on its way, not when it goes away.
type Backend interface {
	Show(Request) error
}

// ErrUnavailable means a backend cannot be used on this machine at all, as opposed to failing on one alert.
var ErrUnavailable = errors.New("alert backend unavailable")

var widths = map[string]int{"small": 280, "medium": 360, "large": 460}

// The banner layout, in points. overlay.js uses the same numbers.
const (
	bannerPadding  = 12
	bannerTitle    = 20
	bannerGap      = 4
	bodyLineHeight = 16
	// MaxBodyLines is the most body text a banner shows; anything longer ends in an ellipsis. Alerts are glances, not documents.
	MaxBodyLines = 4
	// MaxPreviewLines is the most the Preview button shows. Even a preview is not a document.
	MaxPreviewLines = 12
	// ButtonRowHeight is the room under the text for the buttons: a 24 point button and 8 points of gap.
	ButtonRowHeight = 32
	// avgCharWidth is deliberately a little more than the real average at 13 points, so the estimate leans towards too much room.
	avgCharWidth = 7.5
	textMargin   = 32
)

// SizeWidth converts a configured size name to a banner width in points; an unknown name means medium.
func SizeWidth(size string) int {
	if w, ok := widths[size]; ok {
		return w
	}
	return widths["medium"]
}

// BannerHeight is how tall a banner of the given width must be to show the body: one line at least, MaxBodyLines at most. The
// number of lines is an estimate from the character count and the explicit line breaks, because the height has to be known before
// the banner is drawn (banners stack by height).
func BannerHeight(width int, body string) int {
	lines := min(max(bodyLines(width, body), 1), MaxBodyLines)
	return 2*bannerPadding + bannerTitle + bannerGap + lines*bodyLineHeight
}

// PreviewHeight is how tall the banner is once Preview shows as much of the body as is allowed, buttons not included.
func PreviewHeight(width int, body string) int {
	lines := min(max(bodyLines(width, body), 1), MaxPreviewLines)
	return 2*bannerPadding + bannerTitle + bannerGap + lines*bodyLineHeight
}

// Truncated says whether the collapsed banner has to cut the body off, which is when Preview has something to show.
func Truncated(width int, body string) bool { return bodyLines(width, body) > MaxBodyLines }

func bodyLines(width int, body string) int {
	perLine := max(8, int(float64(width-textMargin)/avgCharWidth))
	lines := 0
	for _, paragraph := range strings.Split(body, "\n") {
		if n := len([]rune(strings.TrimSpace(paragraph))); n > 0 {
			lines += (n + perLine - 1) / perLine
		}
	}
	return lines
}
