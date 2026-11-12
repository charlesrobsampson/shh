package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charlesrobsampson/shh/internal/config"
)

func edit(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	// a file system that keeps coarse times could leave the stamp as it was: move it on
	later := time.Now().Add(time.Duration(len(text)+1) * time.Millisecond)
	_ = os.Chtimes(path, later, later)
}

func TestTheWatcherGivesTheNewConfigurationAfterTheFileChanges(t *testing.T) {
	path := write(t, `{"alert": {"color": 33}}`)
	initial, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	w := config.NewWatcher(path, initial, func(error) { t.Error("nothing is wrong") })
	if got := w.Alert().Base.Color; got != 33 {
		t.Fatalf("color = %d", got)
	}
	edit(t, path, `{"alert": {"color": 196, "text_color": 0, "sound": "Hero"}}`)
	if got := w.Alert().Base; got.Color != 196 || got.TextColor != 0 || got.Sound != "Hero" {
		t.Errorf("after the edit: %+v", got)
	}
	if got := w.Config().Alert.Base.Color; got != 196 {
		t.Errorf("Config and Alert agree: %d", got)
	}
}

func TestAFileThatCannotBeReadKeepsTheLastGoodSettingsAndSaysSoOnce(t *testing.T) {
	path := write(t, `{"alert": {"color": 33}}`)
	initial, _ := config.Load(path)
	var errs []error
	w := config.NewWatcher(path, initial, func(err error) { errs = append(errs, err) })
	edit(t, path, `{"alert": {"color": "red"`) // a typo mid-edit
	for i := 0; i < 3; i++ {
		if got := w.Alert().Base.Color; got != 33 {
			t.Errorf("the last good color should stay: %d", got)
		}
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "last good settings stay in use") {
		t.Errorf("errors = %v: one report, however many banners", errs)
	}
	edit(t, path, `{"alert": {"color": 46}}`) // fixed
	if got := w.Alert().Base.Color; got != 46 {
		t.Errorf("after the fix: %d", got)
	}
	edit(t, path, `{"alert": {"color": 999}}`) // a different mistake is reported again
	w.Alert()
	if len(errs) != 2 {
		t.Errorf("errors = %v", errs)
	}
}

func TestADeletedFileMeansTheDefaultsAndANewOneIsPickedUp(t *testing.T) {
	path := write(t, `{"alert": {"color": 33}}`)
	initial, _ := config.Load(path)
	w := config.NewWatcher(path, initial, nil)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := w.Alert().Base.Color; got != config.DefaultColor {
		t.Errorf("a missing file is the defaults: %d", got)
	}
	edit(t, path, `{"alert": {"color": 41}}`)
	if got := w.Alert().Base.Color; got != 41 {
		t.Errorf("a file that comes back: %d", got)
	}
}

func TestTheWatcherDoesNotReadAFileThatHasNotChanged(t *testing.T) {
	path := write(t, `{"alert": {"color": 33}}`)
	initial, _ := config.Load(path)
	w := config.NewWatcher(path, initial, nil)
	first := w.Config()
	second := w.Config()
	if first.Alert.Base != second.Alert.Base {
		t.Error("the same configuration comes back")
	}
}

func TestWatcherGivesRetentionAsTheFileSaysNow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{}`), 0o600)
	initial, _ := config.Load(path)
	w := config.NewWatcher(path, initial, nil)
	if w.Retention().TTLDays != 30 {
		t.Fatal("default first")
	}
	time.Sleep(20 * time.Millisecond)
	os.WriteFile(path, []byte(`{"retention": {"ttl_days": 14, "keep_untrained_days": 20, "auto_train_after_days": 14}}`), 0o600)
	if r := w.Retention(); r.TTLDays != 14 || r.KeepUntrainedDays != 20 {
		t.Errorf("retention = %+v", r)
	}
}
