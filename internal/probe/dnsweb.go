package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

// resolverFor returns a resolver that only talks to the given DNS server.
func resolverFor(server string, timeout time.Duration) *net.Resolver {
	return &net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: timeout}
			return d.DialContext(ctx, network, net.JoinHostPort(server, "53"))
		},
	}
}

// LookupVia resolves name through one DNS server only.
// The names checked are well known: "no such host" means the resolver is broken or filtered.
func LookupVia(ctx context.Context, server, name string, timeout time.Duration) model.DNSResult {
	res := model.DNSResult{Server: server, Name: name}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	addrs, err := resolverFor(server, timeout).LookupIPAddr(ctx, name)
	res.Ms = round2(ms(time.Since(start)))
	switch {
	case err == nil && len(addrs) > 0:
		res.OK = true
	case err != nil:
		res.Err = shortErr(err)
	default:
		res.Err = "empty answer"
	}
	return res
}

// WebCheck performs one HTTP(S) request, resolving names through the given public resolvers
// so that a broken ISP resolver does not hide the state of the web layer.
type WebCheck struct {
	client *http.Client
}

// NewWebCheck builds a client that never reuses connections and ignores proxy settings.
func NewWebCheck(resolvers []string, timeout time.Duration) *WebCheck {
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var last error
		for _, r := range resolvers {
			d := &net.Dialer{Timeout: timeout, Resolver: resolverFor(r, timeout/2)}
			c, err := d.DialContext(ctx, network, addr)
			if err == nil {
				return c, nil
			}
			last = err
			var dnsErr *net.DNSError
			if !errors.As(err, &dnsErr) { // resolved but could not connect: no point trying another resolver
				return nil, err
			}
		}
		if last == nil {
			last = errors.New("no resolver configured")
		}
		return nil, last
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return &WebCheck{client: &http.Client{
		Transport: tr,
		Timeout:   timeout + timeout/2,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // a redirect usually means a captive portal or filtering
		},
	}}
}

// Get fetches url. It returns the result and, for Cloudflare's trace endpoint, the public IP.
func (w *WebCheck) Get(ctx context.Context, url string) (model.HTTPResult, string) {
	res := model.HTTPResult{URL: url}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		res.Err = shortErr(err)
		return res, ""
	}
	req.Header.Set("User-Agent", "BlameBox (+https://blamebox.github.io)")
	start := time.Now()
	resp, err := w.client.Do(req)
	if err != nil {
		res.Ms = round2(ms(time.Since(start)))
		res.Err = shortErr(err)
		return res, ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	res.Ms = round2(ms(time.Since(start)))
	res.Status = resp.StatusCode
	ip := ""
	switch {
	case strings.HasSuffix(url, "/generate_204"):
		res.OK = resp.StatusCode == http.StatusNoContent
		if !res.OK {
			res.Err = fmt.Sprintf("unexpected answer %d (captive portal or filtering?)", resp.StatusCode)
		}
	case strings.Contains(url, "/cdn-cgi/trace"):
		ip = traceIP(string(body))
		res.OK = resp.StatusCode == http.StatusOK && ip != ""
		if !res.OK {
			res.Err = fmt.Sprintf("unexpected answer %d", resp.StatusCode)
		}
	default:
		res.OK = resp.StatusCode >= 200 && resp.StatusCode < 400
		if !res.OK {
			res.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	return res, ip
}

func traceIP(body string) string {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "ip="); ok {
			if net.ParseIP(strings.TrimSpace(v)) != nil {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

// shortErr keeps error messages readable in the interface.
func shortErr(err error) string {
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	case errors.As(err, &dnsErr):
		if dnsErr.IsTimeout {
			return "dns timeout"
		}
		if dnsErr.IsNotFound {
			return "no such host"
		}
		return "dns: " + dnsErr.Err
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 80 {
		s = s[i+2:]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}
