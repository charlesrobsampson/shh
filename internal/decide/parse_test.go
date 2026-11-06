package decide_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/decide"
)

func TestParseThreshold(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{input: "0%", want: 0},
		{input: "1%", want: 1},
		{input: "35%", want: 35},
		{input: "65%", want: 65},
		{input: "98%", want: 98},
		{input: "99%", want: 99},
		{input: "100%", want: 99}, // alias for the maximum level
		{input: " 75% ", want: 75},
		{input: "007%", want: 7},
		{input: "off", want: 0},
		{input: "light", want: 35},
		{input: "medium", want: 65},
		{input: "deep", want: 90},
		{input: "lockdown", want: 99},
		{input: "LockDown", want: 99},
		{input: " Medium ", want: 65},

		{input: "101%", wantErr: true},
		{input: "200%", wantErr: true},
		{input: "-5%", wantErr: true},
		{input: "+5%", wantErr: true},
		{input: "75.5%", wantErr: true},
		{input: "0.75", wantErr: true},
		{input: "0.75%", wantErr: true},
		{input: "75", wantErr: true}, // a bare number is ambiguous; write 75%
		{input: "0", wantErr: true},
		{input: "abc", wantErr: true},
		{input: "", wantErr: true},
		{input: "%", wantErr: true},
		{input: "medium%", wantErr: true},
		{input: "1h30m", wantErr: true},
		{input: "99999999999999999999%", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := decide.ParseThreshold(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseThreshold(%q) = %d, want an error", tt.input, got)
				}
				if !strings.Contains(err.Error(), `"`+strings.TrimSpace(tt.input)+`"`) && tt.input != "" {
					t.Errorf("error %q should quote the input %q", err, tt.input)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseThreshold(%q) = %d, %v; want %d", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestParseLabel(t *testing.T) {
	tests := []struct {
		input   string
		want    int
		wantErr bool
	}{
		{input: "0", want: 0},
		{input: "35", want: 35},
		{input: "80", want: 80},
		{input: "99", want: 99},
		{input: "100", want: 100}, // never muted, unlike thresholds which stop at 99
		{input: "80%", want: 80},
		{input: "100%", want: 100},
		{input: " 42 ", want: 42},
		{input: "off", want: 0},
		{input: "light", want: 35},
		{input: "Medium", want: 65},
		{input: "deep", want: 90},
		{input: "lockdown", want: 99},

		{input: "101", wantErr: true},
		{input: "101%", wantErr: true},
		{input: "-1", wantErr: true},
		{input: "8.5", wantErr: true},
		{input: "0.8", wantErr: true},
		{input: "abc", wantErr: true},
		{input: "", wantErr: true},
		{input: "25m", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := decide.ParseLabel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLabel(%q) = %d, want an error", tt.input, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseLabel(%q) = %d, %v; want %d", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{input: "25m", want: 25 * time.Minute},
		{input: "50m", want: 50 * time.Minute},
		{input: "1h", want: time.Hour},
		{input: "1h30m", want: 90 * time.Minute},
		{input: "2h45m30s", want: 2*time.Hour + 45*time.Minute + 30*time.Second},
		{input: "90s", want: 90 * time.Second},
		{input: "1.5h", want: 90 * time.Minute},
		{input: " 10m ", want: 10 * time.Minute},
		{input: "0", want: 0}, // indefinite
		{input: "0m", want: 0},

		{input: "-5m", wantErr: true},
		{input: "25", wantErr: true}, // minutes or seconds? write the unit
		{input: "500ms", wantErr: true},
		{input: "abc", wantErr: true},
		{input: "", wantErr: true},
		{input: "75%", wantErr: true},
		{input: "1d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := decide.ParseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseDuration(%q) = %v, want an error", tt.input, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ParseDuration(%q) = %v, %v; want %v", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestErrorsExplainWhatToWrite(t *testing.T) {
	tests := []struct {
		name  string
		err   func() error
		wants []string
	}{
		{"bare threshold number", func() error { _, err := decide.ParseThreshold("75"); return err }, []string{"75%"}},
		{"unknown tier lists the valid ones", func() error { _, err := decide.ParseThreshold("extreme"); return err },
			[]string{"off", "light", "medium", "deep", "lockdown"}},
		{"decimal threshold", func() error { _, err := decide.ParseThreshold("0.75"); return err }, []string{"whole"}},
		{"bare duration number", func() error { _, err := decide.ParseDuration("25"); return err }, []string{"25m"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.err()
			if err == nil {
				t.Fatal("no error")
			}
			for _, want := range tt.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q should mention %q", err, want)
				}
			}
		})
	}
}
