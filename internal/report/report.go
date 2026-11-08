// Package report builds the printable incident report to send to the ISP.
package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/blamebox/blamebox/internal/diag"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/monitor"
	"github.com/blamebox/blamebox/internal/notify"
)

//go:embed report.html
var pageHTML string

var page = template.Must(template.New("report").Parse(pageHTML))

// Source is what the report needs from the monitor.
type Source interface {
	Incidents(from, to time.Time) []model.Incident
	Samples(from, to time.Time) []model.Sample
	Stats(from, to time.Time) monitor.Stats
}

// Options select the period and language.
type Options struct {
	Days      int
	Lang      string
	ISP       string
	Line      string
	Demo      bool
	Now       time.Time
	Loc       *time.Location
	Retention int
	Static    bool // frozen copy for the public demo: plain links, assets next to the page
}

// Row is one incident line.
type Row struct {
	Day, Time, Duration, Cause, Proof, Side, Color string
}

// Data feeds the template.
type Data struct {
	L          map[string]string
	Lang       string
	Period     string
	Generated  string
	ISP, Line  string
	Demo       bool
	KPIs       []KPI
	Verdict    template.HTML
	Timeline   template.HTML
	LossChart  template.HTML
	HasLoss    bool
	Outages    []Row
	Degrades   []Row
	Message    string
	Method     []string
	Days       int
	DayChoices []int
	Static     bool
	Assets     string // where report.css and report.js are served
}

// KPI is one headline figure.
type KPI struct{ Value, Label string }

var colors = map[model.Cause]string{
	model.CauseISP: "#E5484D", model.CauseDNSISP: "#F5A524", model.CauseDNSAll: "#F5A524", model.CauseWeb: "#A855F7",
	model.CauseLAN: "#8A94A6", model.CauseLossISP: "#FB923C", model.CauseLossLAN: "#94A3B8",
}

// Render writes the report as a standalone HTML page.
func Render(src Source, o Options) ([]byte, error) {
	if o.Lang != "en" {
		o.Lang = "fr"
	}
	L := labels[o.Lang]
	if o.Days < 1 {
		o.Days = 7
	}
	to := o.Now
	from := to.AddDate(0, 0, -o.Days)
	incs := src.Incidents(from, to)
	sort.SliceStable(incs, func(a, b int) bool { return incs[a].Start.Before(incs[b].Start) })
	samples := src.Samples(from, to)
	st := src.Stats(from, to)

	d := Data{L: L, Lang: o.Lang, ISP: o.ISP, Line: o.Line, Demo: o.Demo, Days: o.Days, Static: o.Static, Assets: "static/",
		Period:    fmt.Sprintf(L["period"], dayFmt(from.In(o.Loc), o.Lang), dayFmt(to.In(o.Loc), o.Lang)),
		Generated: to.In(o.Loc).Format("02/01/2006 15:04")}
	for _, n := range []int{1, 7, 14, 30} {
		if n <= o.Retention || n <= 7 {
			d.DayChoices = append(d.DayChoices, n)
		}
	}

	var outages, degr []model.Incident
	for _, i := range incs {
		if i.Kind == "outage" {
			outages = append(outages, i)
		} else {
			degr = append(degr, i)
		}
	}
	ispShare := 0.0
	if st.DowntimeS > 0 {
		ispShare = st.ISPDowntimeS / st.DowntimeS
	}
	d.KPIs = []KPI{
		{fmt.Sprint(st.Outages), L["k_outages"]},
		{notify.FormatDuration(time.Duration(st.DowntimeS)*time.Second, o.Lang), L["k_downtime"]},
		{fmt.Sprintf("%.0f %%", ispShare*100), L["k_isp"]},
		{fmt.Sprintf("%d / %d", st.Evening, st.Outages), L["k_evening"]},
	}
	for _, i := range outages {
		d.Outages = append(d.Outages, row(i, o, to))
	}
	for _, i := range degr {
		d.Degrades = append(d.Degrades, row(i, o, to))
	}
	d.Verdict = verdict(outages, st, o.Lang, to)
	d.Timeline = timeline(incs, from, to, o.Loc, o.Lang)
	lossSVG, maxNet, gwAtMax := lossByHour(samples, o.Loc, L)
	d.LossChart, d.HasLoss = lossSVG, len(samples) > 0
	d.Message = message(outages, st, from, to, maxNet, gwAtMax, o)
	d.Method = strings.Split(L["method"], "\n")
	if o.Static {
		d.Assets = ""
	}

	var buf bytes.Buffer
	err := page.Execute(&buf, d)
	return buf.Bytes(), err
}

