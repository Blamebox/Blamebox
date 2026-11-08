package monitor

import (
	"context"
	"log"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/diag"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/notify"
	"github.com/blamebox/blamebox/internal/probe"
	"github.com/blamebox/blamebox/internal/store"
)

// Version is set at build time.
var Version = "dev"

// recentSpan is how much history is kept in memory for the live charts.
const recentSpan = 24 * time.Hour

// Monitor runs the measurement loop. It is safe for concurrent use.
type Monitor struct {
	cfg    config.Config
	prober probe.Prober
	st     *store.Store
	notif  *notify.Notifier
	loc    *time.Location

	mu          sync.RWMutex
	recent      []model.Sample
	incidents   []model.Incident
	events      []model.Event
	tracker     Tracker
	started     time.Time
	firstSeen   time.Time // oldest measurement on disk
	statusSince time.Time
	lastPrune   time.Time
	batch       bool // backfilling: skip per-cycle state writes
}

// New restores the previous state from the store.
func New(cfg config.Config, p probe.Prober, st *store.Store, n *notify.Notifier, loc *time.Location, now time.Time) (*Monitor, error) {
	m := &Monitor{cfg: cfg, prober: p, st: st, notif: n, loc: loc, started: now,
		tracker: Tracker{OpenAfter: cfg.OpenAfter, CloseAfter: cfg.CloseAfter, DegOpenAfter: 4, DegCloseAfter: 20}}
	var err error
	if m.recent, err = st.Samples(now.Add(-recentSpan), now); err != nil {
		log.Printf("warning: reading recent measurements: %v", err)
	}
	if m.incidents, err = st.Incidents(); err != nil {
		log.Printf("warning: reading incidents: %v", err)
	}
	if m.events, err = st.Events(now.AddDate(0, 0, -30)); err != nil {
		log.Printf("warning: reading events: %v", err)
	}
	m.firstSeen = st.FirstSampleTime()
	if m.firstSeen.IsZero() {
		m.firstSeen = now
	}
	state, err := st.LoadState()
	if err != nil {
		log.Printf("warning: reading state.json: %v", err)
	}
	m.tracker.Outage, m.tracker.Degradation, m.tracker.LastIP = state.Outage, state.Degradation, state.LastIP

	// After a long gap, ongoing incidents cannot be trusted any more: close them where the data stops.
	last := st.LastSampleTime()
	if gap := maxDur(10*time.Minute, 5*cfg.Interval); !last.IsZero() && now.Sub(last) > gap {
		for _, inc := range m.tracker.Interrupt(last, "interrupted") {
			m.recordClosed(inc)
		}
		m.saveState()
	}
	if len(m.recent) > 0 {
		m.statusSince = m.recent[len(m.recent)-1].T
	}
	m.addEvent(model.Event{T: now, Kind: "start", Detail: Version})
	return m, nil
}

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// Run measures until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.Step(ctx, time.Now())
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.Step(ctx, now)
		}
	}
}

// Step runs one measurement cycle and records its consequences.
func (m *Monitor) Step(ctx context.Context, now time.Time) model.Sample {
	cctx, cancel := context.WithTimeout(ctx, m.cfg.Interval-time.Second)
	s, evs := m.prober.Probe(cctx, now)
	cancel()
	return m.Ingest(ctx, s, evs)
}

