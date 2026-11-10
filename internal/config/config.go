package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/charlesrobsampson/shh/internal/jsonc"
)

// Kind is the sort of alert being shown; kinds can override the base style.
type Kind string

const (
	KindMessage  Kind = "message"
	KindCalendar Kind = "calendar"
)

// Style is a fully resolved banner style.
type Style struct {
	Position string
	Size     string
	// Color is the banner's background and TextColor its text, each a code from the 8-bit ANSI palette, 0 to 255 (see ANSIHex).
	Color     int
	TextColor int
	// ButtonColor and ButtonTextColor style the banner's buttons the same way; NoColor (the default) leaves them as the system draws them.
	ButtonColor     int
	ButtonTextColor int
	DurationSecs    int // 0 (the default) means until dismissed: alerts are the important ones, so they stay until acted on
	Sound           string
}

// StyleOverride changes only the fields that are set.
type StyleOverride struct {
	Position  *string `json:"position,omitempty"`
	Size      *string `json:"size,omitempty"`
	Color     *int    `json:"color,omitempty"`
	TextColor *int    `json:"text_color,omitempty"`
	// ButtonColor is the buttons' background and ButtonTextColor their text.
	ButtonColor     *int    `json:"button_color,omitempty"`
	ButtonTextColor *int    `json:"button_text_color,omitempty"`
	DurationSecs    *int    `json:"duration_secs,omitempty"`
	Sound           *string `json:"sound,omitempty"`
	// Buttons replaces the whole button list when set; it is not merged with the list above it.
	Buttons []ButtonEntry `json:"buttons,omitempty"`
}

func (o StyleOverride) isZero() bool {
	return o.Position == nil && o.Size == nil && o.Color == nil && o.TextColor == nil && o.ButtonColor == nil && o.ButtonTextColor == nil && o.DurationSecs == nil && o.Sound == nil && o.Buttons == nil
}

type Alert struct {
	Base     Style
	Buttons  []ButtonEntry // the base button list as written; nil means the defaults
	Calendar StyleOverride
	Source   map[string]StyleOverride
}

// Resolve layers the base style, then the kind override, then the per-source override.
func (a Alert) Resolve(kind Kind, source string) Style { return a.ResolveWith(kind, source) }

// ResolveWith is Resolve with more overrides laid over the result, in order: a routine's, then one of its steps', so what is more
// specific wins.
func (a Alert) ResolveWith(kind Kind, source string, overrides ...StyleOverride) Style {
	style := a.Base
	if kind == KindCalendar {
		style = style.with(a.Calendar)
	}
	if override, ok := a.Source[source]; ok {
		style = style.with(override)
	}
	for _, o := range overrides {
		style = style.with(o)
	}
	return style
}

// Merge returns the override with another laid over it: what b sets wins, and the rest is a's.
func (a StyleOverride) Merge(b StyleOverride) StyleOverride {
	if b.Position != nil {
		a.Position = b.Position
	}
	if b.Size != nil {
		a.Size = b.Size
	}
	if b.Color != nil {
		a.Color = b.Color
	}
	if b.TextColor != nil {
		a.TextColor = b.TextColor
	}
	if b.ButtonColor != nil {
		a.ButtonColor = b.ButtonColor
	}
	if b.ButtonTextColor != nil {
		a.ButtonTextColor = b.ButtonTextColor
	}
	if b.DurationSecs != nil {
		a.DurationSecs = b.DurationSecs
	}
	if b.Sound != nil {
		a.Sound = b.Sound
	}
	if b.Buttons != nil {
		a.Buttons = b.Buttons
	}
	return a
}

// IsZero says whether the override changes nothing.
func (a StyleOverride) IsZero() bool { return a.isZero() }

func (s Style) with(o StyleOverride) Style {
	if o.Position != nil {
		s.Position = *o.Position
	}
	if o.Size != nil {
		s.Size = *o.Size
	}
	if o.Color != nil {
		s.Color = *o.Color
	}
	if o.TextColor != nil {
		s.TextColor = *o.TextColor
	}
	if o.ButtonColor != nil {
		s.ButtonColor = *o.ButtonColor
	}
	if o.ButtonTextColor != nil {
		s.ButtonTextColor = *o.ButtonTextColor
	}
	if o.DurationSecs != nil {
		s.DurationSecs = *o.DurationSecs
	}
	if o.Sound != nil {
		s.Sound = *o.Sound
	}
	return s
}

