package cli

import (
	"fmt"
	"strings"

	"github.com/charlesrobsampson/shh/internal/config"
)

type alertColorsCmd struct {
	Code *int `arg:"" optional:"" name:"number" help:"Show just this color (0 to 255) and the #rrggbb it is drawn as."`
}

// Run draws the palette: the sixteen standard and bright colors, the 216 of the color cube in rows of 18, and the 24 greys, each as a swatch
// with its number, so a color can be picked by eye and written in config.json (or a routine) as "color": 208. On a terminal the swatches
// are real; otherwise each is listed with its hex.
func (c *alertColorsCmd) Run(env *Env) error {
	if c.Code != nil {
		if *c.Code < 0 || *c.Code > 255 {
			return usagef("%d is not a color number: use 0 to 255", *c.Code)
		}
		fmt.Fprintf(env.Stdout, "%s  %d is %s\n", swatch(*c.Code, env.IsTerminal), *c.Code, config.ANSIHex(*c.Code))
		return nil
	}
	fmt.Fprintf(env.Stdout, "The 8-bit ANSI palette (%s). Use a number as \"color\" in config.json, or --color in a routine step.\n", config.ColorChartURL)
	if !env.IsTerminal {
		for code := 0; code < 256; code++ {
			fmt.Fprintf(env.Stdout, "%3d  %s\n", code, config.ANSIHex(code))
		}
		return nil
	}
	row := func(from, to int) {
		var b strings.Builder
		for code := from; code < to; code++ {
			b.WriteString(swatch(code, true))
		}
		fmt.Fprintln(env.Stdout, b.String())
	}
	fmt.Fprintln(env.Stdout, "\nStandard (0-7) and bright (8-15)")
	row(0, 8)
	row(8, 16)
	fmt.Fprintln(env.Stdout, "\nColors (16-231)")
	for from := 16; from < 232; from += 18 {
		row(from, from+18)
	}
	fmt.Fprintln(env.Stdout, "\nGreys (232-255)")
	row(232, 244)
	row(244, 256)
	return nil
}

// swatch is a color's number on a patch of that color, in white or black, whichever can be read.
func swatch(code int, color bool) string {
	if !color {
		return fmt.Sprintf("%3d", code)
	}
	hex := config.ANSIHex(code)
	var r, g, b int
	fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b)
	text := 97 // white
	if (299*r+587*g+114*b)/1000 > 140 {
		text = 30 // black on a light patch
	}
	return fmt.Sprintf("\x1b[48;5;%dm\x1b[%dm %3d \x1b[0m", code, text, code)
}
