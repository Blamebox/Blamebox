// Package notify sends alerts through ntfy, Telegram or a generic webhook.
//
// When the Internet is down, alerts usually cannot leave the house: they are queued
// and sent as soon as the connection is back.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/blamebox/blamebox/internal/config"
	"github.com/blamebox/blamebox/internal/diag"
	"github.com/blamebox/blamebox/internal/model"
)

// Message is one alert.
type Message struct {
	Title    string          `json:"title"`
	Body     string          `json:"body"`
	Kind     string          `json:"kind"` // "opened", "closed", "test"
	Priority int             `json:"priority"`
	Incident *model.Incident `json:"incident,omitempty"`
	Created  time.Time       `json:"created"`
}

// Sender delivers a message through one channel.
type Sender interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// Notifier fans messages out to every configured channel and retries failures.
type Notifier struct {
	senders []Sender
	lang    string
	loc     *time.Location

	mu       sync.Mutex
	queue    []queued
	inflight int        // messages taken out of the queue by a running flush
	flushMu  sync.Mutex // one flush at a time
}

type queued struct {
	m       Message
	pending map[string]bool // channel names still to deliver
}

// maxQueue bounds memory if a channel is misconfigured.
const maxQueue = 50

// New builds the notifier from the configuration. It returns a notifier with no channel
// when nothing is configured.
func New(cfg config.Config, loc *time.Location) *Notifier {
	n := &Notifier{lang: cfg.Lang, loc: loc}
	client := &http.Client{Timeout: 10 * time.Second}
	if cfg.NtfyURL != "" {
		n.senders = append(n.senders, &ntfy{url: cfg.NtfyURL, token: cfg.NtfyToken, c: client})
	}
	if cfg.TelegramToken != "" {
		n.senders = append(n.senders, &telegram{token: cfg.TelegramToken, chat: cfg.TelegramChat, c: client,
			base: "https://api.telegram.org"})
	}
	if cfg.WebhookURL != "" {
		n.senders = append(n.senders, &webhook{url: cfg.WebhookURL, c: client})
	}
	return n
}

// Channels lists the configured channel names.
func (n *Notifier) Channels() []string {
	var out []string
	for _, s := range n.senders {
		out = append(out, s.Name())
	}
	return out
}

// Enqueue adds a message for every channel and starts delivering it in the background,
// so a dead connection never slows the measurements down.
func (n *Notifier) Enqueue(ctx context.Context, m Message) {
	if len(n.senders) == 0 {
		return
	}
	if m.Created.IsZero() {
		m.Created = time.Now()
	}
	q := queued{m: m, pending: map[string]bool{}}
	for _, s := range n.senders {
		q.pending[s.Name()] = true
	}
	n.mu.Lock()
	n.queue = append(n.queue, q)
	if len(n.queue) > maxQueue {
		n.queue = n.queue[len(n.queue)-maxQueue:]
	}
	n.mu.Unlock()
	go n.TryFlush(ctx)
}

// TryFlush flushes unless a flush is already running.
func (n *Notifier) TryFlush(ctx context.Context) {
	if !n.flushMu.TryLock() {
		return
	}
	defer n.flushMu.Unlock()
	n.flush(ctx)
}

// Flush retries every queued message and waits for the result; messages older than a day are dropped.
func (n *Notifier) Flush(ctx context.Context) {
	n.flushMu.Lock()
	defer n.flushMu.Unlock()
	n.flush(ctx)
}

// flush sends without holding the queue lock: the dashboard stays responsive meanwhile.
func (n *Notifier) flush(ctx context.Context) {
	n.mu.Lock()
	work := n.queue
	n.queue = nil
	n.inflight = len(work)
	n.mu.Unlock()

	var keep []queued
	for _, q := range work {
		if time.Since(q.m.Created) > 24*time.Hour {
			continue
		}
		for _, s := range n.senders {
			if !q.pending[s.Name()] {
				continue
			}
			m := q.m
			if time.Since(m.Created) > 2*time.Minute {
				m.Body += "\n" + n.t("late", m.Created.In(n.loc).Format("15:04"))
			}
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := s.Send(sctx, m)
			cancel()
			if err == nil {
				delete(q.pending, s.Name())
			}
		}
		if len(q.pending) > 0 {
			keep = append(keep, q)
		}
	}

	n.mu.Lock()
	n.queue = append(keep, n.queue...) // messages enqueued meanwhile come after the older ones
	if len(n.queue) > maxQueue {
		n.queue = n.queue[len(n.queue)-maxQueue:]
	}
	n.inflight = 0
	n.mu.Unlock()
}

// Pending returns the number of messages waiting to be delivered.
func (n *Notifier) Pending() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.queue) + n.inflight
}

