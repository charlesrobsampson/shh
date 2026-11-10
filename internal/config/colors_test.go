package config_test

import (
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
)

func TestANSIHexFollowsThe8BitPalette(t *testing.T) {
	for code, want := range map[int]string{
		0: "#000000", 1: "#800000", 7: "#c0c0c0", 8: "#808080", 9: "#ff0000", 10: "#00ff00", 15: "#ffffff", // the sixteen
		16: "#000000", 17: "#00005f", 21: "#0000ff", 46: "#00ff00", 196: "#ff0000", 208: "#ff8700", 214: "#ffaf00", 231: "#ffffff", // the cube
		232: "#080808", 244: "#808080", 255: "#eeeeee", // the greys
	} {
		if got := config.ANSIHex(code); got != want {
			t.Errorf("ANSIHex(%d) = %s, want %s", code, got, want)
		}
	}
	if config.ANSIHex(-5) != "#000000" || config.ANSIHex(999) != "#eeeeee" {
		t.Error("a code outside the palette is taken as the nearest end")
	}
	if config.DefaultColor != 208 {
		t.Errorf("default = %d", config.DefaultColor)
	}
}

func intp(n int) *int       { return &n }
func strp(s string) *string { return &s }

func TestOverridesLayerInOrderAndTheMoreSpecificWins(t *testing.T) {
	a := config.Alert{Base: config.Style{Position: "top-right", Size: "medium", Color: 208, Sound: "Glass"}}
	routineLevel := config.StyleOverride{Color: intp(33), Size: strp("large")}
	stepLevel := config.StyleOverride{Color: intp(196)}
	got := a.ResolveWith(config.KindMessage, "Routine", routineLevel, stepLevel)
	want := config.Style{Position: "top-right", Size: "large", Color: 196, Sound: "Glass"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if plain := a.ResolveWith(config.KindMessage, "Routine"); plain != a.Base {
		t.Errorf("no overrides is the base: %+v", plain)
	}
	// a source override from the config sits under the routine's
	a.Source = map[string]config.StyleOverride{"Routine": {Color: intp(41), Sound: strp("Hero")}}
	got = a.ResolveWith(config.KindMessage, "Routine", routineLevel)
	if got.Color != 33 || got.Sound != "Hero" {
		t.Errorf("the routine's color beats the source's, the source's sound stays: %+v", got)
	}
}

func TestMergeLaysOneOverrideOverAnother(t *testing.T) {
	a := config.StyleOverride{Color: intp(33), Position: strp("top-left")}
	b := config.StyleOverride{Color: intp(196), Sound: strp("Hero")}
	m := a.Merge(b)
	if *m.Color != 196 || *m.Position != "top-left" || *m.Sound != "Hero" || m.Size != nil {
		t.Errorf("merged = %+v", m)
	}
	if *a.Color != 33 {
		t.Error("merging must not change the original")
	}
	if !(config.StyleOverride{}).IsZero() || m.IsZero() {
		t.Error("IsZero")
	}
}

func TestValidateOverrideNamesTheKeyAndPointsAtThePalette(t *testing.T) {
	err := config.ValidateOverride("routines.x.style", config.StyleOverride{Color: intp(300)})
	if err == nil || !contains(err.Error(), "routines.x.style.color") || !contains(err.Error(), config.ColorChartURL) {
		t.Errorf("err = %v", err)
	}
	if err := config.ValidateOverride("s", config.StyleOverride{Size: strp("huge")}); err == nil || !contains(err.Error(), "s.size") {
		t.Errorf("err = %v", err)
	}
	if err := config.ValidateOverride("s", config.StyleOverride{Color: intp(0), Position: strp("bottom-center"), DurationSecs: intp(0)}); err != nil {
		t.Errorf("a good override: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAHexColorLeftInAFileSaysWhatAColorIsNow(t *testing.T) {
	path := write(t, `{"alert": {"color": "#ff9f0a"}}`)
	_, err := config.Load(path)
	if err == nil || !contains(err.Error(), "alert.color") || !contains(err.Error(), "not a #rrggbb code") || !contains(err.Error(), config.ColorChartURL) {
		t.Errorf("err = %v", err)
	}
	other := write(t, `{"alert": {"size": 3}}`)
	if _, err := config.Load(other); err == nil || contains(err.Error(), "8-bit") {
		t.Errorf("another error must be left alone: %v", err)
	}
}

func TestTextColorIsItsOwnSettingThatCascadesLikeTheRest(t *testing.T) {
	path := write(t, `{"alert": {"color": 233, "text_color": 214, "calendar": {"text_color": 0}, "source": {"Slack": {"color": 15, "text_color": 16}}}}`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Alert.Resolve(config.KindMessage, "Teams"); got.Color != 233 || got.TextColor != 214 {
		t.Errorf("base: %+v", got)
	}
	if got := cfg.Alert.Resolve(config.KindCalendar, ""); got.Color != 233 || got.TextColor != 0 {
		t.Errorf("a kind can change only the text: %+v", got)
	}
	if got := cfg.Alert.Resolve(config.KindMessage, "Slack"); got.Color != 15 || got.TextColor != 16 {
		t.Errorf("a source: %+v", got)
	}
	routine := config.StyleOverride{TextColor: intp(46)}
	if got := cfg.Alert.ResolveWith(config.KindMessage, "Teams", routine); got.Color != 233 || got.TextColor != 46 {
		t.Errorf("a routine can change only the text: %+v", got)
	}
	if def, _ := config.Load(write(t, `{}`)); def.Alert.Base.TextColor != config.DefaultTextColor || config.DefaultTextColor != 15 {
		t.Errorf("the text is white unless said otherwise: %+v", def.Alert.Base)
	}
	shown, err := cfg.Effective()
	if err != nil || !contains(string(shown), `"text_color": 214`) {
		t.Errorf("effective: %v\n%s", err, shown)
	}
	for text, key := range map[string]string{`{"alert": {"text_color": 256}}`: "alert.text_color", `{"alert": {"text_color": -1}}`: "alert.text_color", `{"alert": {"source": {"X": {"text_color": 999}}}}`: "alert.source.X.text_color"} {
		if _, err := config.Load(write(t, text)); err == nil || !contains(err.Error(), key) {
			t.Errorf("%s: %v", text, err)
		}
	}
	m := (config.StyleOverride{Color: intp(1)}).Merge(config.StyleOverride{TextColor: intp(2)})
	if *m.Color != 1 || *m.TextColor != 2 {
		t.Errorf("merge: %+v", m)
	}
}
