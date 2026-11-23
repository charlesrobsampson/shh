package testkit_test

import (
	"errors"
	"testing"

	"github.com/charlesrobsampson/shh/internal/config"
	"github.com/charlesrobsampson/shh/internal/hotkey"
	"github.com/charlesrobsampson/shh/internal/testkit"
)

func TestFakeHotkeysRegisterPressAndUnregister(t *testing.T) {
	f := testkit.NewFakeHotkeys()
	var got []string
	b := hotkey.Binding{Modifier: config.Ctrl | config.Alt, Keys: map[string]string{"next": "]", "dismiss": "d", "flag": ""}}
	if err := f.Register(b, func(a string) { got = append(got, a) }); err != nil {
		t.Fatal(err)
	}
	held, ok := f.Held()
	if !ok || held.Modifier != b.Modifier || len(held.Keys) != 2 {
		t.Fatalf("held = %+v, %v: the empty key should not be held", held, ok)
	}
	if !f.Press("dismiss") || f.Press("flag") || f.Press("train") {
		t.Error("only a held key can be pressed")
	}
	f.Unregister()
	if _, ok := f.Held(); ok || f.Press("dismiss") {
		t.Error("nothing should be held after Unregister")
	}
	if len(got) != 1 || got[0] != "dismiss" {
		t.Errorf("presses = %v", got)
	}
}

func TestFakeHotkeysRefuseAKeyAnotherAppHolds(t *testing.T) {
	f := testkit.NewFakeHotkeys()
	f.Refuse("]")
	err := f.Register(hotkey.Binding{Modifier: config.Ctrl, Keys: map[string]string{"next": "]", "dismiss": "d"}}, func(string) {})
	var refused *hotkey.RegisterError
	if !errors.As(err, &refused) || refused.Refused["next"] == "" || len(refused.Refused) != 1 {
		t.Fatalf("err = %v", err)
	}
	if f.Press("next") || !f.Press("dismiss") {
		t.Error("the refused key is not held, the other is")
	}
}

func TestFakeHotkeysRegisteringAgainReplacesTheBinding(t *testing.T) {
	f := testkit.NewFakeHotkeys()
	var first, second int
	f.Register(hotkey.Binding{Modifier: config.Ctrl, Keys: map[string]string{"dismiss": "d"}}, func(string) { first++ })
	f.Register(hotkey.Binding{Modifier: config.Cmd, Keys: map[string]string{"dismiss": "w"}}, func(string) { second++ })
	f.Press("dismiss")
	if held, _ := f.Held(); held.Modifier != config.Cmd || held.Keys["dismiss"] != "w" || first != 0 || second != 1 || f.Registrations() != 2 {
		t.Errorf("held %+v, first %d, second %d, registrations %d", held, first, second, f.Registrations())
	}
}