func dayFmt(t time.Time, lang string) string {
	if lang == "en" {
		return t.Format("Mon 2 Jan 2006")
	}
	days := []string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}
	months := []string{"janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.", "oct.", "nov.", "déc."}
	return fmt.Sprintf("%s %d %s %d", days[t.Weekday()], t.Day(), months[t.Month()-1], t.Year())
}

// rowDay is a compact date for table rows ("dim. 1 nov.").
func rowDay(t time.Time, lang string) string {
	if lang == "en" {
		return t.Format("Mon 2 Jan")
	}
	full := dayFmt(t, lang)
	return full[:strings.LastIndex(full, " ")]
}

func row(i model.Incident, o Options, now time.Time) Row {
	tx := diag.Explain(i.Cause, o.Lang)
	L := labels[o.Lang]
	start := i.Start.In(o.Loc)
	r := Row{Day: rowDay(start, o.Lang), Cause: tx.Short, Side: tx.Side, Color: colors[i.Cause],
		Duration: notify.FormatDuration(i.Duration(now), o.Lang)}
	if i.End != nil {
		r.Time = start.Format("15:04") + "–" + i.End.In(o.Loc).Format("15:04")
	} else {
		r.Time = start.Format("15:04") + "–…"
		r.Duration += " " + L["ongoing"]
	}
	n := i.Cycles
	ev := i.Evidence
	switch i.Cause {
	case model.CauseISP:
		r.Proof = fmt.Sprintf(L["p_isp"], ev.GatewayAnswered, n)
	case model.CauseDNSISP:
		r.Proof = fmt.Sprintf(L["p_dns"], ev.PublicDNSOK, n)
	case model.CauseDNSAll:
		r.Proof = L["p_dnsall"]
	case model.CauseWeb:
		r.Proof = L["p_web"]
	case model.CauseLAN:
		r.Proof = L["p_lan"]
	case model.CauseLossISP, model.CauseLossLAN:
		r.Proof = fmt.Sprintf(L["p_loss"], pct(ev.InternetLossAvg), pct(ev.GatewayLossAvg))
	}
	if ev.IPBefore != "" && ev.IPAfter != "" && ev.IPBefore != ev.IPAfter {
		r.Proof += " " + L["p_ip"]
	}
	if i.Note == "interrupted" {
		r.Proof += " " + L["p_interrupted"]
	}
	return r
}

func pct(f float64) string {
	v := f * 100
	if v < 10 {
		return strings.Replace(fmt.Sprintf("%.1f %%", v), ".", ",", 1)
	}
	return fmt.Sprintf("%.0f %%", v)
}

// verdict states where the problem lies, citing only measured facts.
func verdict(outages []model.Incident, st monitor.Stats, lang string, now time.Time) template.HTML {
	L := labels[lang]
	if len(outages) == 0 {
		return template.HTML(L["v_none"])
	}
	esc := template.HTMLEscapeString
	tot := notify.FormatDuration(time.Duration(st.DowntimeS)*time.Second, lang)
	isp := notify.FormatDuration(time.Duration(st.ISPDowntimeS)*time.Second, lang)
	var home []string
	for _, i := range outages {
		if i.Cause.HomeSide() {
			home = append(home, fmt.Sprintf("%s (%s)", dayFmt(i.Start, lang), notify.FormatDuration(i.Duration(now), lang)))
		}
	}
	var b strings.Builder
	switch {
	case st.ISPDowntimeS > 0 && boxAlwaysUp(outages) && st.HomeDowntimeS == 0:
		fmt.Fprintf(&b, L["v_isp_proof_all"], esc(tot))
	case st.ISPDowntimeS > 0 && boxAlwaysUp(outages):
		fmt.Fprintf(&b, L["v_isp_proof"], esc(isp), esc(tot))
	case st.ISPDowntimeS > 0:
		fmt.Fprintf(&b, L["v_isp"], esc(isp), esc(tot))
	default:
		b.WriteString(L["v_home_only"])
	}
	if len(home) > 0 && st.ISPDowntimeS > 0 {
		fmt.Fprintf(&b, " "+L["v_home_some"], esc(strings.Join(home, ", ")))
	}
	return template.HTML(b.String())
}

