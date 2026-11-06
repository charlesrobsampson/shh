// Package tomlx decodes TOML strictly, with errors that name the file, line and key.
package tomlx

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Decode reads raw into v and fails on keys v does not have. name is shown in errors, normally the file path.
func Decode(name string, raw []byte, v any) error {
	decoder := toml.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return describe(name, err)
	}
	return nil
}

func describe(name string, err error) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) && len(strict.Errors) > 0 {
		first := strict.Errors[0]
		line, _ := first.Position()
		return fmt.Errorf("%s:%d: unknown key %q", name, line, strings.Join(first.Key(), "."))
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		line, _ := decode.Position()
		if key := decode.Key(); len(key) > 0 {
			return fmt.Errorf("%s:%d: %q has the wrong type", name, line, strings.Join(key, "."))
		}
		return fmt.Errorf("%s:%d: %s", name, line, decode.Error())
	}
	return fmt.Errorf("%s: %w", name, err)
}
