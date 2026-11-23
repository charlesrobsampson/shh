package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/charlesrobsampson/shh/internal/jsonc"
)

// Addition is an option the documented template has and a person's config.json lacks, as the template writes it.
type Addition struct {
	// Path is the object it goes in ("alert", "shortcuts"), Key its name.
	Path []string
	Key  string
	// Lead is the paragraph of comments above it in the template (when a blank line sets it apart from the option before), Member the
	// option itself with its value, Trail the comment after it on its line.
	Lead, Member, Trail string
	// gap says the template has a blank line above it, which the file gets too.
	gap bool
	// before are the keys that come before it in the template's object, nearest first: where to put it is beside the nearest of them
	// the file has.
	before []string
}

// Name is the option's whole path, such as alert.shortcuts.
func (a Addition) Name() string {
	return strings.Join(append(append([]string(nil), a.Path...), a.Key), ".")
}

// Text is the addition as it will read in the file, without its comma.
func (a Addition) Text() string {
	return a.Lead + a.Member + a.Trail
}

// PlanUpgrade finds the options in the template that raw lacks, going down into the objects both have. Lists and the per-kind and
// per-app overrides are not entered: they only hold what a person put there. raw must load.
func PlanUpgrade(name string, raw []byte) ([]Addition, error) {
	if _, err := ParseBytes(name, raw); err != nil {
		return nil, err
	}
	return missing(name, raw, []byte(Template), nil)
}

func missing(name string, user, tmpl []byte, path []string) ([]Addition, error) {
	tmembers, _, found, err := jsonc.Members("the template", tmpl, path...)
	if err != nil || !found {
		return nil, err
	}
	umembers, ufound, err := membersOf(name, user, path)
	if err != nil {
		return nil, err
	}
	if !ufound {
		return nil, fmt.Errorf("%s: there is no object to add options to", name)
	}
	var out []Addition
	for i, tm := range tmembers {
		if indexOf(umembers, tm.Key) < 0 {
			out = append(out, additionOf(tmpl, tmembers, i, path))
			continue
		}
		inner := append(append([]string(nil), path...), tm.Key)
		_, _, isObject, err := jsonc.Members("the template", tmpl, inner...)
		if err != nil {
			return nil, err
		}
		_, _, userObject, err := jsonc.Members(name, user, inner...)
		if err != nil {
			return nil, err
		}
		if isObject && userObject {
			more, err := missing(name, user, tmpl, inner)
			if err != nil {
				return nil, err
			}
			out = append(out, more...)
		}
	}
	return out, nil
}

func membersOf(name string, raw []byte, path []string) ([]jsonc.Member, bool, error) {
	members, _, found, err := jsonc.Members(name, raw, path...)
	return members, found, err
}

func indexOf(members []jsonc.Member, key string) int {
	for i, m := range members {
		if m.Key == key {
			return i
		}
	}
	return -1
}

// additionOf reads the template's i-th option of an object, with the comments that belong to it.
func additionOf(tmpl []byte, members []jsonc.Member, i int, path []string) Addition {
	m := members[i]
	indent := leadingBlanks(tmpl, m.Start)
	a := Addition{Path: append([]string(nil), path...), Key: m.Key, Member: dedent(string(tmpl[m.Start:m.End]), indent)}
	for j := i - 1; j >= 0; j-- {
		a.before = append(a.before, members[j].Key)
	}
	// a comment on the same line after the option, with the blanks before it that line it up with the others
	after := m.End
	if after < len(tmpl) && tmpl[after] == ',' {
		after++
	}
	if end := bytes.IndexByte(tmpl[after:], '\n'); end >= 0 || after < len(tmpl) {
		if end < 0 {
			end = len(tmpl) - after
		}
		if rest := string(tmpl[after : after+end]); strings.HasPrefix(strings.TrimSpace(rest), "//") {
			a.Trail = strings.TrimRight(rest, " \t\r")
		}
	}
	// A paragraph of // lines straight above it, with a blank line (or the start of the object) above that. Comment lines that follow
	// the option before it directly are that option's, such as a commented-out setting, and are not taken.
	start := lineStart(tmpl, m.Start)
	var lead []string
	for start > 0 {
		prev := lineStart(tmpl, start-1)
		line := strings.TrimSpace(string(tmpl[prev:start]))
		if !strings.HasPrefix(line, "//") {
			break
		}
		lead = append([]string{strings.TrimPrefix(strings.TrimRight(string(tmpl[prev:start]), "\r\n"), indent)}, lead...)
		start = prev
	}
	a.gap = start > 0 && strings.TrimSpace(string(tmpl[lineStart(tmpl, start-1):start])) == ""
	if len(lead) > 0 && a.gap {
		a.Lead = strings.Join(lead, "\n") + "\n"
	}
	return a
}

// ApplyUpgrade puts the additions into raw, each beside the option that comes before it in the template (or at the end of its object),
// leaving the rest as it was. It checks that the result loads and means exactly what raw meant: the options come in at their defaults.
func ApplyUpgrade(name string, raw []byte, plan []Addition) ([]byte, error) {
	before, err := ParseBytes(name, raw)
	if err != nil {
		return nil, err
	}
	out := raw
	for _, a := range plan {
		if out, err = insertAddition(name, out, a); err != nil {
			return nil, err
		}
	}
	after, err := ParseBytes(name, out)
	if err != nil {
		return nil, fmt.Errorf("not upgraded, because the result would not load: %w", err)
	}
	was, _ := before.Effective()
	now, _ := after.Effective()
	if !bytes.Equal(was, now) {
		return nil, errors.New("not upgraded, because the result would not mean the same as the file does now")
	}
	return out, nil
}

