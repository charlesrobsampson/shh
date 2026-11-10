package routine

import (
	"bytes"
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

// ErrExists is returned by Save when a routine of that name is there and replace was not asked for.
var ErrExists = errors.New("a routine with that name already exists")

// ErrNotFound is returned by Remove when there is no routine of that name.
var ErrNotFound = errors.New("no saved routine has that name")

// Check says whether a routine could be saved: the same checks reading the file would make, so a routine that passes here will
// load. It returns the routine as it will read back.
func Check(r Routine) (Routine, error) {
	raw, err := marshalRoutine(r, "")
	if err != nil {
		return Routine{}, err
	}
	text := "{\"routines\": {" + string(raw) + "}}"
	got, err := parse("the routine", []byte(text), false)
	if err != nil {
		return Routine{}, err
	}
	return got[0], nil
}

// Save adds the routine to the routines file, or replaces the one of the same name (ignoring case) when replace is set. Only that
// routine's text changes: the rest of the file, comments and layout included, is left as it was. A new routine goes at the end. The
// file is checked after the change and nothing is written if it would not load.
func Save(path string, r Routine, replace bool) error {
	if _, err := Check(r); err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		raw = []byte("{\n  \"routines\": {\n  }\n}\n")
	} else if err != nil {
		return err
	}
	members, closeAt, found, err := jsonc.Members(path, raw, "routines")
	if err != nil {
		return err
	}
	if !found {
		if len(bytes.TrimSpace(raw)) == 0 || isOnlyComments(raw) {
			raw = append(bytes.TrimRight(raw, "\n"), []byte("\n{\n  \"routines\": {\n  }\n}\n")...)
			if members, closeAt, found, err = jsonc.Members(path, raw, "routines"); err != nil || !found {
				return fmt.Errorf("%s: cannot find where to put the routine", path)
			}
		} else {
			return fmt.Errorf("%s: there is no \"routines\" object to add to", path)
		}
	}
	indent := memberIndent(raw, members, closeAt)
	text, err := marshalRoutine(r, indent)
	if err != nil {
		return err
	}
	var out []byte
	if i := indexFold(members, r.Name); i >= 0 {
		if !replace {
			return fmt.Errorf("%w: %q (use replace to overwrite it)", ErrExists, members[i].Key)
		}
		out = append(append(append([]byte(nil), raw[:members[i].Start]...), text...), raw[members[i].End:]...)
	} else {
		out = insertMember(raw, members, closeAt, indent, text)
	}
	return write(path, out, r.Name)
}

// Remove deletes the routine of that name (ignoring case) from the file, leaving the rest as it was.
func Remove(path, name string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	if err != nil {
		return err
	}
	members, _, found, err := jsonc.Members(path, raw, "routines")
	if err != nil {
		return err
	}
	i := indexFold(members, name)
	if !found || i < 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	m := members[i]
	start, end := m.Start, m.End
	// Take the line the routine was on, with its comma: the one after it, or when it is the last, the one before.
	end = skipBlanks(raw, end)
	if end < len(raw) && raw[end] == ',' {
		end++
	} else if i > 0 {
		if prev := bytes.LastIndexByte(raw[members[i-1].End:start], ','); prev >= 0 {
			start = members[i-1].End + prev
		}
	}
	// When the routine is alone on its lines, take the lines whole; in a file written on one line, take just the routine.
	if ls, le := lineStart(raw, start), restOfLine(raw, end); strings.TrimSpace(string(raw[ls:start])) == "" && le != end {
		start, end = ls, le
		for start > 0 { // a // comment on the lines just above describes this routine, so it goes too
			prev := lineStart(raw, start-1)
			if !strings.HasPrefix(strings.TrimSpace(string(raw[prev:start])), "//") {
				break
			}
			start = prev
		}
	} else {
		end = skipBlanks(raw, end)
	}
	out := append(append([]byte(nil), raw[:start]...), raw[end:]...)
	return write(path, out, "")
}