// boxAlwaysUp reports whether the router answered during every failing cycle of every ISP-side outage.
func boxAlwaysUp(outages []model.Incident) bool {
	seen := false
	for _, i := range outages {
		if i.Cause.HomeSide() {
			continue
		}
		seen = true
		if i.Evidence.GatewayAnswered < i.Cycles {
			return false
		}
	}
	return seen
}

// timeline draws one row per day with the incidents on a 24-hour axis.
func timeline(incs []model.Incident, from, to time.Time, loc *time.Location, lang string) template.HTML {
	first := from.In(loc)
	day0 := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	var days []time.Time
	for d := day0; d.Before(to); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}
	if len(days) > 31 {
		days = days[len(days)-31:]
	}
	const W, left, rowH, top = 720.0, 70.0, 20.0, 18.0
	H := top + rowH*float64(len(days)) + 22
	x := func(h float64) float64 { return left + (W-left-10)*h/24 }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" width="100%%" role="img" aria-label="%s" xmlns="http://www.w3.org/2000/svg" font-family="Inter, Arial, sans-serif" font-size="10">`,
		W, H, template.HTMLEscapeString(labels[lang]["timeline"]))
	fmt.Fprintf(&b, `<rect x="%.1f" y="%.0f" width="%.1f" height="%.0f" fill="#FEF2F2"/>`, x(20), top-4, x(23)-x(20), rowH*float64(len(days))+4)
	fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" text-anchor="middle" fill="#B42318" font-size="9">%s</text>`, (x(20)+x(23))/2, top-7, labels[lang]["peak"])
	for h := 0; h <= 24; h += 3 {
		fmt.Fprintf(&b, `<line x1="%.1f" x2="%.1f" y1="%.0f" y2="%.0f" stroke="#E5E7EB"/>`, x(float64(h)), x(float64(h)), top-4, H-18)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" text-anchor="middle" fill="#6B7280" font-size="9">%02dh</text>`, x(float64(h)), H-6, h)
	}
	for r, d := range days {
		y := top + rowH*float64(r)
		fmt.Fprintf(&b, `<text x="0" y="%.1f" fill="#374151">%s</text>`, y+rowH/2+3, template.HTMLEscapeString(shortDay(d, lang)))
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="3" fill="#F3F4F6"/>`, left, y+3, W-left-10, rowH-6)
		dEnd := d.AddDate(0, 0, 1)
		for _, i := range incs {
			s, e := i.Start, to
			if i.End != nil {
				e = *i.End
			}
			if !e.After(d) || !s.Before(dEnd) {
				continue
			}
			if s.Before(d) {
				s = d
			}
			if e.After(dEnd) {
				e = dEnd
			}
			hs := s.Sub(d).Hours()
			he := e.Sub(d).Hours()
			w := math.Max(x(he)-x(hs), 2.5)
			yy, hh := y+3, rowH-6
			if i.Kind == "degradation" {
				yy, hh = y+rowH/2+1, rowH/2-4
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s"/>`, x(hs), yy, w, hh, colors[i.Cause])
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func shortDay(t time.Time, lang string) string {
	if lang == "en" {
		return t.Format("Mon 2")
	}
	return []string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}[t.Weekday()] + " " + fmt.Sprint(t.Day())
}