func insertAddition(name string, raw []byte, a Addition) ([]byte, error) {
	members, closeAt, found, err := jsonc.Members(name, raw, a.Path...)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%s: there is no %q object to add %q to", name, strings.Join(a.Path, "."), a.Key)
	}
	anchor := -1
	for _, key := range a.before {
		if anchor = indexOf(members, key); anchor >= 0 {
			break
		}
	}
	if anchor < 0 {
		anchor = len(members) - 1
	}
	if anchor < 0 { // an empty object
		indent := leadingBlanks(raw, closeAt) + "  "
		text := a.render(indent, false, false)
		at := lineStart(raw, closeAt)
		var out []byte
		if strings.TrimSpace(string(raw[at:closeAt])) != "" { // the brace shares its line: {}
			out = append(out, raw[:closeAt]...)
			out = append(out, '\n')
			out = append(out, text...)
			out = append(out, '\n')
			out = append(out, leadingBlanks(raw, closeAt)...)
			return append(out, raw[closeAt:]...), nil
		}
		out = append(out, raw[:at]...)
		out = append(out, text...)
		out = append(out, '\n')
		return append(out, raw[at:]...), nil
	}

	indent := leadingBlanks(raw, members[0].Start)
	last := anchor == len(members)-1
	end := members[anchor].End
	after := skipBlanks(raw, end)
	hasComma := after < len(raw) && raw[after] == ','
	var out []byte
	insertAt := end
	if hasComma {
		insertAt = after + 1
		out = append(out, raw[:insertAt]...)
	} else if last {
		out = append(out, raw[:end]...) // it was the last member, so it gains the comma the new one now needs after it
		out = append(out, ',')
	} else {
		out = append(out, raw[:end]...)
	}
	// the new member is last when the one it follows was, unless a trailing comma was already the style of the file
	text := a.render(indent, !last || hasComma, true)
	// stay on the line the anchor ends on, after any comment on it, unless code (the closing brace, the next member) follows
	rest := raw[insertAt:]
	eol := bytes.IndexByte(rest, '\n')
	if eol < 0 {
		eol = len(rest)
	}
	lineRest := strings.TrimSpace(string(rest[:eol]))
	codeFollows := lineRest != "" && !strings.HasPrefix(lineRest, "//")
	if !codeFollows {
		out = append(out, rest[:eol]...)
		rest = rest[eol:]
	}
	out = append(out, '\n')
	out = append(out, text...)
	if codeFollows {
		out = append(out, '\n')
	}
	return append(out, rest...), nil
}

// render writes the addition at an indent, with its comma when more will follow.
func (a Addition) render(indent string, comma, gap bool) string {
	var b strings.Builder
	if gap && a.gap {
		b.WriteString("\n") // set apart from what is above it, as in the template
	}
	if a.Lead != "" {
		for _, line := range strings.Split(strings.TrimRight(a.Lead, "\n"), "\n") {
			b.WriteString(indent + line + "\n")
		}
	}
	for i, line := range strings.Split(a.Member, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		if line != "" {
			b.WriteString(indent + line)
		}
	}
	if comma {
		b.WriteString(",")
	}
	b.WriteString(a.Trail)
	return b.String()
}

func dedent(text, indent string) string {
	lines := strings.Split(text, "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.TrimPrefix(lines[i], indent)
	}
	return strings.Join(lines, "\n")
}

func leadingBlanks(raw []byte, offset int) string {
	start := lineStart(raw, offset)
	end := start
	for end < len(raw) && (raw[end] == ' ' || raw[end] == '\t') {
		end++
	}
	return string(raw[start:end])
}

func lineStart(raw []byte, offset int) int { return bytes.LastIndexByte(raw[:offset], '\n') + 1 }

func skipBlanks(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t') {
		i++
	}
	return i
}

// UpgradeResult says what an upgrade found and did.
type UpgradeResult struct {
	Added   []Addition
	Written bool
	// Backup is where the file was copied to before it was changed.
	Backup string
}

// Upgrade brings the config.json at path up to date with the template, adding what it lacks at the defaults and keeping everything the
// person wrote. Without write it only reports. A copy of the file is kept first, and nothing is written unless the result loads and
// means the same.
func Upgrade(path string, write bool) (UpgradeResult, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return UpgradeResult{}, fmt.Errorf("there is no %s to upgrade: shh init writes one with every option explained", path)
	}
	if err != nil {
		return UpgradeResult{}, err
	}
	plan, err := PlanUpgrade(path, raw)
	if err != nil || len(plan) == 0 || !write {
		return UpgradeResult{Added: plan}, err
	}
	out, err := ApplyUpgrade(path, raw, plan)
	if err != nil {
		return UpgradeResult{}, err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	backup := path + ".bak"
	for n := 2; ; n++ {
		if _, err := os.Stat(backup); errors.Is(err, fs.ErrNotExist) {
			break
		}
		backup = fmt.Sprintf("%s.bak-%d", path, n)
	}
	if err := os.WriteFile(backup, raw, mode); err != nil {
		return UpgradeResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return UpgradeResult{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return UpgradeResult{}, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return UpgradeResult{}, err
	}
	if err := tmp.Close(); err != nil {
		return UpgradeResult{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return UpgradeResult{}, err
	}
	return UpgradeResult{Added: plan, Written: true, Backup: backup}, nil
}

// CountMissing is how many options an upgrade would add, which is 0 when there is no file (the defaults are in force then).
func CountMissing(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	plan, err := PlanUpgrade(path, raw)
	return len(plan), err
}