// Ingest diagnoses an already measured cycle and records it. Exposed for the demo backfill and tests.
func (m *Monitor) Ingest(ctx context.Context, s model.Sample, evs []model.Event) model.Sample {
	m.mu.Lock()
	// First verdict without the quality window; then judge the loss over the window.
	s.Diag = diag.Evaluate(s, diag.Window{}, m.cfg.LossThreshold)
	if s.Diag.Status != model.StatusDown {
		w := diag.RollingWindow(append(m.tail(m.cfg.LossWindow-1), s))
		s.Diag = diag.Evaluate(s, w, m.cfg.LossThreshold)
	}
	prev := model.Diagnosis{Status: model.StatusOK, Cause: model.CauseNone}
	if len(m.recent) > 0 {
		prev = m.recent[len(m.recent)-1].Diag
	}
	if prev.Status != s.Diag.Status || prev.Cause != s.Diag.Cause || m.statusSince.IsZero() {
		m.statusSince = s.T
	}
	if s.T.Before(m.firstSeen) {
		m.firstSeen = s.T
	}
	m.recent = append(m.recent, s)
	cut := 0
	for cut < len(m.recent) && s.T.Sub(m.recent[cut].T) > recentSpan {
		cut++
	}
	m.recent = m.recent[cut:]
	if err := m.st.AppendSample(s); err != nil {
		log.Printf("error: saving measurement: %v", err)
	}
	ch := m.tracker.Observe(s)
	ch.Events = append(evs, ch.Events...)
	for _, e := range ch.Events {
		m.addEvent(e)
	}
	for _, inc := range ch.Closed {
		m.recordClosed(inc)
	}
	if !m.batch {
		m.saveState()
	}
	if s.T.Sub(m.lastPrune) > 6*time.Hour {
		m.lastPrune = s.T
		if n, err := m.st.Prune(m.cfg.RetentionDays, s.T); err == nil && n > 0 {
			log.Printf("pruned %d old measurement file(s)", n)
		}
	}
	m.mu.Unlock()

	// Alerts are sent outside the lock: they may take a few seconds.
	if m.notif != nil {
		for _, inc := range ch.Opened {
			log.Printf("incident opened: %s %s", inc.Kind, inc.Cause)
			m.notif.Enqueue(ctx, m.notif.Opened(inc))
		}
		for _, inc := range ch.Closed {
			log.Printf("incident closed: %s %s (%s)", inc.Kind, inc.Cause, inc.Duration(s.T).Round(time.Second))
			m.notif.Enqueue(ctx, m.notif.Closed(inc))
		}
		if s.Diag.Status != model.StatusDown && m.notif.Pending() > 0 {
			go m.notif.TryFlush(ctx)
		}
	}
	return s
}

// Backfill ingests a batch of past cycles (demo history) without alerts.
func (m *Monitor) Backfill(ctx context.Context, samples []model.Sample) {
	notif := m.notif
	m.notif, m.batch = nil, true
	for _, s := range samples {
		m.Ingest(ctx, s, nil)
	}
	m.notif, m.batch = notif, false
	m.mu.Lock()
	m.saveState()
	m.mu.Unlock()
}

// tail returns up to n of the most recent samples (caller holds the lock).
func (m *Monitor) tail(n int) []model.Sample {
	if n <= 0 {
		return nil
	}
	if len(m.recent) <= n {
		return append([]model.Sample(nil), m.recent...)
	}
	return append([]model.Sample(nil), m.recent[len(m.recent)-n:]...)
}

func (m *Monitor) recordClosed(inc model.Incident) {
	m.incidents = append(m.incidents, inc)
	if err := m.st.AppendIncident(inc); err != nil {
		log.Printf("error: saving incident: %v", err)
	}
}

func (m *Monitor) addEvent(e model.Event) {
	m.events = append(m.events, e)
	if len(m.events) > 2000 {
		m.events = m.events[len(m.events)-2000:]
	}
	if err := m.st.AppendEvent(e); err != nil {
		log.Printf("error: saving event: %v", err)
	}
}

func (m *Monitor) saveState() {
	st := store.State{Outage: m.tracker.Outage, Degradation: m.tracker.Degradation, LastIP: m.tracker.LastIP}
	if len(m.recent) > 0 {
		st.LastSample = m.recent[len(m.recent)-1].T
	}
	if err := m.st.SaveState(st); err != nil {
		log.Printf("error: saving state: %v", err)
	}
}

// ---- read side, used by the web interface and the report ----

