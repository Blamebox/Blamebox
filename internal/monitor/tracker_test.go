package monitor

import (
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

var t0 = time.Date(2026, 11, 8, 21, 0, 0, 0, time.UTC)

func at(i int) time.Time { return t0.Add(time.Duration(i) * 30 * time.Second) }

func sample(i int, st model.Status, c model.Cause, gwOK bool) model.Sample {
	gw := model.PingResult{Sent: 3, Recv: 3}
	if !gwOK {
		gw.Recv = 0
	}
	inet := model.PingResult{Sent: 3, Recv: 3}
	if st == model.StatusDown {
		inet.Recv = 0
	}
	return model.Sample{T: at(i), Gateway: gw, Internet: []model.PingResult{inet}, Diag: model.Diagnosis{Status: st, Cause: c}}
}

func ok(i int) model.Sample { return sample(i, model.StatusOK, model.CauseNone, true) }
func isp(i int) model.Sample {
	return sample(i, model.StatusDown, model.CauseISP, true)
}

func newTracker() *Tracker {
	return &Tracker{OpenAfter: 3, CloseAfter: 2, DegOpenAfter: 2, DegCloseAfter: 4}
}

func TestBlipIsNotAnIncident(t *testing.T) {
	tr := newTracker()
	tr.Observe(ok(0))
	tr.Observe(isp(1))
	tr.Observe(isp(2))
	ch := tr.Observe(ok(3))
	if tr.Outage != nil || len(ch.Opened) != 0 {
		t.Fatal("a 2-cycle blip must not open an incident")
	}
	if len(ch.Events) != 1 || ch.Events[0].Kind != "blip" || !ch.Events[0].T.Equal(at(1)) {
		t.Fatalf("expected one blip event at the first failing cycle, got %+v", ch.Events)
	}
}

func TestOutageLifecycle(t *testing.T) {
	tr := newTracker()
	tr.Observe(ok(0))
	tr.Observe(isp(1))
	tr.Observe(isp(2))
	ch := tr.Observe(isp(3))
	if len(ch.Opened) != 1 {
		t.Fatalf("outage should open on the 3rd failing cycle")
	}
	inc := ch.Opened[0]
	if !inc.Start.Equal(at(1)) || inc.Cause != model.CauseISP || inc.Cycles != 3 {
		t.Fatalf("bad incident %+v", inc)
	}
	// A single good cycle does not close it, and a new failure resets the count.
	tr.Observe(ok(4))
	tr.Observe(sample(5, model.StatusDown, model.CauseLAN, false))
	tr.Observe(ok(6))
	ch = tr.Observe(ok(7))
	if len(ch.Closed) != 1 {
		t.Fatalf("outage should close after 2 good cycles")
	}
	c := ch.Closed[0]
	if c.End == nil || !c.End.Equal(at(6)) {
		t.Fatalf("end should be the first good cycle, got %v", c.End)
	}
	if c.Cycles != 4 || c.Counts[model.CauseISP] != 3 || c.Counts[model.CauseLAN] != 1 || c.Cause != model.CauseISP {
		t.Fatalf("bad counts %+v cause %s", c.Counts, c.Cause)
	}
	if c.Evidence.GatewayAnswered != 3 {
		t.Fatalf("router answered during 3 of 4 failing cycles, got %d", c.Evidence.GatewayAnswered)
	}
	if got := c.Duration(time.Time{}); got != 2*time.Minute+30*time.Second {
		t.Fatalf("duration %s", got)
	}
	if tr.Outage != nil {
		t.Fatal("tracker should be clear")
	}
}

func TestDegradation(t *testing.T) {
	tr := newTracker()
	deg := func(i int) model.Sample { return sample(i, model.StatusDegraded, model.CauseLossISP, true) }
	tr.Observe(deg(0))
	ch := tr.Observe(deg(1))
	if len(ch.Opened) != 1 || ch.Opened[0].Kind != "degradation" {
		t.Fatal("degradation should open after 2 degraded cycles")
	}
	for i := 2; i < 5; i++ {
		if ch = tr.Observe(ok(i)); len(ch.Closed) != 0 {
			t.Fatal("closed too early")
		}
	}
	if ch = tr.Observe(ok(5)); len(ch.Closed) != 1 || !ch.Closed[0].End.Equal(at(2)) {
		t.Fatalf("degradation should close after 4 good cycles, ending at the first one: %+v", ch.Closed)
	}
}

func TestIPChange(t *testing.T) {
	tr := newTracker()
	a := ok(0)
	a.PublicIP = "203.0.113.1"
	tr.Observe(a)
	b := ok(1)
	b.PublicIP = "203.0.113.9"
	ch := tr.Observe(b)
	if len(ch.Events) != 1 || ch.Events[0].Kind != "ip_change" {
		t.Fatalf("expected ip_change, got %+v", ch.Events)
	}
}

func TestInterrupt(t *testing.T) {
	tr := newTracker()
	for i := 0; i < 4; i++ {
		tr.Observe(isp(i))
	}
	out := tr.Interrupt(at(10), "interrupted")
	if len(out) != 1 || out[0].End == nil || !out[0].End.Equal(at(10)) || out[0].Note != "interrupted" || tr.Outage != nil {
		t.Fatalf("bad interrupt result %+v", out)
	}
}

func TestMainCauseTieBreak(t *testing.T) {
	if c := mainCause(map[model.Cause]int{model.CauseWeb: 2, model.CauseISP: 2}); c != model.CauseISP {
		t.Fatalf("tie should go to the most severe cause, got %s", c)
	}
}
