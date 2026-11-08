package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

func TestSamplesAcrossDays(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Date(2026, 11, 7, 23, 58, 0, 0, time.UTC)
	for i := 0; i < 8; i++ { // crosses midnight UTC
		if err := s.AppendSample(model.Sample{T: base.Add(time.Duration(i) * 30 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Samples(base, base.Add(time.Hour))
	if err != nil || len(all) != 8 {
		t.Fatalf("got %d samples, err %v", len(all), err)
	}
	some, _ := s.Samples(base.Add(90*time.Second), base.Add(150*time.Second))
	if len(some) != 3 {
		t.Fatalf("range filter: got %d, want 3", len(some))
	}
	if first, last := s.FirstSampleTime(), s.LastSampleTime(); !first.Equal(base) || !last.Equal(base.Add(210*time.Second)) {
		t.Fatalf("first %v last %v", first, last)
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir(), "samples-*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("expected one file per UTC day, got %v", files)
	}
}

func TestIncidentsEventsState(t *testing.T) {
	s, _ := Open(t.TempDir())
	end := time.Date(2026, 11, 8, 21, 10, 0, 0, time.UTC)
	inc := model.Incident{ID: "o-1", Kind: "outage", Cause: model.CauseISP, Start: end.Add(-10 * time.Minute), End: &end,
		Counts: map[model.Cause]int{model.CauseISP: 20}}
	if err := s.AppendIncident(inc); err != nil {
		t.Fatal(err)
	}
	got, err := s.Incidents()
	if err != nil || len(got) != 1 || got[0].Counts[model.CauseISP] != 20 || !got[0].End.Equal(end) {
		t.Fatalf("incidents roundtrip: %+v %v", got, err)
	}
	s.AppendEvent(model.Event{T: end, Kind: "ip_change", Detail: "a → b"})
	if ev, _ := s.Events(end.Add(-time.Hour)); len(ev) != 1 {
		t.Fatalf("events: %+v", ev)
	}
	if ev, _ := s.Events(end.Add(time.Hour)); len(ev) != 0 {
		t.Fatalf("events filter: %+v", ev)
	}
	st := State{Outage: &inc, LastIP: "203.0.113.1", LastSample: end}
	if err := s.SaveState(st); err != nil {
		t.Fatal(err)
	}
	back, err := s.LoadState()
	if err != nil || back.Outage == nil || back.Outage.ID != "o-1" || back.LastIP != st.LastIP {
		t.Fatalf("state roundtrip: %+v %v", back, err)
	}
}

func TestEmptyStore(t *testing.T) {
	s, _ := Open(t.TempDir())
	if inc, err := s.Incidents(); err != nil || len(inc) != 0 {
		t.Fatal("empty incidents should not fail")
	}
	if st, err := s.LoadState(); err != nil || st.Outage != nil {
		t.Fatal("empty state should not fail")
	}
	if !s.LastSampleTime().IsZero() {
		t.Fatal("no samples yet")
	}
}

func TestPrune(t *testing.T) {
	s, _ := Open(t.TempDir())
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for d := 0; d < 12; d++ {
		s.AppendSample(model.Sample{T: now.AddDate(0, 0, -d)})
	}
	n, err := s.Prune(7, now)
	if err != nil || n != 4 {
		t.Fatalf("pruned %d, err %v; want 4 (days 8 to 11)", n, err)
	}
	left, _ := filepath.Glob(filepath.Join(s.Dir(), "samples-*.jsonl"))
	if len(left) != 8 {
		t.Fatalf("%d files left, want 8", len(left))
	}
	// Writing still works after pruning.
	if err := s.AppendSample(model.Sample{T: now}); err != nil {
		t.Fatal(err)
	}
}

func TestUnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o500)
	if _, err := Open(dir); err == nil {
		t.Fatal("expected an error for a read-only directory")
	}
}