// Snapshot is the current state of the connection.
type Snapshot struct {
	Now         time.Time            `json:"now"`
	Started     time.Time            `json:"started"`
	Version     string               `json:"version"`
	IntervalS   float64              `json:"interval_s"`
	Last        *model.Sample        `json:"last,omitempty"`
	Diag        model.Diagnosis      `json:"diag"`
	Texts       map[string]diag.Text `json:"texts"`
	Since       time.Time            `json:"since"`
	Outage      *model.Incident      `json:"outage,omitempty"`
	Degradation *model.Incident      `json:"degradation,omitempty"`
	Window      diag.Window          `json:"window"`
	Info        probe.Info           `json:"info"`
	Stats24h    Stats                `json:"stats_24h"`
	Stats7d     Stats                `json:"stats_7d"`
	Channels    []string             `json:"channels"`
	Pending     int                  `json:"pending_alerts"`
	Warnings    []string             `json:"warnings"`
	Retention   int                  `json:"retention_days"`
	Lang        string               `json:"lang"`
	ISP         string               `json:"isp,omitempty"`
}

// Snapshot returns the current state.
func (m *Monitor) Snapshot(now time.Time) Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sn := Snapshot{Now: now, Started: m.started, Version: Version, IntervalS: m.cfg.Interval.Seconds(),
		Info: m.prober.Info(), Retention: m.cfg.RetentionDays, Lang: m.cfg.Lang, ISP: m.cfg.ISPName,
		Diag: model.Diagnosis{Status: model.StatusOK, Cause: model.CauseNone}, Since: m.statusSince, Warnings: []string{}}
	if len(m.recent) > 0 {
		last := m.recent[len(m.recent)-1]
		sn.Last, sn.Diag = &last, last.Diag
		sn.Window = diag.RollingWindow(m.tail(m.cfg.LossWindow))
	}
	sn.Texts = map[string]diag.Text{}
	for _, l := range diag.Langs() {
		sn.Texts[l] = diag.Explain(sn.Diag.Cause, l)
	}
	if o := m.tracker.Outage; o != nil {
		c := *o
		sn.Outage = &c
	}
	if d := m.tracker.Degradation; d != nil {
		c := *d
		sn.Degradation = &c
	}
	sn.Stats24h = m.statsLocked(now.Add(-24*time.Hour), now)
	sn.Stats7d = m.statsLocked(now.Add(-7*24*time.Hour), now)
	if m.notif != nil {
		sn.Channels, sn.Pending = m.notif.Channels(), m.notif.Pending()
	}
	if sn.Info.PingMethod == probe.MethodTCP {
		sn.Warnings = append(sn.Warnings, "ping_tcp")
	}
	if sn.Info.DNSLocalDisabled {
		sn.Warnings = append(sn.Warnings, "dns_local_disabled")
	}
	if !sn.Info.Demo {
		sn.Warnings = append(sn.Warnings, SetupWarnings(sn.Info.Gateway, sn.Info.Iface)...)
	}
	if len(m.recent) == 0 {
		sn.Warnings = append(sn.Warnings, "no_data")
	} else if now.Sub(m.recent[len(m.recent)-1].T) > 3*m.cfg.Interval+10*time.Second {
		sn.Warnings = append(sn.Warnings, "stale")
	}
	return sn
}

// Incidents returns the closed incidents that overlap [from, to] plus the ongoing ones, newest first.
func (m *Monitor) Incidents(from, to time.Time) []model.Incident {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.incidentsLocked(from, to)
}

func (m *Monitor) incidentsLocked(from, to time.Time) []model.Incident {
	var out []model.Incident
	for _, i := range m.incidents {
		end := to
		if i.End != nil {
			end = *i.End
		}
		if !end.Before(from) && !i.Start.After(to) {
			out = append(out, i)
		}
	}
	for _, p := range []*model.Incident{m.tracker.Outage, m.tracker.Degradation} {
		if p != nil && !p.Start.After(to) {
			out = append(out, *p)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Start.After(out[b].Start) })
	return out
}

