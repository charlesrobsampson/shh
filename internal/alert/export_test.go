package alert

// TextWidthForTest lets the external tests check the width table against what the system measured.
func TextWidthForTest(words string) float64 { return textWidth(words) }
