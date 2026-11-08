// Package config reads BlameBox settings from environment variables.
//
// Every setting has a sensible default, so BlameBox runs with no configuration at all.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds every runtime setting.
type Config struct {
	Listen   string        // HTTP listen address
	DataDir  string        // where measurements are stored
	Interval time.Duration // time between two measurement cycles

	Gateway         string   // router IP; empty = detected from the routing table
	InternetTargets []string // public IPs pinged every cycle
	DNSLocal        string   // "auto" (= router), an IP, or "off"
	DNSPublic       []string // public resolvers
	DNSNames        []string // names looked up, one per cycle in rotation
	WebURLs         []string // web endpoints, the first that answers wins

	PingCount   int
	PingTimeout time.Duration

	OpenAfter     int     // failing cycles in a row before an outage is declared
	CloseAfter    int     // good cycles in a row before an outage is closed
	LossWindow    int     // cycles used to compute rolling packet loss
	LossThreshold float64 // rolling loss (0–1) above which the line is "degraded"

	RetentionDays int    // how long raw measurements are kept
	Lang          string // "fr" or "en", for alerts and the report
	Auth          string // "user:password" to protect the web page; empty = open

	NtfyURL       string
	NtfyToken     string
	TelegramToken string
	TelegramChat  string
	WebhookURL    string

	ISPName  string // shown in the report, e.g. "Orange"
	LineName string // shown in the report, e.g. "Fibre"

	Demo bool // simulated measurements, for trying BlameBox without a real outage
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Listen:          ":8080",
		DataDir:         "./data",
		Interval:        30 * time.Second,
		InternetTargets: []string{"1.1.1.1", "9.9.9.9"},
		DNSLocal:        "auto",
		DNSPublic:       []string{"1.1.1.1", "9.9.9.9"},
		DNSNames:        []string{"www.google.com", "www.wikipedia.org", "www.cloudflare.com", "www.apple.com"},
		WebURLs:         []string{"https://www.cloudflare.com/cdn-cgi/trace", "http://www.gstatic.com/generate_204"},
		PingCount:       3,
		PingTimeout:     time.Second,
		OpenAfter:       3,
		CloseAfter:      2,
		LossWindow:      20,
		LossThreshold:   0.05,
		RetentionDays:   7,
		Lang:            "fr",
	}
}

// Load reads the environment on top of the defaults.
func Load() (Config, error) { return load(os.Getenv) }

func load(get func(string) string) (Config, error) {
	c := Default()
	var errs []string
	str := func(key string, dst *string) {
		if v := strings.TrimSpace(get("BLAMEBOX_" + key)); v != "" {
			*dst = v
		}
	}
	list := func(key string, dst *[]string) {
		if v := strings.TrimSpace(get("BLAMEBOX_" + key)); v != "" {
			var out []string
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
			if len(out) > 0 {
				*dst = out
			}
		}
	}
	integer := func(key string, dst *int, min, max int) {
		if v := strings.TrimSpace(get("BLAMEBOX_" + key)); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < min || n > max {
				errs = append(errs, fmt.Sprintf("BLAMEBOX_%s must be a whole number between %d and %d", key, min, max))
				return
			}
			*dst = n
		}
	}
	dur := func(key string, dst *time.Duration, min, max time.Duration) {
		if v := strings.TrimSpace(get("BLAMEBOX_" + key)); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				if n, err2 := strconv.Atoi(v); err2 == nil { // plain number = seconds
					d, err = time.Duration(n)*time.Second, nil
				}
			}
			if err != nil || d < min || d > max {
				errs = append(errs, fmt.Sprintf("BLAMEBOX_%s must be a duration between %s and %s (e.g. 30s)", key, min, max))
				return
			}
			*dst = d
		}
	}

	str("LISTEN", &c.Listen)
	str("DATA_DIR", &c.DataDir)
	dur("INTERVAL", &c.Interval, 5*time.Second, 10*time.Minute)
	str("GATEWAY", &c.Gateway)
	list("TARGETS", &c.InternetTargets)
	str("DNS_LOCAL", &c.DNSLocal)
	list("DNS_PUBLIC", &c.DNSPublic)
	list("DNS_NAMES", &c.DNSNames)
	list("WEB_URLS", &c.WebURLs)
	integer("PING_COUNT", &c.PingCount, 1, 10)
	dur("PING_TIMEOUT", &c.PingTimeout, 200*time.Millisecond, 5*time.Second)
	integer("OPEN_AFTER", &c.OpenAfter, 1, 20)
	integer("CLOSE_AFTER", &c.CloseAfter, 1, 20)
	integer("LOSS_WINDOW", &c.LossWindow, 2, 120)
	if v := strings.TrimSpace(get("BLAMEBOX_LOSS_THRESHOLD")); v != "" {
		v = strings.TrimSpace(strings.TrimSuffix(v, "%"))
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err == nil && f > 1 { // "5" means 5 %
			f /= 100
		}
		if err != nil || f <= 0 || f >= 1 {
			errs = append(errs, "BLAMEBOX_LOSS_THRESHOLD must be a percentage between 0 and 100 (e.g. 5)")
		} else {
			c.LossThreshold = f
		}
	}
	integer("RETENTION_DAYS", &c.RetentionDays, 1, 3650)
	str("LANG", &c.Lang)
	c.Lang = strings.ToLower(c.Lang)
	if c.Lang != "fr" && c.Lang != "en" {
		errs = append(errs, "BLAMEBOX_LANG must be fr or en")
		c.Lang = "fr"
	}
	str("AUTH", &c.Auth)
	if c.Auth != "" && !strings.Contains(c.Auth, ":") {
		errs = append(errs, "BLAMEBOX_AUTH must look like user:password")
	}
	str("NTFY_URL", &c.NtfyURL)
	str("NTFY_TOKEN", &c.NtfyToken)
	str("TELEGRAM_TOKEN", &c.TelegramToken)
	str("TELEGRAM_CHAT", &c.TelegramChat)
	str("WEBHOOK_URL", &c.WebhookURL)
	str("ISP", &c.ISPName)
	str("LINE", &c.LineName)
	switch strings.ToLower(strings.TrimSpace(get("BLAMEBOX_DEMO"))) {
	case "1", "true", "yes", "on", "oui":
		c.Demo = true
	}
	if (c.TelegramToken == "") != (c.TelegramChat == "") {
		errs = append(errs, "BLAMEBOX_TELEGRAM_TOKEN and BLAMEBOX_TELEGRAM_CHAT must be set together")
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("configuration: %s", strings.Join(errs, "; "))
	}
	return c, nil
}
