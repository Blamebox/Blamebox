package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const routeTable = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
wlan0	00000000	FE01A8C0	0003	0	0	600	00000000	0	0	0
docker0	000011AC	00000000	0001	0	0	0	0000FFFF	0	0	0
`

func TestParseRoute(t *testing.T) {
	ip, iface, err := parseRoute(strings.NewReader(routeTable))
	if err != nil || ip != "192.168.1.1" || iface != "eth0" {
		t.Fatalf("got %s %s %v, want 192.168.1.1 on eth0 (lowest metric)", ip, iface, err)
	}
	if _, _, err := parseRoute(strings.NewReader("Iface\tDestination\n")); err != ErrNoRoute {
		t.Fatalf("empty table: %v", err)
	}
}

func TestChecksum(t *testing.T) {
	b := echoRequest(0x1234, 7)
	if checksum(b) != 0 { // a correct checksum makes the sum of the whole message zero
		t.Fatal("bad ICMP checksum")
	}
}

func TestMedian(t *testing.T) {
	if m := median([]float64{3, 1, 2}); m != 2 {
		t.Fatal(m)
	}
	if m := median([]float64{4, 1, 2, 3}); m != 2.5 {
		t.Fatal(m)
	}
}

func TestPingLoopback(t *testing.T) {
	p := NewPinger()
	r := p.Ping(context.Background(), "127.0.0.1", 2, time.Second)
	if p.Method != MethodTCP && r.Recv == 0 {
		t.Fatalf("loopback should answer (%s): %+v", p.Method, r)
	}
	if r := p.Ping(context.Background(), "not-an-ip", 1, time.Second); r.Err == "" {
		t.Fatal("expected an error for a bad address")
	}
}

func TestPingTCPRefusedCountsAsUp(t *testing.T) {
	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	l.Close() // nothing listens any more on 443/80/53 of 127.0.0.2 either: refused = host up
	p := &Pinger{Method: MethodTCP}
	r := p.Ping(context.Background(), "127.0.0.1", 2, time.Second)
	if r.Recv == 0 {
		t.Fatalf("a refused TCP connection proves the host is up: %+v", r)
	}
}

func TestWebCheck(t *testing.T) {
	trace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cdn-cgi/trace":
			w.Write([]byte("fl=1\nip=203.0.113.7\nts=1\n"))
		case "/generate_204":
			w.WriteHeader(http.StatusNoContent)
		case "/portal/generate_204":
			w.Write([]byte("<html>login</html>"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer trace.Close()
	wc := NewWebCheck([]string{"127.0.0.1"}, 2*time.Second)
	res, ip := wc.Get(context.Background(), trace.URL+"/cdn-cgi/trace")
	if !res.OK || ip != "203.0.113.7" {
		t.Fatalf("trace: %+v ip=%q", res, ip)
	}
	if res, _ := wc.Get(context.Background(), trace.URL+"/generate_204"); !res.OK {
		t.Fatalf("204: %+v", res)
	}
	if res, _ := wc.Get(context.Background(), trace.URL+"/portal/generate_204"); res.OK {
		t.Fatal("a 200 page instead of 204 means a captive portal: must fail")
	}
	if res, _ := wc.Get(context.Background(), trace.URL+"/missing"); res.OK || res.Status != 404 {
		t.Fatalf("404: %+v", res)
	}
}

func TestLookupViaDeadServer(t *testing.T) {
	// Nothing answers DNS on this loopback port; the lookup must fail quickly.
	start := time.Now()
	r := LookupVia(context.Background(), "127.0.0.9", "example.com", 500*time.Millisecond)
	if r.OK || time.Since(start) > 3*time.Second {
		t.Fatalf("dead resolver: %+v after %s", r, time.Since(start))
	}
}
