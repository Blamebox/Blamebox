// Package demo simulates a realistic connection, with evening congestion and outages,
// so BlameBox can be tried (and shown) without waiting for a real failure.
package demo

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/probe"
)

const (
	gateway = "192.168.1.254"
	ipA     = "203.0.113.42"
	ipB     = "203.0.113.87"
)

var (
	targets   = []string{"1.1.1.1", "9.9.9.9"}
	resolvers = []string{"1.1.1.1", "9.9.9.9"}
)

// outage is a scripted failure.
type outage struct {
	start, end time.Time
	cause      model.Cause
	flipped    bool // the public IP already changed at the end of this outage
}

// Demo is a fake prober. Its history is deterministic: the same day always has the same outages.
type Demo struct {
	loc   *time.Location
	start time.Time

	mu     sync.Mutex
	rng    *rand.Rand
	script []outage
	days   map[string]bool
	ipNow  string
}

// New prepares a demo whose live part starts at now.
func New(loc *time.Location, now time.Time) *Demo {
	return &Demo{loc: loc, start: now, rng: rand.New(rand.NewPCG(1, uint64(now.Unix()))), days: map[string]bool{}, ipNow: ipA}
}

// Info implements probe.Prober.
func (d *Demo) Info() probe.Info {
	return probe.Info{PingMethod: probe.MethodRaw, Gateway: gateway, Iface: "eth0", DNSLocal: gateway,
		Targets: targets, DNSPublic: resolvers, WebURLs: []string{"https://www.cloudflare.com/cdn-cgi/trace"}, Demo: true}
}

// Probe implements probe.Prober.
func (d *Demo) Probe(_ context.Context, now time.Time) (model.Sample, []model.Event) {
	return d.Sample(now), nil
}

// History returns simulated cycles from from to to.
func (d *Demo) History(from, to time.Time, step time.Duration) []model.Sample {
	var out []model.Sample
	for t := from; t.Before(to); t = t.Add(step) {
		out = append(out, d.Sample(t))
	}
	return out
}

// dayScript plans the outages of one local day, deterministically.
func (d *Demo) dayScript(day time.Time) []outage {
	h := fnv.New64a()
	h.Write([]byte(day.Format("2006-01-02")))
	r := rand.New(rand.NewPCG(h.Sum64(), 42))
	at := func(hour, min int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), hour, min, 0, 0, d.loc)
	}
	var out []outage
	// Most evenings: one or two ISP-side outages during the busy hours.
	if r.IntN(10) < 7 {
		n := 1 + r.IntN(2)
		for i := 0; i < n; i++ {
			s := at(20+r.IntN(3), r.IntN(60))
			out = append(out, outage{start: s, end: s.Add(time.Duration(4+r.IntN(22)) * time.Minute), cause: model.CauseISP})
		}
	}
	// Sometimes the ISP resolver fails.
	if r.IntN(10) < 2 {
		s := at(8+r.IntN(12), r.IntN(60))
		out = append(out, outage{start: s, end: s.Add(time.Duration(6+r.IntN(10)) * time.Minute), cause: model.CauseDNSISP})
	}
	// Rarely, the router restarts (home side).
	if r.IntN(10) < 1 {
		s := at(9+r.IntN(10), r.IntN(60))
		out = append(out, outage{start: s, end: s.Add(3 * time.Minute), cause: model.CauseLAN})
	}
	return out
}

func (d *Demo) ensureDay(t time.Time) {
	lt := t.In(d.loc)
	key := lt.Format("2006-01-02")
	if d.days[key] {
		return
	}
	d.days[key] = true
	day := time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, d.loc)
	d.script = append(d.script, d.dayScript(day)...)
	// A live outage one minute after start, then every 20 minutes, so visitors see one happen.
	if !d.days["live"] {
		d.days["live"] = true
		for s := d.start.Add(time.Minute); s.Before(d.start.Add(14 * 24 * time.Hour)); s = s.Add(20 * time.Minute) {
			d.script = append(d.script, outage{start: s, end: s.Add(3 * time.Minute), cause: model.CauseISP})
		}
	}
	sort.Slice(d.script, func(i, j int) bool { return d.script[i].start.Before(d.script[j].start) })
}

