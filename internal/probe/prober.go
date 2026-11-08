package probe

import (
	"context"
	"sync"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/model"
)

// Prober runs one measurement cycle.
type Prober interface {
	Probe(ctx context.Context, now time.Time) (model.Sample, []model.Event)
	Info() Info
}

// Info describes how the measurements are taken, for the interface.
type Info struct {
	PingMethod       string   `json:"ping_method"`
	Gateway          string   `json:"gateway"`
	Iface            string   `json:"iface,omitempty"`
	DNSLocal         string   `json:"dns_local"`
	DNSLocalDisabled bool     `json:"dns_local_disabled"`
	Targets          []string `json:"targets"`
	DNSPublic        []string `json:"dns_public"`
	WebURLs          []string `json:"web_urls"`
	Demo             bool     `json:"demo"`
}

// localDNSGiveUp is the number of cycles after which an "auto" local resolver that never
// answered (while public ones did) is considered absent rather than broken.
const localDNSGiveUp = 10

// Real measures the actual network.
type Real struct {
	cfg    config.Config
	pinger *Pinger
	web    *WebCheck

	mu                sync.Mutex
	nameIdx           int
	gateway, iface    string
	localEverOK       bool
	localFailWhilePub int
	localDisabled     bool
}

// NewReal prepares the real prober.
func NewReal(cfg config.Config) *Real {
	return &Real{cfg: cfg, pinger: NewPinger(), web: NewWebCheck(cfg.DNSPublic, 4*time.Second)}
}

// Info implements Prober.
func (r *Real) Info() Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Info{
		PingMethod:       r.pinger.Method,
		Gateway:          r.gateway,
		Iface:            r.iface,
		DNSLocal:         r.localServer(r.gateway),
		DNSLocalDisabled: r.localDisabled,
		Targets:          r.cfg.InternetTargets,
		DNSPublic:        r.cfg.DNSPublic,
		WebURLs:          r.cfg.WebURLs,
	}
}

func (r *Real) localServer(gw string) string {
	switch r.cfg.DNSLocal {
	case "off", "":
		return ""
	case "auto":
		return gw
	default:
		return r.cfg.DNSLocal
	}
}

// Probe implements Prober.
func (r *Real) Probe(ctx context.Context, now time.Time) (model.Sample, []model.Event) {
	var events []model.Event
	gw, iface := r.cfg.Gateway, ""
	var gwErr error
	if gw == "" {
		gw, iface, gwErr = DefaultGateway()
	}

	r.mu.Lock()
	name := r.cfg.DNSNames[r.nameIdx%len(r.cfg.DNSNames)]
	alt := r.cfg.DNSNames[(r.nameIdx+1)%len(r.cfg.DNSNames)]
	r.nameIdx++
	r.gateway, r.iface = gw, iface
	local := r.localServer(gw)
	localDisabled := r.localDisabled
	r.mu.Unlock()

	s := model.Sample{T: now, Internet: make([]model.PingResult, len(r.cfg.InternetTargets)),
		DNSPub: make([]model.DNSResult, len(r.cfg.DNSPublic))}
	var wg sync.WaitGroup
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	run(func() {
		if gw == "" {
			msg := "no default route"
			if gwErr != nil {
				msg = gwErr.Error()
			}
			s.Gateway = model.PingResult{Target: "", RTTms: -1, Method: r.pinger.Method, Err: msg}
			return
		}
		s.Gateway = r.pinger.Ping(ctx, gw, r.cfg.PingCount, r.cfg.PingTimeout)
	})
	for i, t := range r.cfg.InternetTargets {
		run(func() { s.Internet[i] = r.pinger.Ping(ctx, t, r.cfg.PingCount, r.cfg.PingTimeout) })
	}
	lookup := func(server string) model.DNSResult {
		res := LookupVia(ctx, server, name, 2*time.Second)
		if !res.OK && res.Err != "timeout" && res.Err != "dns timeout" {
			// The name itself may be the problem: try another one before blaming the server.
			if res2 := LookupVia(ctx, server, alt, 2*time.Second); res2.OK {
				return res2
			}
		}
		return res
	}
	run(func() {
		switch {
		case local == "":
			s.DNSLocal = model.DNSResult{Skip: true}
		case localDisabled:
			s.DNSLocal = model.DNSResult{Server: local, Skip: true}
		default:
			s.DNSLocal = lookup(local)
		}
	})
	for i, srv := range r.cfg.DNSPublic {
		run(func() { s.DNSPub[i] = lookup(srv) })
	}
	run(func() {
		for _, u := range r.cfg.WebURLs {
			res, ip := r.web.Get(ctx, u)
			s.Web = res
			if ip != "" {
				s.PublicIP = ip
			}
			if res.OK {
				return
			}
		}
	})
	wg.Wait()

	// An "auto" local resolver that never answered while public ones did is probably absent.
	if r.cfg.DNSLocal == "auto" && !s.DNSLocal.Skip {
		r.mu.Lock()
		switch {
		case s.DNSLocal.OK:
			r.localEverOK = true
		case !r.localEverOK && s.PublicDNSOK():
			r.localFailWhilePub++
			if r.localFailWhilePub >= localDNSGiveUp {
				r.localDisabled = true
				events = append(events, model.Event{T: now, Kind: "dns_local_disabled", Detail: local})
			}
		}
		r.mu.Unlock()
	}
	return s, events
}
