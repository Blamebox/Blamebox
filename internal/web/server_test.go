package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/demo"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/monitor"
	"github.com/blamebox/blamebox/internal/notify"
	"github.com/blamebox/blamebox/internal/store"
)

// demoServer runs the dashboard on a simulated week.
func demoServer(t *testing.T, cfg config.Config) (*httptest.Server, time.Time) {
	t.Helper()
	loc, _ := time.LoadLocation("Europe/Paris")
	now := time.Date(2026, 11, 8, 23, 0, 0, 0, loc)
	cfg.Demo = true
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	d := demo.New(loc, now)
	m, err := monitor.New(cfg, d, st, notify.New(config.Config{Lang: "fr"}, loc), loc, now)
	if err != nil {
		t.Fatal(err)
	}
	m.Backfill(context.Background(), d.History(now.Add(-7*24*time.Hour), now, cfg.Interval))
	srv := httptest.NewServer(withClock(m, now))
	t.Cleanup(srv.Close)
	return srv, now
}

// withClock pins "now" so the test does not depend on the wall clock.
func withClock(m *monitor.Monitor, now time.Time) http.Handler {
	s := &Server{mon: m, now: func() time.Time { return now }, auth: m.Config().Auth}
	return s.routes()
}

func get(t *testing.T, url string, v any) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("%s: %v", url, err)
		}
	}
	return resp
}

func TestAPIOnDemoWeek(t *testing.T) {
	srv, _ := demoServer(t, config.Default())

	var snap monitor.Snapshot
	get(t, srv.URL+"/api/status", &snap)
	if snap.Last == nil || !snap.Info.Demo || snap.Texts["fr"].Title == "" || snap.Texts["en"].Title == "" {
		t.Fatalf("bad snapshot %+v", snap)
	}
	if snap.Stats7d.Outages == 0 || snap.Stats7d.Availability <= 0.9 || snap.Stats7d.Availability >= 1 {
		t.Fatalf("a demo week has outages: %+v", snap.Stats7d)
	}

	var ser Series
	get(t, srv.URL+"/api/samples?hours=24&points=288", &ser)
	if len(ser.T) != 288 || len(ser.Status) != 288 || ser.BucketS != 300 {
		t.Fatalf("series: %d points, bucket %d", len(ser.T), ser.BucketS)
	}

	var incs []IncidentView
	get(t, srv.URL+"/api/incidents?days=8", &incs)
	if len(incs) == 0 {
		t.Fatal("no incident in a demo week")
	}
	for i := 1; i < len(incs); i++ {
		if incs[i].Start.After(incs[i-1].Start) {
			t.Fatal("incidents must be newest first")
		}
	}
	if incs[0].Texts["fr"].Short == "" {
		t.Fatal("incidents carry their texts")
	}

	resp := get(t, srv.URL+"/api/export/incidents.csv", nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("csv content type %q", ct)
	}

	r, _ := http.Get(srv.URL + "/report?days=7")
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	page := string(body)
	for _, want := range []string{"incidents de connexion", "Diagnostic", "Message prêt à envoyer", "EXEMPLE"} {
		if !strings.Contains(page, want) {
			t.Errorf("report should contain %q", want)
		}
	}
	r, _ = http.Get(srv.URL + "/report?days=7&lang=en")
	body, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(body), "Connection incident report") {
		t.Error("English report")
	}

	r, _ = http.Get(srv.URL + "/")
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("index: %d, CSP %q", r.StatusCode, r.Header.Get("Content-Security-Policy"))
	}
	r.Body.Close()
}

func TestMetrics(t *testing.T) {
	srv, _ := demoServer(t, config.Default())
	r, _ := http.Get(srv.URL + "/metrics")
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	for _, want := range []string{"blamebox_up ", "blamebox_layer_ok{layer=\"box\"}", "blamebox_rtt_milliseconds{target=\"192.168.1.254\",role=\"gateway\"}",
		"blamebox_availability_7d_ratio", "# TYPE blamebox_up gauge"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics should contain %q:\n%s", want, b)
		}
	}
}

func TestTestAlertNeedsHeader(t *testing.T) {
	srv, _ := demoServer(t, config.Default())
	resp, _ := http.Post(srv.URL+"/api/test-alert", "text/plain", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site POST must be refused, got %d", resp.StatusCode)
	}
}

func TestAuth(t *testing.T) {
	cfg := config.Default()
	cfg.Auth = "admin:s3cret"
	srv, _ := demoServer(t, cfg)
	if r, _ := http.Get(srv.URL + "/api/status"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/api/status", nil)
	req.SetBasicAuth("admin", "s3cret")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 200 {
		t.Fatalf("expected 200 with the password, got %d", r.StatusCode)
	}
	if r, _ := http.Get(srv.URL + "/healthz"); r.StatusCode != 200 {
		t.Fatal("healthz stays open for container health checks")
	}
}

func TestBuildSeriesGaps(t *testing.T) {
	from := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	s := []model.Sample{{T: from.Add(10 * time.Minute), Gateway: model.PingResult{Sent: 3, Recv: 3, RTTms: 1},
		Diag: model.Diagnosis{Status: model.StatusDown, Cause: model.CauseISP}}}
	ser := BuildSeries(s, from, from.Add(time.Hour), 12)
	if ser.Status[0] != -1 || ser.Status[2] != 2 || ser.Cause[2] != "isp" || ser.NetLoss[2] != -1 {
		t.Fatalf("gaps and outages: %+v", ser)
	}
}
