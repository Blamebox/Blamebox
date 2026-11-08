// Package store keeps measurements on disk as JSON Lines files.
//
// Layout of the data directory:
//
//	samples-YYYY-MM-DD.jsonl   one line per measurement cycle (UTC day)
//	incidents.jsonl            one line per closed incident
//	events.jsonl               notable facts (public IP changes, ...)
//	state.json                 ongoing incidents, so a restart does not lose them
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

const dayLayout = "2006-01-02"

// Store is safe for concurrent use.
type Store struct {
	dir    string
	mu     sync.Mutex
	cur    *os.File
	curDay string
}

// State is what must survive a restart.
type State struct {
	Outage      *model.Incident `json:"outage,omitempty"`
	Degradation *model.Incident `json:"degradation,omitempty"`
	LastIP      string          `json:"last_ip,omitempty"`
	LastSample  time.Time       `json:"last_sample"`
}

// Open creates the directory if needed.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("data directory %s: %w", dir, err)
	}
	probe := filepath.Join(dir, ".write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return nil, fmt.Errorf("data directory %s is not writable: %w", dir, err)
	}
	os.Remove(probe)
	return &Store{dir: dir}, nil
}

// Dir returns the data directory.
func (s *Store) Dir() string { return s.dir }

// Close flushes and closes the current file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil {
		err := s.cur.Close()
		s.cur = nil
		return err
	}
	return nil
}

// AppendSample writes one measurement cycle.
func (s *Store) AppendSample(sm model.Sample) error {
	line, err := json.Marshal(sm)
	if err != nil {
		return err
	}
	day := sm.T.UTC().Format(dayLayout)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil || s.curDay != day {
		if s.cur != nil {
			s.cur.Close()
		}
		f, err := os.OpenFile(filepath.Join(s.dir, "samples-"+day+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			s.cur = nil
			return err
		}
		s.cur, s.curDay = f, day
	}
	_, err = s.cur.Write(append(line, '\n'))
	return err
}

// Samples returns the cycles measured between from and to (inclusive), oldest first.
func (s *Store) Samples(from, to time.Time) ([]model.Sample, error) {
	var out []model.Sample
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.UTC()); d = d.Add(24 * time.Hour) {
		path := filepath.Join(s.dir, "samples-"+d.Format(dayLayout)+".jsonl")
		err := readLines(path, func(b []byte) {
			var sm model.Sample
			if json.Unmarshal(b, &sm) == nil && !sm.T.Before(from) && !sm.T.After(to) {
				out = append(out, sm)
			}
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return out, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// LastSampleTime returns the time of the most recent stored cycle, zero if none.
func (s *Store) LastSampleTime() time.Time {
	files, _ := filepath.Glob(filepath.Join(s.dir, "samples-*.jsonl"))
	sort.Strings(files)
	for i := len(files) - 1; i >= 0; i-- {
		var last time.Time
		readLines(files[i], func(b []byte) {
			var sm struct {
				T time.Time `json:"t"`
			}
			if json.Unmarshal(b, &sm) == nil && sm.T.After(last) {
				last = sm.T
			}
		})
		if !last.IsZero() {
			return last
		}
	}
	return time.Time{}
}

// FirstSampleTime returns the time of the oldest stored cycle, zero if none.
func (s *Store) FirstSampleTime() time.Time {
	files, _ := filepath.Glob(filepath.Join(s.dir, "samples-*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		var first time.Time
		readLines(f, func(b []byte) {
			var sm struct {
				T time.Time `json:"t"`
			}
			if json.Unmarshal(b, &sm) == nil && (first.IsZero() || sm.T.Before(first)) {
				first = sm.T
			}
		})
		if !first.IsZero() {
			return first
		}
	}
	return time.Time{}
}

// AppendIncident records a closed incident.
func (s *Store) AppendIncident(i model.Incident) error { return s.appendJSON("incidents.jsonl", i) }

// Incidents returns every recorded incident, oldest first.
func (s *Store) Incidents() ([]model.Incident, error) {
	var out []model.Incident
	err := readLines(filepath.Join(s.dir, "incidents.jsonl"), func(b []byte) {
		var i model.Incident
		if json.Unmarshal(b, &i) == nil {
			out = append(out, i)
		}
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Start.Before(out[b].Start) })
	return out, err
}

// AppendEvent records an event.
func (s *Store) AppendEvent(e model.Event) error { return s.appendJSON("events.jsonl", e) }

// Events returns the events since from, oldest first.
func (s *Store) Events(from time.Time) ([]model.Event, error) {
	var out []model.Event
	err := readLines(filepath.Join(s.dir, "events.jsonl"), func(b []byte) {
		var e model.Event
		if json.Unmarshal(b, &e) == nil && !e.T.Before(from) {
			out = append(out, e)
		}
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return out, err
}

// SaveState atomically replaces state.json.
func (s *Store) SaveState(st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, "state.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, "state.json"))
}

// LoadState reads state.json; a missing file gives an empty state.
func (s *Store) LoadState() (State, error) {
	var st State
	b, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

// Prune deletes measurement files older than the retention period.
// Incidents and events are small and kept forever.
func (s *Store) Prune(retentionDays int, now time.Time) (int, error) {
	limit := now.UTC().AddDate(0, 0, -retentionDays).Format(dayLayout)
	files, err := filepath.Glob(filepath.Join(s.dir, "samples-*.jsonl"))
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range files {
		day := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "samples-"), ".jsonl")
		if day < limit {
			s.mu.Lock()
			if s.curDay == day && s.cur != nil {
				s.cur.Close()
				s.cur = nil
			}
			s.mu.Unlock()
			if os.Remove(f) == nil {
				n++
			}
		}
	}
	return n, nil
}

func (s *Store) appendJSON(name string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func readLines(path string, fn func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if b := sc.Bytes(); len(b) > 0 {
			fn(b)
		}
	}
	return sc.Err()
}
