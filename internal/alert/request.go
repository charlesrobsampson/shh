package alert

import (
	"errors"

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
	// Width and Height are the banner size in points, from Style.Size.
	Width, Height int
}

// Backend draws a request. Show returns once the banner is on its way, not when it goes away.
type Backend interface {
	Show(Request) error
}

// ErrUnavailable means a backend cannot be used on this machine at all, as opposed to failing on one alert.
var ErrUnavailable = errors.New("alert backend unavailable")

var sizes = map[string][2]int{"small": {280, 64}, "medium": {360, 84}, "large": {460, 110}}

// SizePixels converts a configured size name to a width and height in points.
func SizePixels(size string) (width, height int) {
	s, ok := sizes[size]
	if !ok {
		s = sizes["medium"]
	}
	return s[0], s[1]
}
