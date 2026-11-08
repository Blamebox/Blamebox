// Package web serves the dashboard, the JSON API and the report.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/blamebox/blamebox/internal/diag"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/monitor"
	"github.com/blamebox/blamebox/internal/report"
)

//go:embed static
var staticFS embed.FS

// Server wires the monitor to HTTP.
type Server struct {
	mon  *monitor.Monitor
	now  func() time.Time
	auth string
}

// New returns the HTTP handler.
func New(mon *monitor.Monitor) http.Handler {
	s := &Server{mon: mon, now: time.Now, auth: mon.Config().Auth}
	return s.routes()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServer(http.FS(static)), time.Hour)))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		b, _ := fs.ReadFile(static, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/samples", s.samples)
	mux.HandleFunc("GET /api/incidents", s.incidents)
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/export/incidents.csv", s.exportCSV)
	mux.HandleFunc("POST /api/test-alert", s.testAlert)
	mux.HandleFunc("GET /report", s.report)
	mux.HandleFunc("GET /metrics", s.metrics)
	return s.secure(mux)
}

// secure adds security headers and the optional password.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		if s.auth != "" && r.URL.Path != "/healthz" {
			user, pass, ok := r.BasicAuth()
			want := strings.SplitN(s.auth, ":", 2)
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(want[0])) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(want[1])) != 1 {
				h.Set("WWW-Authenticate", `Basic realm="BlameBox", charset="UTF-8"`)
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func cacheFor(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		log.Printf("api: %v", err)
	}
}

// intParam reads a bounded integer query parameter.
func intParam(r *http.Request, name string, def, min, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.mon.Snapshot(s.now()))
}

// Series is a down-sampled view of the measurements, one value per bucket.
type Series struct {
	BucketS int       `json:"bucket_s"`
	T       []int64   `json:"t"`       // bucket start, Unix seconds
	Status  []int     `json:"st"`      // worst status: 0 ok, 1 degraded, 2 down, -1 no data
	Cause   []string  `json:"c"`       // cause of the worst cycle
	GwRTT   []float64 `json:"gw_rtt"`  // median, ms (-1 when unknown)
	NetRTT  []float64 `json:"net_rtt"` // median, ms
	GwLoss  []float64 `json:"gw_loss"` // 0–1
	NetLoss []float64 `json:"net_loss"`
}

func statusRank(s model.Status) int {
	switch s {
	case model.StatusDown:
		return 2
	case model.StatusDegraded:
		return 1
	}
	return 0
}

func (s *Server) samples(w http.ResponseWriter, r *http.Request) {
	hours := intParam(r, "hours", 24, 1, 24*31)
	points := intParam(r, "points", 288, 24, 1440)
	now := s.now()
	from := now.Add(-time.Duration(hours) * time.Hour)
	writeJSON(w, BuildSeries(s.mon.Samples(from, now), from, now, points))
}

// BuildSeries groups samples into evenly spaced buckets.
func BuildSeries(samples []model.Sample, from, to time.Time, points int) Series {
	span := to.Sub(from)
	bucket := span / time.Duration(points)
	if bucket < time.Second {
		bucket = time.Second
	}
	n := int(math.Ceil(float64(span) / float64(bucket)))
	out := Series{BucketS: int(bucket.Seconds())}
	type acc struct {
		worst                  int
		cause                  model.Cause
		gwR, netR              []float64
		gs, gr, ns, nr, cycles int
	}
	accs := make([]acc, n)
	for i := range accs {
		accs[i].worst = -1
	}
	for _, sm := range samples {
		i := int(sm.T.Sub(from) / bucket)
		if i < 0 || i >= n {
			continue
		}
		a := &accs[i]
		a.cycles++
		if rk := statusRank(sm.Diag.Status); rk > a.worst {
			a.worst, a.cause = rk, sm.Diag.Cause
		}
		if sm.Gateway.Recv > 0 {
			a.gwR = append(a.gwR, sm.Gateway.RTTms)
		}
		if rtt := sm.InternetRTT(); rtt >= 0 {
			a.netR = append(a.netR, rtt)
		}
		if sm.Diag.Status != model.StatusDown { // outages are shown as such, not as 100 % loss
			a.gs += sm.Gateway.Sent
			a.gr += sm.Gateway.Recv
			for _, p := range sm.Internet {
				a.ns += p.Sent
				a.nr += p.Recv
			}
		}
	}
	med := func(v []float64) float64 {
		if len(v) == 0 {
			return -1
		}
		sort.Float64s(v)
		return math.Round(v[len(v)/2]*100) / 100
	}
	loss := func(sent, recv int) float64 {
		if sent == 0 {
			return -1
		}
		return math.Round(float64(sent-recv)/float64(sent)*10000) / 10000
	}
	for i, a := range accs {
		out.T = append(out.T, from.Add(time.Duration(i)*bucket).Unix())
		out.Status = append(out.Status, a.worst)
		out.Cause = append(out.Cause, string(a.cause))
		out.GwRTT = append(out.GwRTT, med(a.gwR))
		out.NetRTT = append(out.NetRTT, med(a.netR))
		out.GwLoss = append(out.GwLoss, loss(a.gs, a.gr))
		out.NetLoss = append(out.NetLoss, loss(a.ns, a.nr))
	}
	return out
}

