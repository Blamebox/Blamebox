package monitor

import (
	"context"
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/notify"
	"github.com/blamebox/blamebox/internal/probe"
	"github.com/blamebox/blamebox/internal/store"
)

// scripted returns raw measurements: the line is down between down and up.
type scripted struct{ down, up time.Time }

func (p scripted) Info() probe.Info { return probe.Info{PingMethod: "test"} }

func (p scripted) Probe(_ context.Context, now time.Time) (model.Sample, []model.Event) {
	ok := now.Before(p.down) || !now.Before(p.up)
	ping := func(up bool) model.PingResult {
		if up {
			return model.PingResult{Sent: 3, Recv: 3, RTTms: 10}
		}
		return model.PingResult{Sent: 3, RTTms: -1}
	}
	return model.Sample{T: now, Gateway: ping(true), Internet: []model.PingResult{ping(ok)},
		DNSLocal: model.DNSResult{OK: ok}, DNSPub: []model.DNSResult{{OK: ok}}, Web: model.HTTPResult{OK: ok},
		PublicIP: map[bool]string{true: "203.0.113.1", false: ""}[ok]}, nil
}

func TestMonitorEndToEndAndRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	start := time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)
	p := scripted{down: start.Add(5 * time.Minute), up: start.Add(17 * time.Minute)}

	st, _ := store.Open(dir)
	m, err := New(cfg, p, st, notify.New(cfg, time.UTC), time.UTC, start)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := 0; i < 30; i++ { // 15 minutes: the outage is ongoing at the end
		m.Step(ctx, start.Add(time.Duration(i)*30*time.Second))
	}
	now := start.Add(15 * time.Minute)
	sn := m.Snapshot(now)
	if sn.Diag.Cause != model.CauseISP || sn.Outage == nil || !sn.Outage.Start.Equal(p.down) {
		t.Fatalf("expected an ongoing ISP outage since %v, got %+v / %+v", p.down, sn.Diag, sn.Outage)
	}
	st.Close()

	// Restart quickly: the ongoing outage is restored and later closed normally.
	st2, _ := store.Open(dir)
	m2, _ := New(cfg, p, st2, notify.New(cfg, time.UTC), time.UTC, now)
	if m2.Snapshot(now).Outage == nil {
		t.Fatal("the ongoing outage should survive a restart")
	}
	for i := 30; i < 40; i++ {
		m2.Step(ctx, start.Add(time.Duration(i)*30*time.Second))
	}
	incs := m2.Incidents(start, start.Add(time.Hour))
	if len(incs) != 1 || incs[0].End == nil || !incs[0].End.Equal(p.up) {
		t.Fatalf("expected one closed outage ending at %v, got %+v", p.up, incs)
	}
	stats := m2.Stats(start, start.Add(20*time.Minute))
	if stats.Outages != 1 || stats.DowntimeS != 12*60 || stats.ISPDowntimeS != 12*60 {
		t.Fatalf("stats %+v", stats)
	}
	st2.Close()

	// A long gap closes an ongoing outage where the data stops.
	dir3 := t.TempDir()
	st3, _ := store.Open(dir3)
	m3, _ := New(cfg, p, st3, nil, time.UTC, start)
	for i := 0; i < 20; i++ {
		m3.Step(ctx, start.Add(time.Duration(i)*30*time.Second))
	}
	st3.Close()
	st4, _ := store.Open(dir3)
	m4, _ := New(cfg, p, st4, nil, time.UTC, start.Add(3*time.Hour))
	got := m4.Incidents(start, start.Add(4*time.Hour))
	if len(got) != 1 || got[0].End == nil || got[0].Note != "interrupted" {
		t.Fatalf("expected the outage closed as interrupted, got %+v", got)
	}
}

func TestSetupWarnings(t *testing.T) {
	orig := inContainer
	inContainer = func() bool { return true }
	defer func() { inContainer = orig }()
	if w := SetupWarnings("172.17.0.1", "eth0"); len(w) != 1 || w[0] != "docker_bridge" {
		t.Fatalf("docker bridge not spotted: %v", w)
	}
	if w := SetupWarnings("192.168.1.1", "wlan0"); len(w) != 1 || w[0] != "wifi" {
		t.Fatalf("wifi not spotted: %v", w)
	}
	if w := SetupWarnings("192.168.1.1", "eth0"); len(w) != 0 {
		t.Fatalf("false alarm: %v", w)
	}
}
