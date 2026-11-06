package classify

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// wordPiece is the BERT WordPiece tokenizer the potion models use: clean the text, space out CJK characters, lowercase, strip
// accents, split on whitespace and punctuation, then greedily match the longest known piece of each word.
type wordPiece struct {
	vocab    map[string]int
	unk      int
	maxChars int
}

// ids returns the vocabulary ids of the known pieces; words that cannot be tokenized are left out.
func (t *wordPiece) ids(text string) []int {
	var out []int
	for _, word := range bertWords(text) {
		for _, id := range t.pieces(word) {
			if id != t.unk {
				out = append(out, id)
			}
		}
	}
	return out
}

func (t *wordPiece) pieces(word string) []int {
	runes := []rune(word)
	if len(runes) > t.maxChars {
		return []int{t.unk}
	}
	var ids []int
	for start := 0; start < len(runes); {
		end, found := len(runes), -1
		for ; end > start; end-- {
			piece := string(runes[start:end])
			if start > 0 {
				piece = "##" + piece
			}
			if id, ok := t.vocab[piece]; ok {
				found = id
				break
			}
		}
		if found < 0 {
			return []int{t.unk}
		}
		ids = append(ids, found)
		start = end
	}
	return ids
}

// bertWords applies BERT's normalizer and pre-tokenizer.
func bertWords(text string) []string {
	var cleaned strings.Builder
	for _, r := range text {
		switch {
		case r == 0 || r == 0xfffd || (unicode.IsControl(r) && !unicode.IsSpace(r)):
		case unicode.IsSpace(r):
			cleaned.WriteRune(' ')
		case unicode.Is(unicode.Han, r):
			cleaned.WriteString(" " + string(r) + " ")
		default:
			cleaned.WriteRune(r)
		}
	}
	var stripped strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(cleaned.String())) {
		if !unicode.Is(unicode.Mn, r) {
			stripped.WriteRune(r)
		}
	}
	var words []string
	for _, field := range strings.Fields(stripped.String()) {
		var current []rune
		for _, r := range field {
			if isBertPunctuation(r) {
				if len(current) > 0 {
					words = append(words, string(current))
					current = nil
				}
				words = append(words, string(r))
			} else {
				current = append(current, r)
			}
		}
		if len(current) > 0 {
			words = append(words, string(current))
		}
	}
	return words
}

func isBertPunctuation(r rune) bool {
	return (r >= 33 && r <= 47) || (r >= 58 && r <= 64) || (r >= 91 && r <= 96) || (r >= 123 && r <= 126) || unicode.IsPunct(r)
}
