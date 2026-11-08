// Package monitor runs the measurement loop and turns verdicts into incidents.
package monitor

import (
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

// severity orders causes when two are equally frequent during an incident.
var severity = map[model.Cause]int{
	model.CauseLAN: 6, model.CauseISP: 5, model.CauseDNSAll: 4, model.CauseDNSISP: 3, model.CauseWeb: 2,
	model.CauseLossLAN: 2, model.CauseLossISP: 1,
}

// Tracker opens and closes incidents from successive verdicts.
// It ignores short blips: an outage needs OpenAfter failing cycles in a row.
type Tracker struct {
	OpenAfter     int
	CloseAfter    int
	DegOpenAfter  int
	DegCloseAfter int

	Outage      *model.Incident
	Degradation *model.Incident
	LastIP      string // last public IP seen while the connection worked

	pending    []model.Sample
	good       int
	firstGood  time.Time
	degPending []model.Sample
	degGood    int
	degFirst   time.Time
}

// Change reports what an observation changed.
type Change struct {
	Opened []model.Incident
	Closed []model.Incident
	Events []model.Event
}

// Observe feeds one diagnosed cycle.
func (t *Tracker) Observe(s model.Sample) Change {
	var ch Change
	down := s.Diag.Status == model.StatusDown

	// Public IP changes, only judged on working cycles.
	if !down && s.PublicIP != "" {
		if t.LastIP != "" && s.PublicIP != t.LastIP {
			ch.Events = append(ch.Events, model.Event{T: s.T, Kind: "ip_change", Detail: t.LastIP + " → " + s.PublicIP})
		}
		t.LastIP = s.PublicIP
	}

	// Outages.
	if down {
		t.good = 0
		if t.Outage == nil {
			t.pending = append(t.pending, s)
			if len(t.pending) >= t.OpenAfter {
				inc := &model.Incident{Kind: "outage", Start: t.pending[0].T, Counts: map[model.Cause]int{},
					Evidence: model.Evidence{IPBefore: t.LastIP}}
				for _, p := range t.pending {
					accumulate(inc, p)
				}
				inc.ID = "o-" + inc.Start.UTC().Format("20060102-150405")
				inc.Cause = mainCause(inc.Counts)
				t.Outage, t.pending = inc, nil
				ch.Opened = append(ch.Opened, *inc)
			}
		} else {
			accumulate(t.Outage, s)
			t.Outage.Cause = mainCause(t.Outage.Counts)
		}
	} else {
		if len(t.pending) > 0 {
			ch.Events = append(ch.Events, model.Event{T: t.pending[0].T, Kind: "blip",
				Detail: string(mainCause(countOf(t.pending)))})
			t.pending = nil
		}
		if t.Outage != nil {
			if t.good == 0 {
				t.firstGood = s.T
			}
			t.good++
			if s.PublicIP != "" {
				t.Outage.Evidence.IPAfter = s.PublicIP
			}
			if t.good >= t.CloseAfter {
				end := t.firstGood
				t.Outage.End = &end
				ch.Closed = append(ch.Closed, *t.Outage)
				t.Outage, t.good = nil, 0
			}
		}
	}

	// Degradations (packet loss), judged only while the connection works.
	switch s.Diag.Status {
	case model.StatusDegraded:
		t.degGood = 0
		if t.Degradation == nil {
			t.degPending = append(t.degPending, s)
			if len(t.degPending) >= t.DegOpenAfter {
				inc := &model.Incident{Kind: "degradation", Start: t.degPending[0].T, Counts: map[model.Cause]int{}}
				for _, p := range t.degPending {
					accumulate(inc, p)
				}
				inc.ID = "d-" + inc.Start.UTC().Format("20060102-150405")
				inc.Cause = mainCause(inc.Counts)
				t.Degradation, t.degPending = inc, nil
				ch.Opened = append(ch.Opened, *inc)
			}
		} else {
			accumulate(t.Degradation, s)
			t.Degradation.Cause = mainCause(t.Degradation.Counts)
		}
	case model.StatusOK:
		t.degPending = nil
		if t.Degradation != nil {
			if t.degGood == 0 {
				t.degFirst = s.T
			}
			t.degGood++
			if t.degGood >= t.DegCloseAfter {
				end := t.degFirst
				t.Degradation.End = &end
				ch.Closed = append(ch.Closed, *t.Degradation)
				t.Degradation, t.degGood = nil, 0
			}
		}
	}
	return ch
}

// Interrupt closes ongoing incidents at the given time, e.g. after a long monitoring gap.
func (t *Tracker) Interrupt(at time.Time, note string) []model.Incident {
	var out []model.Incident
	for _, p := range []**model.Incident{&t.Outage, &t.Degradation} {
		if *p != nil {
			end := at
			if end.Before((*p).Start) {
				end = (*p).Start
			}
			(*p).End = &end
			(*p).Note = note
			out = append(out, **p)
			*p = nil
		}
	}
	t.pending, t.degPending, t.good, t.degGood = nil, nil, 0, 0
	return out
}

func accumulate(inc *model.Incident, s model.Sample) {
	n := float64(inc.Cycles)
	inc.Cycles++
	if inc.Counts == nil {
		inc.Counts = map[model.Cause]int{}
	}
	inc.Counts[s.Diag.Cause]++
	if s.Gateway.OK() {
		inc.Evidence.GatewayAnswered++
	}
	if s.PublicDNSOK() {
		inc.Evidence.PublicDNSOK++
	}
	il, gl := s.InternetLoss(), s.Gateway.Loss()
	if il > inc.Evidence.InternetLossMax {
		inc.Evidence.InternetLossMax = il
	}
	inc.Evidence.InternetLossAvg = (inc.Evidence.InternetLossAvg*n + il) / (n + 1)
	inc.Evidence.GatewayLossAvg = (inc.Evidence.GatewayLossAvg*n + gl) / (n + 1)
}

func countOf(ss []model.Sample) map[model.Cause]int {
	m := map[model.Cause]int{}
	for _, s := range ss {
		m[s.Diag.Cause]++
	}
	return m
}

// mainCause is the most frequent cause, the most severe one on a tie.
func mainCause(counts map[model.Cause]int) model.Cause {
	best, bestN := model.CauseNone, -1
	for c, n := range counts {
		if n > bestN || (n == bestN && severity[c] > severity[best]) {
			best, bestN = c, n
		}
	}
	return best
}