// SendTest sends a test message synchronously and reports each channel's result.
func (n *Notifier) SendTest(ctx context.Context) map[string]string {
	out := map[string]string{}
	m := Message{Title: n.t("test_title"), Body: n.t("test_body"), Kind: "test", Priority: 3, Created: time.Now()}
	for _, s := range n.senders {
		if err := s.Send(ctx, m); err != nil {
			out[s.Name()] = err.Error()
		} else {
			out[s.Name()] = "ok"
		}
	}
	return out
}

// Opened builds the alert for a new incident.
func (n *Notifier) Opened(i model.Incident) Message {
	tx := diag.Explain(i.Cause, n.lang)
	icon := "⚠️"
	prio := 4
	if i.Kind == "degradation" {
		icon, prio = "📉", 3
	}
	body := n.t("since", i.Start.In(n.loc).Format("15:04")) + " " + tx.Body
	return Message{Title: icon + " " + tx.Title, Body: body, Kind: "opened", Priority: prio, Incident: &i, Created: time.Now()}
}

// Closed builds the alert for a finished incident.
func (n *Notifier) Closed(i model.Incident) Message {
	tx := diag.Explain(i.Cause, n.lang)
	end := time.Now()
	if i.End != nil {
		end = *i.End
	}
	title := "✅ " + n.t("back")
	if i.Kind == "degradation" {
		title = "✅ " + n.t("loss_over")
	}
	body := n.t("summary", FormatDuration(i.Duration(end), n.lang),
		i.Start.In(n.loc).Format("15:04"), end.In(n.loc).Format("15:04"), tx.Short)
	if i.Kind == "outage" && !i.Cause.HomeSide() && i.Cycles > 0 && i.Evidence.GatewayAnswered == i.Cycles {
		body += " " + n.t("proof")
	}
	return Message{Title: title, Body: body, Kind: "closed", Priority: 3, Incident: &i, Created: time.Now()}
}

var phrases = map[string]map[string]string{
	"fr": {
		"since":      "Depuis %s.",
		"back":       "Connexion rétablie",
		"loss_over":  "Pertes de paquets terminées",
		"summary":    "%s (%s → %s). Cause : %s.",
		"proof":      "Ta box est restée joignable pendant toute la coupure : c'est noté pour ton rapport.",
		"late":       "(envoyé en retard : la connexion était coupée à %s)",
		"test_title": "BlameBox : test d'alerte",
		"test_body":  "Si tu lis ceci, les alertes fonctionnent.",
	},
	"en": {
		"since":      "Since %s.",
		"back":       "Connection restored",
		"loss_over":  "Packet loss is over",
		"summary":    "%s (%s → %s). Cause: %s.",
		"proof":      "Your router stayed reachable during the whole outage: noted for your report.",
		"late":       "(sent late: the connection was down at %s)",
		"test_title": "BlameBox: test alert",
		"test_body":  "If you can read this, alerts work.",
	},
}

func (n *Notifier) t(key string, args ...any) string {
	m, ok := phrases[n.lang]
	if !ok {
		m = phrases["fr"]
	}
	if len(args) == 0 {
		return m[key]
	}
	return fmt.Sprintf(m[key], args...)
}

// FormatDuration prints a duration the way people say it: "45 s", "12 min", "2 h 05".
func FormatDuration(d time.Duration, lang string) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) - h*60
		return fmt.Sprintf("%d h %02d", h, m)
	default:
		days := int(d.Hours() / 24)
		if lang == "en" {
			return fmt.Sprintf("%d days", days)
		}
		return fmt.Sprintf("%d jours", days)
	}
}

// --- channels ---

type ntfy struct {
	url, token string
	c          *http.Client
}

func (s *ntfy) Name() string { return "ntfy" }

func (s *ntfy) Send(ctx context.Context, m Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", encodeHeader(m.Title))
	req.Header.Set("Priority", fmt.Sprint(m.Priority))
	req.Header.Set("Tags", "blamebox")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	return do(s.c, req)
}

// encodeHeader keeps non-ASCII titles intact (RFC 2047), which ntfy understands.
func encodeHeader(v string) string {
	for _, r := range v {
		if r > 127 {
			return "=?UTF-8?B?" + base64Std(v) + "?="
		}
	}
	return v
}

type telegram struct {
	token, chat, base string
	c                 *http.Client
}

func (s *telegram) Name() string { return "telegram" }

func (s *telegram) Send(ctx context.Context, m Message) error {
	form := url.Values{"chat_id": {s.chat}, "text": {m.Title + "\n" + m.Body}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/bot"+s.token+"/sendMessage",
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := do(s.c, req); err != nil {
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), s.token, "***")) // never leak the bot token
	}
	return nil
}

type webhook struct {
	url string
	c   *http.Client
}

func (s *webhook) Name() string { return "webhook" }

func (s *webhook) Send(ctx context.Context, m Message) error {
	b, _ := json.Marshal(m)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(s.c, req)
}

func do(c *http.Client, req *http.Request) error {
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s answered HTTP %d", req.URL.Host, resp.StatusCode)
	}
	return nil
}