func (d *Demo) causeAt(t time.Time) (model.Cause, *outage) {
	for i := range d.script {
		o := &d.script[i]
		if !t.Before(o.start) && t.Before(o.end) {
			return o.cause, o
		}
	}
	return model.CauseNone, nil
}

// congestion returns the evening packet-loss probability and extra latency for a moment.
func (d *Demo) congestion(t time.Time) (loss float64, extraMs float64) {
	lt := t.In(d.loc)
	h := float64(lt.Hour()) + float64(lt.Minute())/60
	// A bell curve centred on 21:30, stronger on some days.
	peak := math.Exp(-math.Pow((h-21.5)/1.2, 2))
	hsh := fnv.New32a()
	hsh.Write([]byte(lt.Format("2006-01-02")))
	strength := []float64{0.02, 0.06, 0.09, 0.04, 0.11, 0.07, 0.03}[hsh.Sum32()%7]
	return strength * peak, 28 * peak
}

// Sample synthesises one cycle.
func (d *Demo) Sample(t time.Time) model.Sample {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureDay(t.Add(-24 * time.Hour))
	d.ensureDay(t)
	cause, o := d.causeAt(t)
	lossP, extra := d.congestion(t)
	r := d.rng

	burst := func(target string, base, jitter, loss float64, up bool) model.PingResult {
		p := model.PingResult{Target: target, Sent: 3, RTTms: -1, Method: probe.MethodRaw}
		if !up {
			p.Err = "no reply"
			return p
		}
		var rtts []float64
		for i := 0; i < 3; i++ {
			if r.Float64() >= loss {
				rtts = append(rtts, base+r.Float64()*jitter)
			}
		}
		p.Recv = len(rtts)
		if len(rtts) > 0 {
			sort.Float64s(rtts)
			p.RTTms = math.Round(rtts[len(rtts)/2]*100) / 100
		} else {
			p.Err = "no reply"
		}
		return p
	}
	gwUp := cause != model.CauseLAN
	netUp := gwUp && cause != model.CauseISP
	s := model.Sample{T: t}
	s.Gateway = burst(gateway, 0.5, 0.6, 0.002, gwUp)
	s.Gateway.Target = gateway
	for _, tg := range targets {
		s.Internet = append(s.Internet, burst(tg, 9+extra*0.7, 3+extra*0.3, lossP, netUp))
	}
	localOK := netUp && cause != model.CauseDNSISP
	s.DNSLocal = model.DNSResult{Server: gateway, Name: "www.wikipedia.org", OK: localOK, Ms: 12 + r.Float64()*8}
	if !localOK {
		s.DNSLocal.Err, s.DNSLocal.Ms = "timeout", 2000
	}
	for _, srv := range resolvers {
		res := model.DNSResult{Server: srv, Name: "www.wikipedia.org", OK: netUp, Ms: 10 + extra*0.5 + r.Float64()*6}
		if !netUp {
			res.Err, res.Ms = "timeout", 2000
		}
		s.DNSPub = append(s.DNSPub, res)
	}
	s.Web = model.HTTPResult{URL: "https://www.cloudflare.com/cdn-cgi/trace", OK: netUp, Status: 200, Ms: 35 + extra + r.Float64()*20}
	if !netUp {
		s.Web = model.HTTPResult{URL: s.Web.URL, Err: "timeout", Ms: 4000}
	}
	// Long ISP outages end with a new public IP, as when the line resynchronises.
	if o != nil && !o.flipped && o.cause == model.CauseISP && o.end.Sub(o.start) > 15*time.Minute && t.Add(time.Minute).After(o.end) {
		o.flipped = true
		if d.ipNow == ipA {
			d.ipNow = ipB
		} else {
			d.ipNow = ipA
		}
	}
	if netUp {
		s.PublicIP = d.ipNow
	}
	return s
}
