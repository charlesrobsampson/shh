package config

import (
	"fmt"
	"slices"
)

// Button type names. Adding a button type means adding it here and to buttonSpecs, then handling it in the alert package.
const (
	ButtonPreview = "preview"
	ButtonGoto    = "goto"
	ButtonDismiss = "dismiss"
	ButtonTrain   = "train"
)

// buttonSpecs is the registry of known button types, in their default left-to-right order.
type buttonSpec struct {
	Type    string
	Label   string
	Enabled bool
}

var buttonSpecs = []buttonSpec{
	{ButtonPreview, "Preview", true},
	{ButtonGoto, "Go to", true},
	{ButtonDismiss, "Dismiss", true},
	{ButtonTrain, "Train", false}, // reserved until task 45
}

// ButtonEntry is one button as written in config.json. Label and Enabled fall back to the defaults when left out; an entry that is
// listed is enabled unless it says otherwise.
type ButtonEntry struct {
	Type    string  `json:"type"`
	Label   *string `json:"label,omitempty"`
	Enabled *bool   `json:"enabled,omitempty"`
	// CollapseLabel (preview only) is what the button says while the banner is expanded; pressing it folds the banner back up.
	CollapseLabel *string `json:"collapse_label,omitempty"`
}

// Button is a resolved button.
type Button struct {
	Type    string
	Label   string
	Enabled bool
	// CollapseLabel is only set for preview.
	CollapseLabel string
}

// ButtonsFor is the full button list for an alert, left to right: the per-source list if there is one, else the per-kind list, else
// the base list, else the defaults. Every known type is present; a type the list leaves out comes back disabled, after the listed ones.
func (a Alert) ButtonsFor(kind Kind, source string) []Button {
	entries := a.Buttons
	if kind == KindCalendar && a.Calendar.Buttons != nil {
		entries = a.Calendar.Buttons
	}
	if override, ok := a.Source[source]; ok && override.Buttons != nil {
		entries = override.Buttons
	}
	return normalize(entries)
}

func normalize(entries []ButtonEntry) []Button {
	if entries == nil {
		out := make([]Button, 0, len(buttonSpecs))
		for _, s := range buttonSpecs {
			out = append(out, Button{s.Type, s.Label, s.Enabled, defaultCollapse(s.Type)})
		}
		return out
	}
	out := make([]Button, 0, len(buttonSpecs))
	seen := map[string]bool{}
	for _, e := range entries {
		spec := buttonSpecs[specIndex(e.Type)]
		b := Button{Type: spec.Type, Label: spec.Label, Enabled: true, CollapseLabel: defaultCollapse(spec.Type)}
		if e.CollapseLabel != nil {
			b.CollapseLabel = *e.CollapseLabel
		}
		if e.Label != nil {
			b.Label = *e.Label
		}
		if e.Enabled != nil {
			b.Enabled = *e.Enabled
		}
		seen[e.Type] = true
		out = append(out, b)
	}
	for _, s := range buttonSpecs {
		if !seen[s.Type] {
			out = append(out, Button{s.Type, s.Label, false, defaultCollapse(s.Type)})
		}
	}
	return out
}

func defaultCollapse(buttonType string) string {
	if buttonType == ButtonPreview {
		return "Collapse"
	}
	return ""
}

func specIndex(t string) int {
	return slices.IndexFunc(buttonSpecs, func(s buttonSpec) bool { return s.Type == t })
}

func entries(buttons []Button) []ButtonEntry {
	out := make([]ButtonEntry, 0, len(buttons))
	for _, b := range buttons {
		label, enabled := b.Label, b.Enabled
		entry := ButtonEntry{Type: b.Type, Label: &label, Enabled: &enabled}
		if b.CollapseLabel != "" {
			collapse := b.CollapseLabel
			entry.CollapseLabel = &collapse
		}
		out = append(out, entry)
	}
	return out
}

func validateButtons(prefix string, list []ButtonEntry) error {
	seen := map[string]bool{}
	for i, e := range list {
		key := fmt.Sprintf("%s.buttons[%d]", prefix, i)
		switch {
		case e.Type == "" || specIndex(e.Type) < 0:
			known := make([]string, 0, len(buttonSpecs))
			for _, s := range buttonSpecs {
				known = append(known, s.Type)
			}
			return notOneOf(key+".type", e.Type, known)
		case seen[e.Type]:
			return fmt.Errorf("%q: %q is listed twice", key+".type", e.Type)
		case e.Label != nil && *e.Label == "":
			return fmt.Errorf("%q must not be empty (leave it out to use the default label)", key+".label")
		case e.CollapseLabel != nil && e.Type != ButtonPreview:
			return fmt.Errorf("%q: only the preview button has a collapse label", key+".collapse_label")
		case e.CollapseLabel != nil && *e.CollapseLabel == "":
			return fmt.Errorf("%q must not be empty (leave it out to use the default label)", key+".collapse_label")
		}
		seen[e.Type] = true
	}
	return nil
}