// lossByHour draws the average packet loss per hour of the day, to the router and to the Internet.
// It returns the chart, the worst hourly Internet loss and the router loss at that hour.
func lossByHour(samples []model.Sample, loc *time.Location, L map[string]string) (template.HTML, float64, float64) {
	var gs, gr, is, ir [24]int
	for _, s := range samples {
		if s.Diag.Status == model.StatusDown {
			continue
		}
		h := s.T.In(loc).Hour()
		gs[h] += s.Gateway.Sent
		gr[h] += s.Gateway.Recv
		for _, p := range s.Internet {
			is[h] += p.Sent
			ir[h] += p.Recv
		}
	}
	var gl, il [24]float64
	maxV, maxNet, gwAt := 0.02, 0.0, 0.0
	for h := 0; h < 24; h++ {
		if gs[h] > 0 {
			gl[h] = float64(gs[h]-gr[h]) / float64(gs[h])
		}
		if is[h] > 0 {
			il[h] = float64(is[h]-ir[h]) / float64(is[h])
		}
		maxV = math.Max(maxV, math.Max(gl[h], il[h]))
		if il[h] > maxNet {
			maxNet, gwAt = il[h], gl[h]
		}
	}
	maxV = math.Ceil(maxV*100/2) * 2 / 100
	const W, H, left, bottom, top = 720.0, 190.0, 40.0, 22.0, 12.0
	x := func(h float64) float64 { return left + (W-left-10)*h/23 }
	y := func(v float64) float64 { return top + (H-top-bottom)*(1-v/maxV) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" width="100%%" role="img" aria-label="%s" xmlns="http://www.w3.org/2000/svg" font-family="Inter, Arial, sans-serif" font-size="10">`,
		W, H, template.HTMLEscapeString(L["loss_title"]))
	for i := 0; i <= 4; i++ {
		v := maxV * float64(i) / 4
		fmt.Fprintf(&b, `<line x1="%.0f" x2="%.0f" y1="%.1f" y2="%.1f" stroke="#E5E7EB"/><text x="%.0f" y="%.1f" text-anchor="end" fill="#6B7280">%.0f %%</text>`,
			left, W-10, y(v), y(v), left-6, y(v)+3, v*100)
	}
	for h := 0; h < 24; h += 3 {
		fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" text-anchor="middle" fill="#6B7280">%02dh</text>`, x(float64(h)), H-6, h)
	}
	line := func(v [24]float64, color string) {
		var pts []string
		for h := 0; h < 24; h++ {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x(float64(h)), y(v[h])))
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2.2" stroke-linejoin="round"/>`, strings.Join(pts, " "), color)
	}
	line(gl, "#0F766E")
	line(il, "#E5484D")
	fmt.Fprintf(&b, `<rect x="%.0f" y="4" width="10" height="3" fill="#E5484D"/><text x="%.0f" y="9" fill="#374151">%s</text>`, left+6, left+20, template.HTMLEscapeString(L["to_net"]))
	fmt.Fprintf(&b, `<rect x="%.0f" y="4" width="10" height="3" fill="#0F766E"/><text x="%.0f" y="9" fill="#374151">%s</text>`, left+160, left+174, template.HTMLEscapeString(L["to_box"]))
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), maxNet, gwAt
}

// message pre-writes the request to the ISP from the measured facts only.
func message(outages []model.Incident, st monitor.Stats, from, to time.Time, maxNet, gwAt float64, o Options) string {
	L := labels[o.Lang]
	var isp []model.Incident
	dns := 0
	for _, i := range outages {
		if !i.Cause.HomeSide() {
			isp = append(isp, i)
		}
		if i.Cause == model.CauseDNSISP {
			dns++
		}
	}
	var b strings.Builder
	b.WriteString(L["m_hello"])
	if len(isp) == 0 {
		if maxNet >= 0.03 {
			fmt.Fprintf(&b, L["m_loss_only"], pct(maxNet), pct(gwAt))
		} else {
			b.WriteString(L["m_nothing"])
		}
	} else {
		first := isp[0].Start.In(o.Loc)
		dur := notify.FormatDuration(time.Duration(st.ISPDowntimeS)*time.Second, o.Lang)
		evening := ""
		if st.Evening*2 > st.Outages {
			evening = L["m_evening"]
		}
		fmt.Fprintf(&b, L["m_outages"], dayFmt(first, o.Lang), evening, len(isp), dur,
			dayFmt(from.In(o.Loc), o.Lang), dayFmt(to.In(o.Loc), o.Lang))
		if boxAlwaysUp(outages) {
			b.WriteString(L["m_proof"])
		}
		if dns > 0 {
			fmt.Fprintf(&b, L["m_dns"], dns)
		}
		if maxNet >= 0.03 {
			fmt.Fprintf(&b, L["m_loss"], pct(maxNet), pct(gwAt))
		}
	}
	b.WriteString(L["m_end"])
	return b.String()
}
