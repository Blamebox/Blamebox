package diag

import (
	"testing"

	"github.com/blamebox/blamebox/internal/model"
)

func ping(ok bool) model.PingResult {
	if ok {
		return model.PingResult{Sent: 3, Recv: 3, RTTms: 10}
	}
	return model.PingResult{Sent: 3, Recv: 0, RTTms: -1}
}

func dns(ok bool) model.DNSResult { return model.DNSResult{OK: ok} }

// smp builds a sample: router, public IPs, router DNS, public DNS, web.
func smp(gw, inet, dnsl, dnsp, web bool) model.Sample {
	return model.Sample{
		Gateway:  ping(gw),
		Internet: []model.PingResult{ping(inet), ping(inet)},
		DNSLocal: dns(dnsl),
		DNSPub:   []model.DNSResult{dns(dnsp), dns(dnsp)},
		Web:      model.HTTPResult{OK: web},
	}
}

func TestEvaluate(t *testing.T) {
	skipLocal := smp(true, true, false, true, true)
	skipLocal.DNSLocal = model.DNSResult{Skip: true}
	onePublicIPDown := smp(true, true, true, true, true)
	onePublicIPDown.Internet[1] = ping(false)

	cases := []struct {
		name   string
		s      model.Sample
		status model.Status
		cause  model.Cause
		layers model.Layers
	}{
		{"all good", smp(true, true, true, true, true), model.StatusOK, model.CauseNone,
			model.Layers{Box: "ok", Internet: "ok", DNS: "ok", Web: "ok"}},
		{"router down", smp(false, false, false, false, false), model.StatusDown, model.CauseLAN,
			model.Layers{Box: "fail", Internet: "skip", DNS: "skip", Web: "skip"}},
		{"router ignores ping but Internet works", smp(false, true, true, true, true), model.StatusOK, model.CauseNone,
			model.Layers{Box: "warn", Internet: "ok", DNS: "ok", Web: "ok"}},
		{"isp outage", smp(true, false, true, false, false), model.StatusDown, model.CauseISP,
			model.Layers{Box: "ok", Internet: "fail", DNS: "skip", Web: "skip"}},
		{"isp outage even if the router DNS answers from cache", smp(true, false, true, false, false), model.StatusDown, model.CauseISP,
			model.Layers{Box: "ok", Internet: "fail", DNS: "skip", Web: "skip"}},
		{"ICMP filtered upstream but traffic flows", smp(true, false, true, true, true), model.StatusOK, model.CauseNone,
			model.Layers{Box: "ok", Internet: "warn", DNS: "ok", Web: "ok"}},
		{"router DNS down", smp(true, true, false, true, true), model.StatusDown, model.CauseDNSISP,
			model.Layers{Box: "ok", Internet: "ok", DNS: "fail", Web: "ok"}},
		{"every DNS down", smp(true, true, false, false, false), model.StatusDown, model.CauseDNSAll,
			model.Layers{Box: "ok", Internet: "ok", DNS: "fail", Web: "skip"}},
		{"router without resolver", skipLocal, model.StatusOK, model.CauseNone,
			model.Layers{Box: "ok", Internet: "ok", DNS: "ok", Web: "ok"}},
		{"public DNS blocked, router DNS fine", smp(true, true, true, false, false), model.StatusOK, model.CauseNone,
			model.Layers{Box: "ok", Internet: "ok", DNS: "warn", Web: "skip"}},
		{"web down", smp(true, true, true, true, false), model.StatusDown, model.CauseWeb,
			model.Layers{Box: "ok", Internet: "ok", DNS: "ok", Web: "fail"}},
		{"one public IP down is not an outage", onePublicIPDown, model.StatusOK, model.CauseNone,
			model.Layers{Box: "ok", Internet: "ok", DNS: "ok", Web: "ok"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Evaluate(c.s, Window{}, 0.05)
			if d.Status != c.status || d.Cause != c.cause || d.Layers != c.layers {
				t.Errorf("got %s/%s %+v, want %s/%s %+v", d.Status, d.Cause, d.Layers, c.status, c.cause, c.layers)
			}
		})
	}
}

func TestEvaluateLoss(t *testing.T) {
	ok := smp(true, true, true, true, true)
	cases := []struct {
		name  string
		w     Window
		cause model.Cause
	}{
		{"too few cycles to judge", Window{N: 3, InternetLoss: 0.5}, model.CauseNone},
		{"isp loss", Window{N: 10, InternetLoss: 0.08}, model.CauseLossISP},
		{"lan loss wins", Window{N: 10, GatewayLoss: 0.1, InternetLoss: 0.12}, model.CauseLossLAN},
		{"loss explained by the LAN is not blamed on the ISP", Window{N: 10, GatewayLoss: 0.04, InternetLoss: 0.05}, model.CauseNone},
		{"below threshold", Window{N: 10, InternetLoss: 0.03}, model.CauseNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Evaluate(ok, c.w, 0.05)
			if d.Cause != c.cause {
				t.Errorf("cause %s, want %s", d.Cause, c.cause)
			}
			if c.cause != model.CauseNone && d.Status != model.StatusDegraded {
				t.Errorf("status %s, want degraded", d.Status)
			}
		})
	}
	// An outage is never hidden by a degradation.
	if d := Evaluate(smp(true, false, true, false, false), Window{N: 10, InternetLoss: 0.5}, 0.05); d.Cause != model.CauseISP {
		t.Errorf("outage hidden: %s", d.Cause)
	}
}

func TestRollingWindowIgnoresOutages(t *testing.T) {
	good := smp(true, true, true, true, true)
	lossy := good
	lossy.Internet = []model.PingResult{{Sent: 3, Recv: 2}, {Sent: 3, Recv: 3}}
	down := smp(true, false, true, false, false)
	down.Diag.Status = model.StatusDown
	w := RollingWindow([]model.Sample{good, lossy, down})
	if w.N != 2 {
		t.Fatalf("N = %d, want 2", w.N)
	}
	if want := 1.0 / 12; w.InternetLoss < want-1e-9 || w.InternetLoss > want+1e-9 {
		t.Errorf("internet loss %f, want %f", w.InternetLoss, want)
	}
}

func TestExplainCoversEveryCause(t *testing.T) {
	for _, lang := range Langs() {
		for _, c := range []model.Cause{model.CauseNone, model.CauseLAN, model.CauseISP, model.CauseDNSISP,
			model.CauseDNSAll, model.CauseWeb, model.CauseLossLAN, model.CauseLossISP} {
			tx := texts[lang][c]
			if tx.Title == "" || tx.Body == "" || tx.Short == "" || tx.Side == "" {
				t.Errorf("%s/%s: incomplete text", lang, c)
			}
			if c != model.CauseNone && tx.Advice == "" {
				t.Errorf("%s/%s: missing advice", lang, c)
			}
		}
	}
	if Explain(model.CauseISP, "de").Title != texts["fr"][model.CauseISP].Title {
		t.Error("unknown language should fall back to French")
	}
}