// IncidentView adds human-readable fields to an incident.
type IncidentView struct {
	model.Incident
	DurationS float64              `json:"duration_s"`
	Ongoing   bool                 `json:"ongoing"`
	Texts     map[string]diag.Text `json:"texts"`
}

func (s *Server) incidentViews(days int) []IncidentView {
	now := s.now()
	incs := s.mon.Incidents(now.AddDate(0, 0, -days), now)
	out := make([]IncidentView, 0, len(incs))
	for _, i := range incs {
		v := IncidentView{Incident: i, DurationS: math.Round(i.Duration(now).Seconds()), Ongoing: i.End == nil,
			Texts: map[string]diag.Text{}}
		for _, l := range diag.Langs() {
			v.Texts[l] = diag.Explain(i.Cause, l)
		}
		out = append(out, v)
	}
	return out
}

func (s *Server) incidents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.incidentViews(intParam(r, "days", 30, 1, 3650)))
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	days := intParam(r, "days", 7, 1, 365)
	now := s.now()
	ev := s.mon.Events(now.AddDate(0, 0, -days), now)
	if ev == nil {
		ev = []model.Event{}
	}
	writeJSON(w, ev)
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	views := s.incidentViews(intParam(r, "days", 3650, 1, 3650))
	loc := s.mon.Location()
	lang := s.mon.Config().Lang
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="blamebox-incidents.csv"`)
	w.Write([]byte("\xef\xbb\xbf")) // lets Excel detect UTF-8
	cw := csv.NewWriter(w)
	if lang == "fr" {
		cw.Comma = ';' // what French spreadsheets expect
	}
	cw.Write([]string{"id", "kind", "cause", "label", "start", "end", "duration_s", "failed_checks",
		"router_answered", "public_dns_answered", "internet_loss_avg", "ip_before", "ip_after", "note"})
	for i := len(views) - 1; i >= 0; i-- { // oldest first
		v := views[i]
		end := ""
		if v.End != nil {
			end = v.End.In(loc).Format(time.RFC3339)
		}
		cw.Write([]string{v.ID, v.Kind, string(v.Cause), v.Texts[lang].Short, v.Start.In(loc).Format(time.RFC3339), end,
			strconv.Itoa(int(v.DurationS)), strconv.Itoa(v.Cycles), strconv.Itoa(v.Evidence.GatewayAnswered),
			strconv.Itoa(v.Evidence.PublicDNSOK), strconv.FormatFloat(v.Evidence.InternetLossAvg, 'f', 4, 64),
			v.Evidence.IPBefore, v.Evidence.IPAfter, v.Note})
	}
	cw.Flush()
}

func (s *Server) testAlert(w http.ResponseWriter, r *http.Request) {
	// Only same-origin scripts can send this header: protects against cross-site requests.
	if r.Header.Get("X-BlameBox") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	n := s.mon.Notifier()
	if n == nil || len(n.Channels()) == 0 {
		writeJSON(w, map[string]string{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeJSON(w, n.SendTest(ctx))
}

func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	cfg := s.mon.Config()
	lang := r.URL.Query().Get("lang")
	if lang != "fr" && lang != "en" {
		lang = cfg.Lang
	}
	maxDays := cfg.RetentionDays
	if maxDays < 7 {
		maxDays = 7
	}
	b, err := report.Render(s.mon, report.Options{
		Days: intParam(r, "days", 7, 1, maxDays), Lang: lang, ISP: cfg.ISPName, Line: cfg.LineName,
		Demo: cfg.Demo, Now: s.now(), Loc: s.mon.Location(), Retention: cfg.RetentionDays,
	})
	if err != nil {
		log.Printf("report: %v", err)
		http.Error(w, "report error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

// metrics exposes the current state in the Prometheus text format.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	sn := s.mon.Snapshot(s.now())
	var b strings.Builder
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	metric := func(name, help, typ string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	b01 := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	metric("blamebox_info", "Build and probe information.", "gauge")
	fmt.Fprintf(&b, "blamebox_info{version=\"%s\",ping_method=\"%s\",demo=\"%t\"} 1\n", esc.Replace(sn.Version), esc.Replace(sn.Info.PingMethod), sn.Info.Demo)
	if sn.Last == nil {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(b.String()))
		return
	}
	metric("blamebox_up", "1 when the connection works (degraded included), 0 during an outage.", "gauge")
	fmt.Fprintf(&b, "blamebox_up %d\n", b01(sn.Diag.Status != model.StatusDown))
	metric("blamebox_degraded", "1 when packet loss is above the threshold.", "gauge")
	fmt.Fprintf(&b, "blamebox_degraded %d\n", b01(sn.Diag.Status == model.StatusDegraded))
	metric("blamebox_layer_ok", "1 when the layer works, 0 when it failed or could not be tested.", "gauge")
	for _, l := range []struct{ name, state string }{{"box", sn.Diag.Layers.Box}, {"internet", sn.Diag.Layers.Internet},
		{"dns", sn.Diag.Layers.DNS}, {"web", sn.Diag.Layers.Web}} {
		fmt.Fprintf(&b, "blamebox_layer_ok{layer=\"%s\"} %d\n", l.name, b01(l.state == model.LayerOK || l.state == model.LayerWarn))
	}
	metric("blamebox_rtt_milliseconds", "Median round-trip time of the last check.", "gauge")
	pings := append([]model.PingResult{sn.Last.Gateway}, sn.Last.Internet...)
	for i, p := range pings {
		role := "internet"
		if i == 0 {
			role = "gateway"
		}
		if p.Recv > 0 {
			fmt.Fprintf(&b, "blamebox_rtt_milliseconds{target=\"%s\",role=\"%s\"} %g\n", esc.Replace(p.Target), role, p.RTTms)
		}
	}
	metric("blamebox_loss_ratio", "Packet loss over the rolling window (0-1).", "gauge")
	fmt.Fprintf(&b, "blamebox_loss_ratio{path=\"gateway\"} %g\nblamebox_loss_ratio{path=\"internet\"} %g\n", sn.Window.GatewayLoss, sn.Window.InternetLoss)
	metric("blamebox_outages_24h", "Outages over the last 24 hours.", "gauge")
	fmt.Fprintf(&b, "blamebox_outages_24h %d\n", sn.Stats24h.Outages)
	metric("blamebox_downtime_24h_seconds", "Downtime over the last 24 hours.", "gauge")
	fmt.Fprintf(&b, "blamebox_downtime_24h_seconds %g\n", sn.Stats24h.DowntimeS)
	metric("blamebox_availability_7d_ratio", "Availability over the last 7 days (0-1).", "gauge")
	fmt.Fprintf(&b, "blamebox_availability_7d_ratio %g\n", sn.Stats7d.Availability)
	metric("blamebox_incident_open", "1 while an incident is ongoing.", "gauge")
	for _, inc := range []*model.Incident{sn.Outage, sn.Degradation} {
		if inc != nil {
			fmt.Fprintf(&b, "blamebox_incident_open{kind=\"%s\",cause=\"%s\"} 1\n", inc.Kind, inc.Cause)
		}
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.Write([]byte(b.String()))
}
