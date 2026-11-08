package report

import (
	"strings"
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/monitor"
)

type fake struct {
	incs    []model.Incident
	samples []model.Sample
	stats   monitor.Stats
}

func (f fake) Incidents(from, to time.Time) []model.Incident { return f.incs }
func (f fake) Samples(from, to time.Time) []model.Sample     { return f.samples }
func (f fake) Stats(from, to time.Time) monitor.Stats        { return f.stats }

var now = time.Date(2026, 11, 8, 22, 0, 0, 0, time.UTC)

func render(t *testing.T, f fake, lang string) string {
	t.Helper()
	b, err := Render(f, Options{Days: 7, Lang: lang, Now: now, Loc: time.UTC, Retention: 7})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEmptyReport(t *testing.T) {
	page := render(t, fake{}, "fr")
	if !strings.Contains(page, "Aucune coupure") || !strings.Contains(page, "[Décrivez ici votre problème.]") {
		t.Fatal("an empty period must say there was no outage and leave the message to the user")
	}
}

func TestProofIsOnlyClaimedWhenMeasured(t *testing.T) {
	end := now.Add(-time.Hour)
	full := model.Incident{Kind: "outage", Cause: model.CauseISP, Start: end.Add(-20 * time.Minute), End: &end, Cycles: 40,
		Evidence: model.Evidence{GatewayAnswered: 40}}
	st := monitor.Stats{Outages: 1, DowntimeS: 1200, ISPDowntimeS: 1200}
	page := render(t, fake{incs: []model.Incident{full}, stats: st}, "fr")
	if !strings.Contains(page, "ma box restait joignable") {
		t.Fatal("the router answered every time: the message should say so")
	}
	partial := full
	partial.Evidence.GatewayAnswered = 30
	page = render(t, fake{incs: []model.Incident{partial}, stats: st}, "fr")
	if strings.Contains(page, "ma box restait joignable") {
		t.Fatal("the router did not answer every time: the message must not claim it did")
	}
	if !strings.Contains(render(t, fake{incs: []model.Incident{full}, stats: st}, "en"), "my router stayed reachable") {
		t.Fatal("English message")
	}
}

func TestLossOnlyMessage(t *testing.T) {
	var samples []model.Sample
	for i := 0; i < 120; i++ {
		ts := time.Date(2026, 11, 7, 21, 0, 0, 0, time.UTC).Add(time.Duration(i) * 30 * time.Second)
		samples = append(samples, model.Sample{T: ts, Gateway: model.PingResult{Sent: 3, Recv: 3},
			Internet: []model.PingResult{{Sent: 10, Recv: 9}}, Diag: model.Diagnosis{Status: model.StatusOK}})
	}
	page := render(t, fake{samples: samples}, "fr")
	if !strings.Contains(page, "pertes de paquets importantes") || !strings.Contains(page, "10 %") {
		t.Fatal("10 % evening loss without outage should be reported")
	}
}
