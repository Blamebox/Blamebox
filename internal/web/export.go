package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blamebox/blamebox/internal/monitor"
	"github.com/blamebox/blamebox/internal/report"
)

// ExportStatic writes a frozen, file-only copy of the dashboard and its reports into dir,
// all files side by side, ready to be hosted on any static web server (the public demo).
func ExportStatic(mon *monitor.Monitor, now time.Time, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s := &Server{mon: mon, now: func() time.Time { return now }}
	h := s.routes()
	fetch := func(path string) ([]byte, error) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			return nil, fmt.Errorf("%s: HTTP %d", path, rec.Code)
		}
		return rec.Body.Bytes(), nil
	}
	write := func(name string, b []byte) error { return os.WriteFile(filepath.Join(dir, name), b, 0o644) }

	// The status tells the page it is a frozen copy.
	b, err := fetch("/api/status")
	if err != nil {
		return err
	}
	var st map[string]any
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	st["static"] = true
	if b, err = json.Marshal(st); err != nil {
		return err
	}
	if err := write("status.json", b); err != nil {
		return err
	}
	for name, path := range map[string]string{
		"samples-24.json":  "/api/samples?hours=24&points=288",
		"samples-168.json": "/api/samples?hours=168&points=336",
		"incidents.json":   "/api/incidents?days=30",
		"incidents.csv":    "/api/export/incidents.csv",
	} {
		b, err := fetch(path)
		if err != nil {
			return err
		}
		if err := write(name, b); err != nil {
			return err
		}
	}
	cfg := mon.Config()
	for _, lang := range []string{"fr", "en"} {
		b, err := report.Render(mon, report.Options{Days: 7, Lang: lang, ISP: cfg.ISPName, Line: cfg.LineName, Demo: true,
			Now: now, Loc: mon.Location(), Retention: cfg.RetentionDays, Static: true})
		if err != nil {
			return err
		}
		if err := write("report-"+lang+".html", b); err != nil {
			return err
		}
	}
	static, _ := fs.Sub(staticFS, "static")
	for _, name := range []string{"app.css", "app.js", "favicon.svg", "report.css", "report.js"} {
		b, err := fs.ReadFile(static, name)
		if err != nil {
			return err
		}
		if err := write(name, b); err != nil {
			return err
		}
	}
	idx, err := fs.ReadFile(static, "index.html")
	if err != nil {
		return err
	}
	page := strings.NewReplacer(`href="static/`, `href="`, `src="static/`, `src="`, "<body>", `<body data-mode="static">`).Replace(string(idx))
	return write("index.html", []byte(page))
}
