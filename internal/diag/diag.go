// Package diag turns raw measurements into a verdict: which layer failed, and why.
package diag

import "github.com/blamebox/blamebox/internal/model"

// Window summarises the recent cycles, used to detect packet loss that a single cycle
// (a handful of probes) cannot measure reliably.
type Window struct {
	N            int     `json:"n"`        // cycles in the window
	GatewayLoss  float64 `json:"gw_loss"`  // 0–1
	InternetLoss float64 `json:"net_loss"` // 0–1
}

// MinWindow is the number of cycles needed before packet loss is judged.
const MinWindow = 10

// RollingWindow computes the window over the given samples (oldest first).
func RollingWindow(samples []model.Sample) Window {
	var w Window
	gs, gr, is, ir := 0, 0, 0, 0
	for _, s := range samples {
		if s.Diag.Status == model.StatusDown {
			continue // outages are counted as incidents, not as loss
		}
		w.N++
		gs += s.Gateway.Sent
		gr += s.Gateway.Recv
		for _, p := range s.Internet {
			is += p.Sent
			ir += p.Recv
		}
	}
	if gs > 0 {
		w.GatewayLoss = float64(gs-gr) / float64(gs)
	}
	if is > 0 {
		w.InternetLoss = float64(is-ir) / float64(is)
	}
	return w
}

// Evaluate returns the verdict for one cycle.
//
// The layers are checked in order: router, Internet, DNS, web. The first layer that fails
// explains the outage; the following ones are not judged ("skip").
func Evaluate(s model.Sample, w Window, lossThreshold float64) model.Diagnosis {
	d := model.Diagnosis{Status: model.StatusOK, Cause: model.CauseNone,
		Layers: model.Layers{Box: model.LayerOK, Internet: model.LayerOK, DNS: model.LayerOK, Web: model.LayerOK}}
	down := func(c model.Cause) model.Diagnosis {
		d.Status, d.Cause = model.StatusDown, c
		return d
	}

	// Evidence that the Internet is reachable, whatever the protocol.
	internetReach := s.InternetOK() || s.PublicDNSOK() || s.Web.OK

	// 1. Router.
	if !s.Gateway.OK() {
		if !internetReach {
			d.Layers = model.Layers{Box: model.LayerFail, Internet: model.LayerSkip, DNS: model.LayerSkip, Web: model.LayerSkip}
			return down(model.CauseLAN)
		}
		d.Layers.Box = model.LayerWarn // the router ignores our probes but obviously works
	}

	// 2. Internet.
	if !internetReach {
		d.Layers.Internet, d.Layers.DNS, d.Layers.Web = model.LayerFail, model.LayerSkip, model.LayerSkip
		return down(model.CauseISP)
	}
	if !s.InternetOK() {
		d.Layers.Internet = model.LayerWarn // pings filtered somewhere, but traffic flows
	}

	// 3. DNS.
	localOK := s.DNSLocal.OK || s.DNSLocal.Skip
	pubOK := s.PublicDNSOK()
	switch {
	case !localOK && !pubOK:
		d.Layers.DNS, d.Layers.Web = model.LayerFail, model.LayerSkip
		return down(model.CauseDNSAll)
	case !localOK:
		d.Layers.DNS = model.LayerFail
		if !s.Web.OK {
			d.Layers.Web = model.LayerSkip
		}
		return down(model.CauseDNSISP)
	case !pubOK:
		d.Layers.DNS = model.LayerWarn // public resolvers unreachable, but the one devices use works
	}

	// 4. Web.
	if !s.Web.OK {
		if !pubOK { // the web check resolves names through public resolvers: cannot judge
			d.Layers.Web = model.LayerSkip
		} else {
			d.Layers.Web = model.LayerFail
			return down(model.CauseWeb)
		}
	}

	// 5. Quality, judged on the rolling window.
	if w.N >= MinWindow {
		switch {
		case w.GatewayLoss >= lossThreshold:
			d.Status, d.Cause = model.StatusDegraded, model.CauseLossLAN
			d.Layers.Box = model.LayerWarn
		case w.InternetLoss >= lossThreshold && w.InternetLoss-w.GatewayLoss >= lossThreshold/2:
			d.Status, d.Cause = model.StatusDegraded, model.CauseLossISP
			d.Layers.Internet = model.LayerWarn
		}
	}
	return d
}
