// Package jsonc decodes JSON that may contain comments and trailing commas, strictly, with errors that name the file, line and key.
package jsonc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// Decode reads raw into v, which must be a non-nil pointer to a struct. Unlike encoding/json it rejects keys the struct does not
// have, matches keys exactly (not case-insensitively), and reports problems as "name:line: message". A file with nothing in it
// (or only comments) is an empty object. name is shown in errors, normally the file path.
func Decode(name string, raw []byte, v any) error {
	clean := standardize(raw)
	if len(bytes.TrimSpace(clean)) == 0 {
		clean = []byte("{}")
	}
	if err := checkKeys(clean, reflect.TypeOf(v).Elem()); err != nil {
		return describe(name, clean, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(clean))
	if err := decoder.Decode(v); err != nil {
		return describe(name, clean, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s:%d: unexpected data after the top-level object", name, lineAt(clean, decoder.InputOffset()))
	}
	return nil
}

// standardize turns JSON with comments and trailing commas into plain JSON of exactly the same length, so byte offsets, and
// therefore line numbers, in errors still refer to the original text.
func standardize(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	lastComma := -1 // a comma that is only followed by blanks and comments so far
	for i := 0; i < len(out); i++ {
		switch c := out[i]; {
		case c == '"':
			lastComma = -1
			for i++; i < len(out) && out[i] != '"'; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case c == '/' && i+1 < len(out) && out[i+1] == '/':
			end := i
			for end < len(out) && out[end] != '\n' {
				end++
			}
			blank(i, end)
			i = end - 1
		case c == '/' && i+1 < len(out) && out[i+1] == '*':
			end := bytes.Index(out[i+2:], []byte("*/"))
			if end < 0 {
				return out // unterminated: leave it for the JSON parser to complain about
			}
			end += i + 4
			blank(i, end)
			i = end - 1
		case c == ',':
			lastComma = i
		case c == '}' || c == ']':
			if lastComma >= 0 {
				out[lastComma] = ' '
			}
			lastComma = -1
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
		default:
			lastComma = -1
		}
	}
	return out
}

type keyError struct {
	path   string
	offset int64
}

func (e *keyError) Error() string { return fmt.Sprintf("unknown key %q", e.path) }

func describe(name string, text []byte, err error) error {
	var key *keyError
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	switch {
	case errors.As(err, &key):
		return fmt.Errorf("%s:%d: %s", name, lineAt(text, key.offset), key)
	case errors.As(err, &typed):
		field := typed.Field
		if field == "" {
			return fmt.Errorf("%s:%d: the top level must be an object", name, lineAt(text, typed.Offset))
		}
		return fmt.Errorf("%s:%d: %q has the wrong type (expected %s)", name, lineAt(text, typed.Offset), field, typed.Type)
	case errors.As(err, &syntax):
		return fmt.Errorf("%s:%d: %s", name, lineAt(text, syntax.Offset), syntax)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%s:%d: the file ends in the middle of a value", name, lineAt(text, int64(len(text))))
	}
	return fmt.Errorf("%s: %w", name, err)
}

func lineAt(text []byte, offset int64) int {
	offset = max(0, min(offset, int64(len(text))))
	return 1 + bytes.Count(text[:offset], []byte("\n"))
}

// checkKeys walks the JSON against the struct type and fails on the first key the type does not have.
func checkKeys(text []byte, t reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(text))
	if tok, err := decoder.Token(); err != nil {
		return err
	} else if d, ok := tok.(json.Delim); !ok || d != '{' {
		return &json.UnmarshalTypeError{Value: "non-object", Type: t, Offset: decoder.InputOffset()}
	}
	return walkObject(decoder, t, "")
}

// walkObject reads the members of an object whose opening brace has been consumed. t nil means "do not check the keys".
func walkObject(decoder *json.Decoder, t reflect.Type, path string) error {
	var known map[string]reflect.Type
	var elem reflect.Type
	if t != nil {
		switch t.Kind() {
		case reflect.Struct:
			known = fieldTypes(t)
		case reflect.Map:
			elem = t.Elem()
		}
	}
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			return err
		}
		key := tok.(string)
		memberPath := joinPath(path, key)
		var memberType reflect.Type
		switch {
		case known != nil:
			ft, ok := known[key]
			if !ok {
				return &keyError{path: memberPath, offset: decoder.InputOffset()}
			}
			memberType = ft
		case elem != nil:
			memberType = elem
		}
		if err := walkValue(decoder, memberType, memberPath); err != nil {
			return err
		}
	}
	_, err := decoder.Token() // the closing brace
	return err
}

// walkValue reads one value; if it is an object or array of a type we know, it checks inside it too.
func walkValue(decoder *json.Decoder, t reflect.Type, path string) error {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		return walkObject(decoder, t, path)
	}
	var elem reflect.Type
	if t != nil && t.Kind() == reflect.Slice {
		elem = t.Elem()
	}
	for decoder.More() {
		if err := walkValue(decoder, elem, path); err != nil {
			return err
		}
	}
	_, err = decoder.Token() // the closing bracket
	return err
}

// fieldTypes lists the keys a struct accepts: its fields by their json names, with embedded structs' fields alongside.
func fieldTypes(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" && f.Anonymous {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				for key, typ := range fieldTypes(ft) {
					out[key] = typ
				}
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
	}
	return out
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