// Samples returns measurements in [from, to], from memory when possible.
func (m *Monitor) Samples(from, to time.Time) []model.Sample {
	m.mu.RLock()
	if len(m.recent) > 0 && !from.Before(m.recent[0].T.Add(-m.cfg.Interval)) {
		var out []model.Sample
		for _, s := range m.recent {
			if !s.T.Before(from) && !s.T.After(to) {
				out = append(out, s)
			}
		}
		m.mu.RUnlock()
		return out
	}
	m.mu.RUnlock()
	out, err := m.st.Samples(from, to)
	if err != nil {
		log.Printf("warning: reading measurements: %v", err)
	}
	return out
}

// Events returns the events in [from, to], newest first.
func (m *Monitor) Events(from, to time.Time) []model.Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.Event
	for i := len(m.events) - 1; i >= 0; i-- {
		e := m.events[i]
		if !e.T.Before(from) && !e.T.After(to) {
			out = append(out, e)
		}
	}
	return out
}

// Stats summarises a period.
type Stats struct {
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	Outages       int       `json:"outages"`
	Degradations  int       `json:"degradations"`
	DowntimeS     float64   `json:"downtime_s"`
	ISPDowntimeS  float64   `json:"isp_downtime_s"`
	HomeDowntimeS float64   `json:"home_downtime_s"`
	Availability  float64   `json:"availability"` // 0–1
	Evening       int       `json:"evening_outages"`
	Blips         int       `json:"blips"`
	IPChanges     int       `json:"ip_changes"`
}

// Stats summarises a period.
func (m *Monitor) Stats(from, to time.Time) Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statsLocked(from, to)
}

func (m *Monitor) statsLocked(from, to time.Time) Stats {
	st := Stats{From: from, To: to}
	for _, i := range m.incidentsLocked(from, to) {
		if i.Kind == "degradation" {
			st.Degradations++
			continue
		}
		st.Outages++
		s, e := i.Start, to
		if i.End != nil {
			e = *i.End
		}
		if s.Before(from) {
			s = from
		}
		if e.After(to) {
			e = to
		}
		d := e.Sub(s).Seconds()
		if d < 0 {
			d = 0
		}
		st.DowntimeS += d
		if i.Cause.HomeSide() {
			st.HomeDowntimeS += d
		} else {
			st.ISPDowntimeS += d
		}
		if h := i.Start.In(m.loc).Hour(); h >= 19 && h < 23 {
			st.Evening++
		}
	}
	for _, e := range m.events {
		if e.T.Before(from) || e.T.After(to) {
			continue
		}
		switch e.Kind {
		case "blip":
			st.Blips++
		case "ip_change":
			st.IPChanges++
		}
	}
	if span := to.Sub(from).Seconds(); span > 0 {
		st.Availability = 1 - st.DowntimeS/span
		// Before the first measurement nothing is known: judge only the observed span.
		first := m.firstSeen
		if len(m.recent) > 0 && m.recent[0].T.Before(first) {
			first = m.recent[0].T
		}
		if first.After(from) {
			if obs := to.Sub(first).Seconds(); obs > 0 {
				st.Availability = 1 - st.DowntimeS/obs
			}
		}
		if st.Availability < 0 {
			st.Availability = 0
		}
	}
	return st
}

// inContainer is replaced in tests.
var inContainer = func() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// SetupWarnings spots the two installation mistakes that make every measurement misleading.
func SetupWarnings(gateway, iface string) []string {
	var w []string
	if ip := net.ParseIP(gateway); ip != nil && inContainer() {
		if _, docker, _ := net.ParseCIDR("172.16.0.0/12"); docker.Contains(ip) {
			w = append(w, "docker_bridge") // the container is not using the host network
		}
	}
	if strings.HasPrefix(iface, "wl") {
		w = append(w, "wifi") // the probe measures its own Wi-Fi link too
	}
	return w
}

// Location returns the display time zone.
func (m *Monitor) Location() *time.Location { return m.loc }

// Config returns the configuration.
func (m *Monitor) Config() config.Config { return m.cfg }

// Notifier returns the alert sender (may have no channel).
func (m *Monitor) Notifier() *notify.Notifier { return m.notif }