type Menubar struct {
	// Enabled says whether the worker shows the status item in the menu bar.
	Enabled bool
	Label   string
}

type Summarizer struct {
	Backend string
}

type Paths struct {
	Dir    string
	DB     string
	Socket string
	PID    string
}

type Config struct {
	Alert      Alert
	Menubar    Menubar
	Summarizer Summarizer
	Paths      Paths
}

// file is the shape of config.json; pointers tell "not set" apart from zero values.
type file struct {
	Alert struct {
		StyleOverride
		Calendar *StyleOverride           `json:"calendar,omitempty"`
		Source   map[string]StyleOverride `json:"source,omitempty"`
	} `json:"alert"`
	Menubar struct {
		Enabled *bool   `json:"enabled,omitempty"`
		Label   *string `json:"label,omitempty"`
	} `json:"menubar"`
	Summarizer struct {
		Backend *string `json:"backend,omitempty"`
	} `json:"summarizer"`
	Paths struct {
		Dir    *string `json:"dir,omitempty"`
		DB     *string `json:"db,omitempty"`
		Socket *string `json:"socket,omitempty"`
		PID    *string `json:"pid,omitempty"`
	} `json:"paths"`
}

var (
	positions = []string{"top-left", "top-center", "top-right", "bottom-left", "bottom-center", "bottom-right"}
	sizes     = []string{"small", "medium", "large"}
	backends  = []string{"none", "ollama", "claude"}
)

// DefaultPath is ~/.config/shh/config.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "shh", "config.json"), nil
}

// Load reads the config at path over the defaults. A missing file means all defaults.
// Relative paths in the file are resolved against the directory it names, which defaults to the file's own directory.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw = nil
	} else if err != nil {
		return Config{}, err
	}
	var f file
	if err := jsonc.Decode(path, raw, &f); err != nil {
		return Config{}, ExplainColorError(err)
	}
	cfg, err := build(f, filepath.Dir(path))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func build(f file, configDir string) (Config, error) {
	cfg := Config{
		Alert:      Alert{Base: Style{Position: "top-right", Size: "medium", Color: DefaultColor, TextColor: DefaultTextColor, ButtonColor: NoColor, ButtonTextColor: NoColor, DurationSecs: 0, Sound: "Glass"}},
		Menubar:    Menubar{Enabled: true, Label: "shh"},
		Summarizer: Summarizer{Backend: "none"},
	}
	if err := validateOverride("alert", f.Alert.StyleOverride); err != nil {
		return Config{}, err
	}
	cfg.Alert.Base = cfg.Alert.Base.with(f.Alert.StyleOverride)
	cfg.Alert.Buttons = f.Alert.Buttons
	if f.Alert.Calendar != nil {
		if err := validateOverride("alert.calendar", *f.Alert.Calendar); err != nil {
			return Config{}, err
		}
		cfg.Alert.Calendar = *f.Alert.Calendar
	}
	for source, override := range f.Alert.Source {
		if err := validateOverride("alert.source."+source, override); err != nil {
			return Config{}, err
		}
	}
	cfg.Alert.Source = f.Alert.Source

	if f.Menubar.Enabled != nil {
		cfg.Menubar.Enabled = *f.Menubar.Enabled
	}
	if f.Menubar.Label != nil {
		cfg.Menubar.Label = *f.Menubar.Label
	}
	if f.Summarizer.Backend != nil {
		if !slices.Contains(backends, *f.Summarizer.Backend) {
			return Config{}, notOneOf("summarizer.backend", *f.Summarizer.Backend, backends)
		}
		cfg.Summarizer.Backend = *f.Summarizer.Backend
	}

	paths, err := buildPaths(f, configDir)
	if err != nil {
		return Config{}, err
	}
	cfg.Paths = paths
	return cfg, nil
}

func buildPaths(f file, configDir string) (Paths, error) {
	for key, value := range map[string]*string{"paths.dir": f.Paths.Dir, "paths.db": f.Paths.DB, "paths.socket": f.Paths.Socket, "paths.pid": f.Paths.PID} {
		if value != nil && *value == "" {
			return Paths{}, fmt.Errorf("%q must not be empty", key)
		}
	}
	dir := configDir
	if f.Paths.Dir != nil {
		dir = *f.Paths.Dir
	}
	resolve := func(override *string, name string) string {
		if override == nil {
			return filepath.Join(dir, name)
		}
		if filepath.IsAbs(*override) {
			return *override
		}
		return filepath.Join(dir, *override)
	}
	return Paths{
		Dir:    dir,
		DB:     resolve(f.Paths.DB, "shh.db"),
		Socket: resolve(f.Paths.Socket, "shh.sock"),
		PID:    resolve(f.Paths.PID, "shh.pid"),
	}, nil
}

