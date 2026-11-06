package store_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/charlesrobsampson/shh/internal/store"
)

func TestWeightsAreKeyedByFeatureVersion(t *testing.T) {
	s := open(t)
	if _, ok, err := s.LoadWeights("v1"); err != nil || ok {
		t.Fatalf("fresh store: ok=%v err=%v, want no weights", ok, err)
	}
	if err := s.UpdateModel("v1", []byte{1, 2, 3}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateModel("v2", []byte{9}, nil, nil); err != nil {
		t.Fatal(err)
	}
	v1, ok, _ := s.LoadWeights("v1")
	v2, _, _ := s.LoadWeights("v2")
	if !ok || !bytes.Equal(v1, []byte{1, 2, 3}) || !bytes.Equal(v2, []byte{9}) {
		t.Fatalf("v1=%v v2=%v, want each version to keep its own weights", v1, v2)
	}
}

func TestTheLatestWeightsWin(t *testing.T) {
	s := open(t)
	s.UpdateModel("v1", []byte{1}, nil, nil)
	s.UpdateModel("v1", []byte{2}, nil, nil)
	got, _, _ := s.LoadWeights("v1")
	if !bytes.Equal(got, []byte{2}) {
		t.Fatalf("weights = %v, want the latest", got)
	}
}

func TestTrainingLogIsNewestFirst(t *testing.T) {
	s := open(t)
	id := insert(t, s, sample(nil))
	entries := []store.TrainingLogEntry{
		{NotificationID: id, Label: "exact:80", Predicted: 0.5, Delta: []byte{1}},
		{Label: "checkpoint"},
		{NotificationID: id, Label: "y@65", Predicted: 0.4, Delta: []byte{2}},
	}
	if err := s.UpdateModel("v1", []byte{0}, entries, nil); err != nil {
		t.Fatal(err)
	}
	log, err := s.TrainingLog(0)
	if err != nil || len(log) != 3 {
		t.Fatalf("log = %+v, %v", log, err)
	}
	if log[0].Label != "y@65" || log[1].Label != "checkpoint" || log[2].Label != "exact:80" {
		t.Errorf("order = %q %q %q, want newest first", log[0].Label, log[1].Label, log[2].Label)
	}
	if log[0].NotificationID != id || log[0].Predicted != 0.4 || !bytes.Equal(log[0].Delta, []byte{2}) || log[0].ID == 0 {
		t.Errorf("entry = %+v", log[0])
	}
	if log[1].NotificationID != 0 || log[1].Delta != nil {
		t.Errorf("a marker has no notification and no delta: %+v", log[1])
	}
	limited, _ := s.TrainingLog(2)
	if len(limited) != 2 || limited[0].Label != "y@65" {
		t.Errorf("limited log = %+v", limited)
	}
}

func TestUpdateModelIsAtomicAndCanRemoveLogRows(t *testing.T) {
	s := open(t)
	s.UpdateModel("v1", []byte{1}, []store.TrainingLogEntry{{Label: "a", Delta: []byte{1}}, {Label: "b", Delta: []byte{2}}}, nil)
	log, _ := s.TrainingLog(0)

	if err := s.UpdateModel("v1", []byte{2}, nil, []int64{log[0].ID}); err != nil {
		t.Fatal(err)
	}
	after, _ := s.TrainingLog(0)
	if len(after) != 1 || after[0].Label != "a" {
		t.Fatalf("log after removing the newest = %+v", after)
	}

	// A row that violates a constraint must roll back the weights too.
	err := s.UpdateModel("v1", []byte{3}, []store.TrainingLogEntry{{Label: "needs a notification", NotificationID: 999}}, nil)
	if err == nil {
		t.Fatal("a log row pointing at a missing notification was accepted")
	}
	if got, _, _ := s.LoadWeights("v1"); !bytes.Equal(got, []byte{2}) {
		t.Fatalf("weights = %v after a failed update, want the old value to survive", got)
	}
}

func TestClearTrainingLog(t *testing.T) {
	s := open(t)
	s.UpdateModel("v1", []byte{1}, []store.TrainingLogEntry{{Label: "a"}, {Label: "b"}}, nil)
	if err := s.ClearTrainingLog(); err != nil {
		t.Fatal(err)
	}
	if log, _ := s.TrainingLog(0); len(log) != 0 {
		t.Fatalf("log = %+v, want empty", log)
	}
}

func TestModelStateSurvivesReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shh.db")
	first, _ := store.Open(path)
	first.UpdateModel("v1", []byte{7, 7}, []store.TrainingLogEntry{{Label: "a", Delta: []byte{1}}}, nil)
	first.Close()

	second, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if got, ok, _ := second.LoadWeights("v1"); !ok || !bytes.Equal(got, []byte{7, 7}) {
		t.Errorf("weights = %v, %v", got, ok)
	}
	if log, _ := second.TrainingLog(0); len(log) != 1 {
		t.Errorf("log = %+v", log)
	}
}
