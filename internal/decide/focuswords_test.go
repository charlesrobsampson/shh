package decide_test

import (
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/decide"
)

func TestSplitFocusWords(t *testing.T) {
	tests := []struct {
		name        string
		level, len  string
		words       []string
		wantLevel   string
		wantLength  string
		wantNote    string
		wantErrPart string
	}{
		{"nothing", "", "", nil, "", "", "", ""},
		{"a percentage", "", "", []string{"30%"}, "30%", "", "", ""},
		{"a length", "", "", []string{"25m"}, "", "25m", "", ""},
		{"either order, then a note", "", "", []string{"25m", "75%", "deep", "work"}, "75%", "25m", "deep work", ""},
		{"a lone tier", "", "", []string{"deep"}, "deep", "", "", ""},
		{"a tier then a length", "", "", []string{"deep", "5m"}, "deep", "5m", "", ""},
		{"a tier then words is a note", "", "", []string{"deep", "work"}, "", "", "deep work", ""},
		{"settings only count at the front", "", "", []string{"meeting", "at", "30%"}, "", "", "meeting at 30%", ""},
		{"off for a while", "", "", []string{"off", "25m"}, "off", "25m", "", ""},
		{"a level already given and a note", "40%", "", []string{"25m", "write"}, "40%", "25m", "write", ""},
		{"the level twice", "40%", "", []string{"30%"}, "", "", "", "level is given twice"},
		{"the length twice", "", "10m", []string{"25m"}, "", "", "", "length is given twice"},
		{"a level twice in the words", "", "", []string{"30%", "40%"}, "", "", "", "level is given twice"},
		{"a given level stops a tier word being read as one", "40%", "", []string{"deep"}, "40%", "", "deep", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, length, rest, err := decide.SplitFocusWords(tt.level, tt.len, tt.words)
			if tt.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrPart) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErrPart)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if level != tt.wantLevel || length != tt.wantLength || strings.Join(rest, " ") != tt.wantNote {
				t.Errorf("got level %q length %q note %q, want %q %q %q", level, length, strings.Join(rest, " "), tt.wantLevel, tt.wantLength, tt.wantNote)
			}
		})
	}
}

func TestWordClassifiers(t *testing.T) {
	for w, want := range map[string]bool{"30%": true, "30": false, "3.5%": false, "%": false, "deep": false} {
		if decide.IsPercentWord(w) != want {
			t.Errorf("IsPercentWord(%q) = %v", w, !want)
		}
	}
	for w, want := range map[string]bool{"25m": true, "1h30m": true, "1.5h": true, "25": false, "m": false, "25 m": false} {
		if decide.IsDurationWord(w) != want {
			t.Errorf("IsDurationWord(%q) = %v", w, !want)
		}
	}
	for w, want := range map[string]bool{"off": true, "light": true, "lockdown": true, "30%": false, "work": false} {
		if decide.IsTierWord(w) != want {
			t.Errorf("IsTierWord(%q) = %v", w, !want)
		}
	}
}
