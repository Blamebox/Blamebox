package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaults(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != 30*time.Second || c.OpenAfter != 3 || c.Lang != "fr" || c.DNSLocal != "auto" || len(c.InternetTargets) != 2 {
		t.Fatalf("unexpected defaults %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	c, err := load(env(map[string]string{
		"BLAMEBOX_INTERVAL":       "45", // plain seconds
		"BLAMEBOX_TARGETS":        " 8.8.8.8 , 1.0.0.1 ,",
		"BLAMEBOX_LOSS_THRESHOLD": "3,5 %",
		"BLAMEBOX_LANG":           "EN",
		"BLAMEBOX_DEMO":           "oui",
		"BLAMEBOX_RETENTION_DAYS": "30",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Interval != 45*time.Second {
		t.Errorf("interval %s", c.Interval)
	}
	if strings.Join(c.InternetTargets, "|") != "8.8.8.8|1.0.0.1" {
		t.Errorf("targets %q", c.InternetTargets)
	}
	if c.LossThreshold < 0.0349 || c.LossThreshold > 0.0351 {
		t.Errorf("threshold %f", c.LossThreshold)
	}
	if c.Lang != "en" || !c.Demo || c.RetentionDays != 30 {
		t.Errorf("lang %s demo %v retention %d", c.Lang, c.Demo, c.RetentionDays)
	}
}

func TestErrors(t *testing.T) {
	_, err := load(env(map[string]string{
		"BLAMEBOX_INTERVAL":       "1s",
		"BLAMEBOX_LANG":           "de",
		"BLAMEBOX_AUTH":           "nopassword",
		"BLAMEBOX_TELEGRAM_TOKEN": "x",
		"BLAMEBOX_PING_COUNT":     "abc",
	}))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"INTERVAL", "LANG", "AUTH", "TELEGRAM", "PING_COUNT"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}
