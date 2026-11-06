package jsonc_test

import (
	"strings"
	"testing"

	"github.com/charlesrobsampson/shh/internal/jsonc"
)

type style struct {
	Color    *string `json:"color"`
	Duration *int    `json:"duration_secs"`
}

type rule struct {
	Name  string  `json:"name"`
	Boost float64 `json:"boost"`
}

type doc struct {
	Alert struct {
		style                     // embedded: its keys sit beside the others
		Position *string          `json:"position"`
		Calendar *style           `json:"calendar"`
		Source   map[string]style `json:"source"`
	} `json:"alert"`
	Urgency []rule `json:"urgency"`
	Menubar struct {
		Label *string `json:"label"`
	} `json:"menubar"`
}

func decode(t *testing.T, src string) (doc, error) {
	t.Helper()
	var d doc
	err := jsonc.Decode("config.json", []byte(src), &d)
	return d, err
}

func TestPlainJSONDecodes(t *testing.T) {
	d, err := decode(t, `{"alert": {"color": "#fff", "position": "top-left", "calendar": {"duration_secs": 3}, "source": {"Slack": {"color": "#000"}}},
		"urgency": [{"name": "x", "boost": 0.2}], "menubar": {"label": "focus"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if *d.Alert.Color != "#fff" || *d.Alert.Position != "top-left" || *d.Alert.Calendar.Duration != 3 || *d.Alert.Source["Slack"].Color != "#000" ||
		d.Urgency[0].Name != "x" || d.Urgency[0].Boost != 0.2 || *d.Menubar.Label != "focus" {
		t.Errorf("decoded = %+v", d)
	}
}

func TestCommentsAndTrailingCommasAreAllowed(t *testing.T) {
	d, err := decode(t, `
// Alert banners
{
  "alert": {
    /* the banner colour */
    "color": "#30d158", // green
    "position": "bottom-left",
  },
  "urgency": [
    {"name": "x", "boost": 0.2,}, // trailing comma above and below
  ],
}
`)
	if err != nil {
		t.Fatal(err)
	}
	if *d.Alert.Color != "#30d158" || *d.Alert.Position != "bottom-left" || len(d.Urgency) != 1 {
		t.Errorf("decoded = %+v", d)
	}
}

func TestABlankFileIsAnEmptyObject(t *testing.T) {
	for _, src := range []string{"", "  \n\t", "// only a comment\n", "/* nothing */"} {
		if _, err := decode(t, src); err != nil {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestUnknownKeysNameTheirDottedPathAndLine(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantKey string
		line    int
	}{
		{"a top-level key", "{\n  \"theme\": 1\n}", "theme", 2},
		{"a nested key", "{\n  \"alert\": {\n    \"colour\": \"x\"\n  }\n}", "alert.colour", 3},
		{"a key in a map value", "{\"alert\": {\n\"source\": {\"Slack\": {\n\"volume\": 3}}}}", "alert.source.Slack.volume", 3},
		{"a key in an array element", "{\"urgency\": [\n  {\"name\": \"a\"},\n  {\"name\": \"b\", \"weight\": 3}\n]}", "urgency.weight", 3},
		{"a key beside an embedded struct's keys", "{\"alert\": {\"color\": \"x\",\n\"bogus\": 1}}", "alert.bogus", 2},
		{"a wrong-case key", "{\"alert\": {\n\"Color\": \"x\"}}", "alert.Color", 2},
		{"after comments", "// a\n// b\n{\n// c\n\"menubar\": {\"lable\": \"x\"}}", "menubar.lable", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode(t, tt.src)
			if err == nil {
				t.Fatal("decode succeeded")
			}
			msg := err.Error()
			if !strings.Contains(msg, "unknown key") || !strings.Contains(msg, `"`+tt.wantKey+`"`) {
				t.Errorf("error %q should say unknown key %q", msg, tt.wantKey)
			}
			if want := "config.json:" + string(rune('0'+tt.line)) + ":"; !strings.HasPrefix(msg, want) {
				t.Errorf("error %q should start with %q", msg, want)
			}
		})
	}
}

func TestWrongTypesNameTheKeyAndLine(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantKey string
		line    int
	}{
		{"a string where a number goes", "{\"alert\": {\n\"duration_secs\": \"soon\"}}", "alert.duration_secs", 2},
		{"a number where a string goes", "{\"menubar\": {\n\"label\": 3}}", "menubar.label", 2},
		{"an object where a list goes", "{\n\"urgency\": {\"name\": \"x\"}}", "urgency", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode(t, tt.src)
			if err == nil {
				t.Fatal("decode succeeded")
			}
			msg := err.Error()
			if !strings.Contains(msg, `"`+tt.wantKey+`"`) || !strings.Contains(msg, "wrong type") {
				t.Errorf("error %q should say %q has the wrong type", msg, tt.wantKey)
			}
			if want := "config.json:" + string(rune('0'+tt.line)) + ":"; !strings.HasPrefix(msg, want) {
				t.Errorf("error %q should start with %q", msg, want)
			}
		})
	}
}

func TestSyntaxErrorsPointAtTheLine(t *testing.T) {
	tests := []struct {
		name string
		src  string
		line int
	}{
		{"a missing comma", "{\n\"alert\": {\"color\": \"x\"\n\"position\": \"y\"}}", 3},
		{"an unclosed brace", "{\n\"alert\": {\n\"color\": \"x\"\n", 4},
		{"a bare word", "{\n\"alert\": nope}", 2},
		{"a single-quoted string", "{'alert': 1}", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode(t, tt.src)
			if err == nil {
				t.Fatal("decode succeeded")
			}
			if want := "config.json:" + string(rune('0'+tt.line)) + ":"; !strings.HasPrefix(err.Error(), want) {
				t.Errorf("error %q should start with %q", err, want)
			}
		})
	}
}

func TestTheTopLevelMustBeAnObject(t *testing.T) {
	for _, src := range []string{"[]", "3", `"x"`, "null"} {
		if _, err := decode(t, src); err == nil {
			t.Errorf("%q was accepted", src)
		}
	}
}

func TestTrailingGarbageIsRejected(t *testing.T) {
	if _, err := decode(t, `{} {}`); err == nil {
		t.Error("two documents in one file were accepted")
	}
}

func TestNullsLeaveFieldsUnset(t *testing.T) {
	d, err := decode(t, `{"alert": {"color": null}}`)
	if err != nil || d.Alert.Color != nil {
		t.Fatalf("color = %v, %v; want null to mean not set", d.Alert.Color, err)
	}
}
