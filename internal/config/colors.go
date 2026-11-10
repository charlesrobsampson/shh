package config

import (
	"fmt"
	"strings"
)

// ColorChartURL is where the 8-bit ANSI palette that colors are chosen from is laid out.
const ColorChartURL = "https://en.wikipedia.org/wiki/ANSI_escape_code#8-bit"

// ExplainColorError adds what a color is now to an error about a color of the wrong type, which is what a #rrggbb string left over from
// the days of hex colors gives. Any other error is returned as it was.
func ExplainColorError(err error) error {
	if err != nil && strings.Contains(err.Error(), `color" has the wrong type`) {
		return fmt.Errorf("%w; a color is a number from 0 to 255 from the 8-bit ANSI palette, not a #rrggbb code (%s; shh alert colors shows the palette)", err, ColorChartURL)
	}
	return err
}

// DefaultColor is the banner color when nothing says otherwise: 208, an orange.
const DefaultColor = 208

// NoColor as a button color means the system's own look.
const NoColor = -1

// DefaultTextColor is the banner's text color when nothing says otherwise: 15, white.
const DefaultTextColor = 15

// ansiStandard are the sixteen colors at the start of the palette, as xterm draws them.
var ansiStandard = [16]string{
	"#000000", "#800000", "#008000", "#808000", "#000080", "#800080", "#008080", "#c0c0c0",
	"#808080", "#ff0000", "#00ff00", "#ffff00", "#0000ff", "#ff00ff", "#00ffff", "#ffffff",
}

// ANSIHex is a code of the 8-bit ANSI palette as #rrggbb: 0 to 15 the standard and bright colors, 16 to 231 a six by six by six cube of
// colors, and 232 to 255 a ramp of greys. A code outside 0 to 255 is taken as the nearest end.
func ANSIHex(code int) string {
	code = max(0, min(code, 255))
	switch {
	case code < 16:
		return ansiStandard[code]
	case code < 232:
		n := code - 16
		level := func(i int) int {
			if i == 0 {
				return 0
			}
			return 55 + 40*i
		}
		return fmt.Sprintf("#%02x%02x%02x", level(n/36), level(n/6%6), level(n%6))
	}
	grey := 8 + 10*(code-232)
	return fmt.Sprintf("#%02x%02x%02x", grey, grey, grey)
}
