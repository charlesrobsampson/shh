package alert

import "math"

// glyphWidths are the widths in points of the printable ASCII characters in the system font at 12 points, which is what button words are
// set in (measured with NSString sizeWithAttributes: on macOS; ranging from the narrow i to the wide W). They let the width of a button's
// words be worked out here as closely as the banner script measures it, so the rows of buttons that are reserved are the rows it uses.
var glyphWidths = [95]float64{
	3.38, 3.73, 5.73, 7.56, 7.56,
	11.10, 8.54, 3.56, 4.58, 4.58,
	5.66, 7.56, 3.56, 5.66, 3.56,
	3.66, 7.56, 5.57, 7.24, 7.52,
	7.72, 7.42, 7.64, 6.83, 7.66,
	7.64, 3.56, 3.56, 7.56, 7.56,
	7.56, 6.15, 11.02, 8.09, 7.89,
	8.59, 8.72, 7.15, 6.87, 8.96,
	8.91, 3.21, 6.46, 7.90, 6.81,
	10.49, 8.91, 9.26, 7.62, 9.26,
	7.84, 7.65, 7.61, 8.85, 8.09,
	11.61, 8.14, 7.86, 7.94, 4.58,
	3.66, 4.58, 7.56, 7.00, 6.00,
	6.62, 7.37, 6.71, 7.37, 6.86,
	4.34, 7.31, 7.06, 2.96, 2.96,
	6.52, 3.04, 10.44, 7.00, 7.09,
	7.32, 7.31, 4.57, 6.28, 4.36,
	7.00, 6.50, 9.29, 6.29, 6.52,
	6.47, 4.58, 3.11, 4.58, 7.56,
}

// otherGlyph is the width taken for a character that is not printable ASCII (an accented letter, a symbol): about that of a wide one.
const otherGlyph = 12.0

// textWidth is how wide the words are, in points, at 12 points in the system font.
func textWidth(words string) float64 {
	total := 0.0
	for _, r := range words {
		if r >= 32 && r < 127 {
			total += glyphWidths[r-32]
		} else {
			total += otherGlyph
		}
	}
	return total
}

// buttonSlot is how wide, at most, a button with these words is: the words and 20 points of padding either side of them (what the
// system makes a small button), and two more for the little the measuring can be out by. The banner script measures the same words and
// adds the same 20, so it never needs more room than this, and the rows reserved here are the rows it uses.
func buttonSlot(words string) int {
	return int(math.Ceil(textWidth(words))) + 22
}
