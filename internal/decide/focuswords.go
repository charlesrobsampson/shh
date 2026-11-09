package decide

import (
	"fmt"
	"regexp"
)

var (
	percentWord  = regexp.MustCompile(`^[0-9]+%$`)
	durationWord = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?[hms])+$`)
)

// IsPercentWord is true for a level written as a percentage, such as "30%".
func IsPercentWord(w string) bool { return percentWord.MatchString(w) }

// IsDurationWord is true for a length such as "25m" or "1h30m".
func IsDurationWord(w string) bool { return durationWord.MatchString(w) }

// IsTierWord is true for a tier name, "off" included.
func IsTierWord(w string) bool {
	_, err := ParseThreshold(w)
	return err == nil && !percentWord.MatchString(w)
}

func isSettingWord(w string) bool { return IsPercentWord(w) || IsDurationWord(w) || IsTierWord(w) }

// SplitFocusWords takes a level and a length off the front of the plain words of a focus command, so "30%" or "25m deep work"
// is read as settings and not as a note. level and length are what was already given another way (a flag, a separate field); a
// setting given both ways is an error rather than a choice. A lone tier name ("deep") is a level too, and so is one followed by
// another setting ("deep 5m"), but "deep work" is a note. The words left over are the note.
func SplitFocusWords(level, length string, words []string) (levelOut, lengthOut string, rest []string, err error) {
	for len(words) > 0 {
		w := words[0]
		switch {
		case IsPercentWord(w):
			if level != "" {
				return "", "", nil, fmt.Errorf("the level is given twice (%s and %s)", level, w)
			}
			level = w
		case IsDurationWord(w):
			if length != "" {
				return "", "", nil, fmt.Errorf("the length is given twice (%s and %s)", length, w)
			}
			length = w
		case level == "" && IsTierWord(w) && (len(words) == 1 || isSettingWord(words[1])):
			level = w
		default:
			return level, length, words, nil
		}
		words = words[1:]
	}
	return level, length, words, nil
}