// write checks that the new text loads (and has the routine in it, when one is named) and then replaces the file in one step.
func write(path string, text []byte, name string) error {
	got, err := Parse(path, text)
	if err != nil {
		return fmt.Errorf("not saved, because the file would no longer load: %w", err)
	}
	if name != "" {
		if _, ok := Find(got, name); !ok {
			return fmt.Errorf("not saved: the routine %q is not in the file after the change", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".routines-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func indexFold(members []jsonc.Member, name string) int {
	return slices.IndexFunc(members, func(m jsonc.Member) bool { return strings.EqualFold(m.Key, name) })
}

func isOnlyComments(raw []byte) bool {
	_, _, found, err := jsonc.Members("", raw)
	return err == nil && !found
}

// memberIndent is the whitespace the routines are indented by in the file: that of the first routine's line, or two more than the
// line of the closing brace of the routines object when there are none.
func memberIndent(raw []byte, members []jsonc.Member, closeAt int) string {
	if len(members) > 0 {
		return leadingBlanks(raw, members[0].Start)
	}
	return leadingBlanks(raw, closeAt) + "  "
}

// leadingBlanks is the spaces and tabs at the start of the line that offset is on.
func leadingBlanks(raw []byte, offset int) string {
	start := lineStart(raw, offset)
	end := start
	for end < len(raw) && (raw[end] == ' ' || raw[end] == '\t') {
		end++
	}
	return string(raw[start:end])
}

func lineStart(raw []byte, offset int) int {
	return bytes.LastIndexByte(raw[:offset], '\n') + 1
}

func skipBlanks(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
		i++
	}
	return i
}

// restOfLine is the offset just past the end of the line that i is on, newline included, as long as only blanks are left on it.
func restOfLine(raw []byte, i int) int {
	j := skipBlanks(raw, i)
	if j < len(raw) && raw[j] == '\r' {
		j++
	}
	if j < len(raw) && raw[j] == '\n' {
		return j + 1
	}
	return i // something else follows on the line (a comment, say): leave it
}

// insertMember puts a new member after the last one, adding the comma it needs, or in an empty object.
func insertMember(raw []byte, members []jsonc.Member, closeAt int, indent string, text []byte) []byte {
	var out []byte
	if len(members) == 0 {
		at := lineStart(raw, closeAt)
		if strings.TrimSpace(string(raw[at:closeAt])) != "" { // the brace shares its line: `"routines": {}`
			out = append(out, raw[:closeAt]...)
			out = append(out, '\n')
			out = append(out, indent...)
			out = append(out, text...)
			out = append(out, '\n')
			out = append(out, leadingBlanks(raw, closeAt)...)
			return append(out, raw[closeAt:]...)
		}
		out = append(out, raw[:at]...)
		out = append(out, indent...)
		out = append(out, text...)
		out = append(out, '\n')
		return append(out, raw[at:]...)
	}
	last := members[len(members)-1]
	after := skipBlanks(raw, last.End)
	hasComma := after < len(raw) && raw[after] == ','
	if hasComma {
		out = append(out, raw[:after+1]...) // a trailing comma was already allowed
	} else {
		out = append(out, raw[:last.End]...)
		out = append(out, ',')
		after = last.End
	}
	rest := raw[after+btoi(hasComma):]
	if after+btoi(hasComma) > len(raw) {
		rest = nil
	}
	out = append(out, '\n')
	out = append(out, indent...)
	out = append(out, text...)
	if hasComma {
		out = append(out, ',')
	}
	return append(out, rest...)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- writing a routine as JSON ----

type outRoutine struct {
	Description string   `json:"description,omitempty"`
	Repeat      any      `json:"repeat,omitempty"`
	Steps       []string `json:"steps"`
	Finish      []string `json:"finish,omitempty"`
	Waiting     string   `json:"waiting,omitempty"`
	Summary     []string `json:"summary,omitempty"`
}

var defaultSummary = []Where{WhereBanner, WhereUI}

// marshalRoutine writes `"name": {...}` with its lines indented by indent after the first. Steps are written as the commands that
// do them, and a setting at its default is left out so the file stays short.
func marshalRoutine(r Routine, indent string) ([]byte, error) {
	if r.Name == "" {
		return nil, errors.New("a routine needs a name")
	}
	out := outRoutine{Description: r.Description}
	switch {
	case r.Repeat == Forever:
		out.Repeat = "forever"
	case r.Repeat > 1:
		out.Repeat = r.Repeat
	}
	for _, s := range r.Steps {
		out.Steps = append(out.Steps, s.Command())
	}
	for _, s := range r.Finish {
		out.Finish = append(out.Finish, s.Command())
	}
	if r.Waiting != "" && r.Waiting != WaitStay {
		out.Waiting = string(r.Waiting)
	}
	if len(r.Summary) > 0 && !slices.Equal(r.Summary, defaultSummary) {
		for _, w := range r.Summary {
			out.Summary = append(out.Summary, string(w))
		}
	}
	// An encoder, not json.Marshal: that one writes & < > as \u0026 and the like, which would make a file full of noise.
	encode := func(v any, prefix string) ([]byte, error) {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		e.SetIndent(prefix, "  ")
		if err := e.Encode(v); err != nil {
			return nil, err
		}
		return bytes.TrimRight(b.Bytes(), "\n"), nil
	}
	body, err := encode(out, indent)
	if err != nil {
		return nil, err
	}
	key, err := encode(r.Name, "")
	if err != nil {
		return nil, err
	}
	return append(append(key, ": "...), body...), nil
}
