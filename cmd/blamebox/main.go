// Command blamebox watches your Internet connection and tells you who is to blame when it drops.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // time zones work even in a minimal container

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/demo"
	"github.com/blamebox/blamebox/internal/diag"
	"github.com/blamebox/blamebox/internal/model"
	"github.com/blamebox/blamebox/internal/monitor"
	"github.com/blamebox/blamebox/internal/notify"
	"github.com/blamebox/blamebox/internal/probe"
	"github.com/blamebox/blamebox/internal/store"
	"github.com/blamebox/blamebox/internal/web"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	var (
		demoFlag    = flag.Bool("demo", false, "simulate a connection with outages (nothing is measured)")
		check       = flag.Bool("check", false, "run one measurement, print the diagnosis and exit")
		listen      = flag.String("listen", "", "HTTP listen address (default :8080, or BLAMEBOX_LISTEN)")
		dataDir     = flag.String("data", "", "data directory (default ./data, or BLAMEBOX_DATA_DIR)")
		showVersion = flag.Bool("version", false, "print the version and exit")
		healthcheck = flag.Bool("healthcheck", false, "exit 0 if the local web server answers (for Docker)")
		exportDemo  = flag.String("export-demo", "", "write a frozen demo dashboard (static files) into this directory and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "BlameBox %s — finds out who is to blame when your Internet drops.\n\n", monitor.Version)
		fmt.Fprintf(os.Stderr, "Usage: blamebox [options]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEvery setting can also be given as a BLAMEBOX_* environment variable; see the README.\n")
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(monitor.Version)
		return
	}
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("%v", err)
	}
	if *demoFlag {
		cfg.Demo = true
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	loc := time.Local

	if *check {
		os.Exit(runCheck(cfg))
	}
	if *exportDemo != "" {
		if err := runExportDemo(cfg, loc, *exportDemo); err != nil {
			log.Fatalf("export: %v", err)
		}
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	now := time.Now()

	dir := cfg.DataDir
	var prober probe.Prober
	var sim *demo.Demo
	notifier := notify.New(cfg, loc)
	if cfg.Demo {
		dir = filepath.Join(cfg.DataDir, "demo")
		if err := os.RemoveAll(dir); err != nil {
			log.Fatalf("cleaning the demo directory: %v", err)
		}
		sim = demo.New(loc, now)
		prober = sim
		notifier = notify.New(config.Config{Lang: cfg.Lang}, loc) // no alerts from simulated outages
	} else {
		prober = probe.NewReal(cfg)
	}
	st, err := store.Open(dir)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer st.Close()

	mon, err := monitor.New(cfg, prober, st, notifier, loc, now)
	if err != nil {
		log.Fatalf("%v", err)
	}
	if sim != nil {
		log.Printf("demo mode: simulating the last 7 days…")
		mon.Backfill(ctx, sim.History(now.Add(-7*24*time.Hour), now, cfg.Interval))
	}

	srv := &http.Server{Addr: cfg.Listen, Handler: web.New(mon), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("web server: %v", err)
		}
	}()

	info := prober.Info()
	log.Printf("BlameBox %s started: dashboard on http://%s", monitor.Version, displayAddr(cfg.Listen))
	if !cfg.Demo {
		log.Printf("measuring every %s · ping method: %s · data in %s", cfg.Interval, info.PingMethod, dir)
		if info.PingMethod == probe.MethodTCP {
			log.Printf("note: ICMP ping is not allowed here, TCP is used instead (less precise). Run as root, add CAP_NET_RAW, or allow unprivileged ping (sysctl net.ipv4.ping_group_range).")
		}
		if ch := notifier.Channels(); len(ch) > 0 {
			log.Printf("alerts: %s", strings.Join(ch, ", "))
		}
	}

	go mon.Run(ctx)
	<-ctx.Done()
	log.Printf("stopping…")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

// runExportDemo simulates a week, stops during a live ISP outage and writes the dashboard as static files.
func runExportDemo(cfg config.Config, loc *time.Location, dir string) error {
	tmp, err := os.MkdirTemp("", "blamebox-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	st, err := store.Open(tmp)
	if err != nil {
		return err
	}
	defer st.Close()
	cfg.Demo = true
	// Freeze on an evening, when outages usually happen: the last 21:27 before now.
	n := time.Now().In(loc)
	start := time.Date(n.Year(), n.Month(), n.Day(), 21, 27, 0, 0, loc)
	if start.After(n) {
		start = start.AddDate(0, 0, -1)
	}
	sim := demo.New(loc, start)
	mon, err := monitor.New(cfg, sim, st, nil, loc, start)
	if err != nil {
		return err
	}
	ctx := context.Background()
	mon.Backfill(ctx, sim.History(start.Add(-7*24*time.Hour), start, cfg.Interval))
	// The demo's first live outage starts one minute after start: freeze two minutes into it.
	freeze := start.Add(3 * time.Minute)
	for t := start; !t.After(freeze); t = t.Add(cfg.Interval) {
		mon.Ingest(ctx, sim.Sample(t), nil)
	}
	if err := web.ExportStatic(mon, freeze, dir); err != nil {
		return err
	}
	log.Printf("demo dashboard written to %s (open index.html through a web server)", dir)
	return nil
}

// runHealthcheck asks the running instance for /healthz; used by the container image,
// which has no shell or curl.
func runHealthcheck() int {
	cfg, _ := config.Load()
	addr := cfg.Listen
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func displayAddr(listen string) string {
	if strings.HasPrefix(listen, ":") {
		return "localhost" + listen
	}
	return listen
}

// runCheck measures once and prints a readable diagnosis, for testing an installation.
func runCheck(cfg config.Config) int {
	p := probe.NewReal(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, _ := p.Probe(ctx, time.Now())
	s.Diag = diag.Evaluate(s, diag.Window{}, cfg.LossThreshold)
	info := p.Info()
	mark := func(ok bool) string {
		if ok {
			return "OK  "
		}
		return "FAIL"
	}
	fmt.Printf("BlameBox %s — one-off check (ping method: %s)\n\n", monitor.Version, info.PingMethod)
	fmt.Printf("[%s] router %-15s  %d/%d answers  %s  %s\n", mark(s.Gateway.OK()), s.Gateway.Target, s.Gateway.Recv, s.Gateway.Sent, rtt(s.Gateway), s.Gateway.Err)
	for _, i := range s.Internet {
		fmt.Printf("[%s] ping   %-15s  %d/%d answers  %s  %s\n", mark(i.OK()), i.Target, i.Recv, i.Sent, rtt(i), i.Err)
	}
	if s.DNSLocal.Skip {
		fmt.Printf("[skip] router DNS (disabled)\n")
	} else {
		fmt.Printf("[%s] DNS    %-15s  %s  %.0f ms  %s\n", mark(s.DNSLocal.OK), s.DNSLocal.Server, s.DNSLocal.Name, s.DNSLocal.Ms, s.DNSLocal.Err)
	}
	for _, d := range s.DNSPub {
		fmt.Printf("[%s] DNS    %-15s  %s  %.0f ms  %s\n", mark(d.OK), d.Server, d.Name, d.Ms, d.Err)
	}
	fmt.Printf("[%s] web    %s  %.0f ms  %s\n", mark(s.Web.OK), s.Web.URL, s.Web.Ms, s.Web.Err)
	if s.PublicIP != "" {
		fmt.Printf("       public IP: %s\n", s.PublicIP)
	}
	tx := diag.Explain(s.Diag.Cause, cfg.Lang)
	fmt.Printf("\n→ %s\n  %s\n", tx.Title, tx.Body)
	if tx.Advice != "" {
		fmt.Printf("  %s\n", tx.Advice)
	}
	if s.Diag.Cause == model.CauseDNSISP && cfg.DNSLocal == "auto" {
		fmt.Printf("\n  Note: some routers do not run a DNS server. If yours does not, this is expected:\n" +
			"  BlameBox stops testing it after a few minutes, or set BLAMEBOX_DNS_LOCAL=off.\n")
	}
	for _, w := range monitor.SetupWarnings(info.Gateway, info.Iface) {
		switch w {
		case "docker_bridge":
			fmt.Printf("\n  Warning: this container is not on the host network, so it measures Docker, not your router.\n" +
				"  Add \"network_mode: host\" (docker run --network host).\n")
		case "wifi":
			fmt.Printf("\n  Warning: the probe is on Wi-Fi (%s). Wire it to the router for a reliable diagnosis.\n", info.Iface)
		}
	}
	if info.PingMethod == probe.MethodTCP {
		fmt.Printf("\n  Note: ICMP ping is not allowed, TCP was used (less precise). Run as root or add CAP_NET_RAW.\n")
	}
	if s.Diag.Status == model.StatusDown {
		return 1
	}
	return 0
}

func rtt(p model.PingResult) string {
	if p.RTTms < 0 {
		return "   -   "
	}
	return fmt.Sprintf("%5.1f ms", p.RTTms)
}