// ValidateOverride checks the settings of a style override, naming prefix in a problem. Routines and their steps use it for theirs.
func ValidateOverride(prefix string, o StyleOverride) error { return validateOverride(prefix, o) }

func validateOverride(prefix string, o StyleOverride) error {
	switch {
	case o.Position != nil && !slices.Contains(positions, *o.Position):
		return notOneOf(prefix+".position", *o.Position, positions)
	case o.Size != nil && !slices.Contains(sizes, *o.Size):
		return notOneOf(prefix+".size", *o.Size, sizes)
	case o.Color != nil && (*o.Color < 0 || *o.Color > 255):
		return fmt.Errorf("%q: %d is not a color code from 0 to 255 (the 8-bit ANSI palette: %s; shh alert colors shows them)", prefix+".color", *o.Color, ColorChartURL)
	case o.TextColor != nil && (*o.TextColor < 0 || *o.TextColor > 255):
		return fmt.Errorf("%q: %d is not a color code from 0 to 255 (the 8-bit ANSI palette: %s; shh alert colors shows them)", prefix+".text_color", *o.TextColor, ColorChartURL)
	case o.ButtonColor != nil && (*o.ButtonColor < 0 || *o.ButtonColor > 255):
		return fmt.Errorf("%q: %d is not a color code from 0 to 255 (the 8-bit ANSI palette: %s; shh alert colors shows them)", prefix+".button_color", *o.ButtonColor, ColorChartURL)
	case o.ButtonTextColor != nil && (*o.ButtonTextColor < 0 || *o.ButtonTextColor > 255):
		return fmt.Errorf("%q: %d is not a color code from 0 to 255 (the 8-bit ANSI palette: %s; shh alert colors shows them)", prefix+".button_text_color", *o.ButtonTextColor, ColorChartURL)
	case o.DurationSecs != nil && *o.DurationSecs < 0:
		return fmt.Errorf("%q: %d is negative (use 0 for until dismissed)", prefix+".duration_secs", *o.DurationSecs)
	case o.Sound != nil && *o.Sound == "":
		return fmt.Errorf("%q must not be empty (use \"none\" for silence)", prefix+".sound")
	}
	return validateButtons(prefix, o.Buttons)
}

func notOneOf(key, got string, allowed []string) error {
	return fmt.Errorf("%q: %q is not one of %s", key, got, strings.Join(allowed, ", "))
}

// Effective renders the resolved configuration as JSON that Load reads back to the same values.
func (c Config) Effective() ([]byte, error) {
	var f file
	base := c.Alert.Base
	f.Alert.Position, f.Alert.Size, f.Alert.Color, f.Alert.TextColor, f.Alert.DurationSecs, f.Alert.Sound =
		&base.Position, &base.Size, &base.Color, &base.TextColor, &base.DurationSecs, &base.Sound
	if base.ButtonColor != NoColor { // left out when the buttons are the system's
		f.Alert.ButtonColor = &base.ButtonColor
	}
	if base.ButtonTextColor != NoColor {
		f.Alert.ButtonTextColor = &base.ButtonTextColor
	}
	f.Alert.Buttons = entries(normalize(c.Alert.Buttons))
	if !c.Alert.Calendar.isZero() {
		calendar := c.Alert.Calendar
		if calendar.Buttons != nil {
			calendar.Buttons = entries(normalize(calendar.Buttons))
		}
		f.Alert.Calendar = &calendar
	}
	if c.Alert.Source != nil {
		f.Alert.Source = make(map[string]StyleOverride, len(c.Alert.Source))
		for name, override := range c.Alert.Source {
			if override.Buttons != nil {
				override.Buttons = entries(normalize(override.Buttons))
			}
			f.Alert.Source[name] = override
		}
	}
	f.Menubar.Enabled, f.Menubar.Label = &c.Menubar.Enabled, &c.Menubar.Label
	f.Summarizer.Backend = &c.Summarizer.Backend
	f.Paths.Dir, f.Paths.DB, f.Paths.Socket, f.Paths.PID = &c.Paths.Dir, &c.Paths.DB, &c.Paths.Socket, &c.Paths.PID
	out, err := json.MarshalIndent(f, "", "  ")
	return append(out, '\n'), err
}
