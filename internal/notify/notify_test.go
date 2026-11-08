package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/model"
)

type recorder struct {
	mu   sync.Mutex
	fail bool
	reqs []*http.Request
	body []string
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	b, _ := io.ReadAll(req.Body)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	r.body = append(r.body, string(b))
	if r.fail {
		w.WriteHeader(http.StatusBadGateway)
	}
}

func TestChannelsAndQueue(t *testing.T) {
	rec := &recorder{fail: true}
	srv := httptest.NewServer(http.HandlerFunc(rec.handler))
	defer srv.Close()
	cfg := config.Default()
	cfg.NtfyURL = srv.URL + "/topic"
	cfg.WebhookURL = srv.URL + "/hook"
	cfg.TelegramToken, cfg.TelegramChat = "SECRET", "42"
	n := New(cfg, time.UTC)
	n.senders[1].(*telegram).base = srv.URL
	if got := strings.Join(n.Channels(), ","); got != "ntfy,telegram,webhook" {
		t.Fatalf("channels %s", got)
	}

	end := time.Date(2026, 11, 8, 21, 42, 0, 0, time.UTC)
	inc := model.Incident{Kind: "outage", Cause: model.CauseISP, Start: end.Add(-12 * time.Minute), End: &end, Cycles: 24,
		Evidence: model.Evidence{GatewayAnswered: 24}}
	n.Enqueue(context.Background(), n.Closed(inc))
	n.Flush(context.Background()) // wait for the background attempt
	if n.Pending() != 1 {
		t.Fatal("a failed delivery must stay queued")
	}
	rec.mu.Lock()
	rec.fail = false
	rec.mu.Unlock()
	n.Flush(context.Background())
	if n.Pending() != 0 {
		t.Fatal("queue should be empty once delivered")
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	var ntfyBody, hookBody, tgBody string
	for i, r := range rec.reqs {
		switch {
		case r.URL.Path == "/topic":
			ntfyBody = rec.body[i]
			if r.Header.Get("Title") == "" || !strings.HasPrefix(r.Header.Get("Title"), "=?UTF-8?B?") {
				t.Errorf("ntfy title header %q", r.Header.Get("Title"))
			}
		case r.URL.Path == "/hook":
			hookBody = rec.body[i]
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			tgBody = rec.body[i]
		}
	}
	if !strings.Contains(ntfyBody, "12 min") || !strings.Contains(ntfyBody, "21:30 → 21:42") || !strings.Contains(ntfyBody, "box est restée joignable") {
		t.Errorf("ntfy body %q", ntfyBody)
	}
	var m Message
	if err := json.Unmarshal([]byte(hookBody), &m); err != nil || m.Kind != "closed" || m.Incident == nil {
		t.Errorf("webhook payload %q (%v)", hookBody, err)
	}
	if !strings.Contains(tgBody, "chat_id=42") {
		t.Errorf("telegram form %q", tgBody)
	}
}

func TestTelegramErrorHidesToken(t *testing.T) {
	s := &telegram{token: "SECRET-TOKEN", chat: "1", c: &http.Client{Timeout: time.Second}, base: "http://127.0.0.1:1"}
	err := s.Send(context.Background(), Message{Title: "t"})
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("error must not leak the token: %v", err)
	}
}

func TestNoChannel(t *testing.T) {
	n := New(config.Default(), time.UTC)
	n.Enqueue(context.Background(), Message{Title: "x"})
	if n.Pending() != 0 || len(n.Channels()) != 0 {
		t.Fatal("nothing configured: nothing queued")
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:                "45 s",
		12*time.Minute + 20*time.Second: "12 min",
		2*time.Hour + 5*time.Minute:     "2 h 05",
		50 * time.Hour:                  "2 jours",
	}
	for d, want := range cases {
		if got := FormatDuration(d, "fr"); got != want {
			t.Errorf("%s: %q, want %q", d, got, want)
		}
	}
}
