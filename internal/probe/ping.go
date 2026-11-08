package probe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/blamebox/blamebox/internal/model"
)

// Ping methods, from the most to the least precise.
const (
	MethodRaw   = "icmp"    // raw ICMP socket (root or CAP_NET_RAW)
	MethodDgram = "icmp-dg" // unprivileged ICMP socket (net.ipv4.ping_group_range)
	MethodTCP   = "tcp"     // TCP connection attempt, no privilege needed
)

// spacing between two echo requests of the same burst.
const spacing = 150 * time.Millisecond

// Pinger sends short bursts of echo requests.
type Pinger struct {
	Method string
}

// NewPinger picks the best method available on this system.
func NewPinger() *Pinger {
	if c, err := net.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		c.Close()
		return &Pinger{Method: MethodRaw}
	}
	if fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP); err == nil {
		syscall.Close(fd)
		return &Pinger{Method: MethodDgram}
	}
	return &Pinger{Method: MethodTCP}
}

// Ping sends count probes to target and waits up to timeout for each answer.
func (p *Pinger) Ping(ctx context.Context, target string, count int, timeout time.Duration) model.PingResult {
	res := model.PingResult{Target: target, RTTms: -1, Method: p.Method}
	ip := net.ParseIP(target).To4()
	if ip == nil {
		res.Err = "not an IPv4 address"
		return res
	}
	if count < 1 {
		count = 1
	}
	var rtts []float64
	var err error
	switch p.Method {
	case MethodRaw:
		rtts, err = pingRaw(ctx, ip, count, timeout)
	case MethodDgram:
		rtts, err = pingDgram(ctx, ip, count, timeout)
	default:
		rtts, err = pingTCP(ctx, ip, count, timeout)
	}
	res.Sent = count
	res.Recv = len(rtts)
	if len(rtts) > 0 {
		res.RTTms = median(rtts)
	}
	if err != nil && res.Recv == 0 {
		res.Err = err.Error()
	}
	return res
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return round2(s[n/2])
	}
	return round2((s[n/2-1] + s[n/2]) / 2)
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// echoRequest builds an ICMP echo request with an 8-byte timestamp payload plus padding.
func echoRequest(id, seq uint16) []byte {
	b := make([]byte, 8+24)
	b[0] = 8 // echo request
	binary.BigEndian.PutUint16(b[4:], id)
	binary.BigEndian.PutUint16(b[6:], seq)
	binary.BigEndian.PutUint64(b[8:], uint64(time.Now().UnixNano()))
	for i := 16; i < len(b); i++ {
		b[i] = byte(i)
	}
	cs := checksum(b)
	binary.BigEndian.PutUint16(b[2:], cs)
	return b
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = (s & 0xffff) + (s >> 16)
	}
	return ^uint16(s)
}

// burst sends count requests with send() and collects answers reported by recv().
// recv must return the sequence number of a matching echo reply, or an error when it times out.
func burst(ctx context.Context, count int, timeout time.Duration,
	send func(seq uint16) error, recv func(deadline time.Time) (uint16, error)) ([]float64, error) {

	var mu sync.Mutex
	sentAt := make([]time.Time, count)
	got := make(map[uint16]bool, count)
	var rtts []float64
	deadline := time.Now().Add(time.Duration(count-1)*spacing + timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			seq, err := recv(deadline)
			if err != nil {
				return
			}
			mu.Lock()
			if int(seq) < count && !got[seq] && !sentAt[seq].IsZero() {
				got[seq] = true
				rtts = append(rtts, ms(time.Since(sentAt[seq])))
			}
			n := len(got)
			mu.Unlock()
			if n == count {
				return
			}
		}
	}()
	var sendErr error
	for i := 0; i < count; i++ {
		mu.Lock()
		sentAt[i] = time.Now()
		mu.Unlock()
		if err := send(uint16(i)); err != nil && sendErr == nil {
			sendErr = err
		}
		if i < count-1 {
			select {
			case <-ctx.Done():
				i = count
			case <-time.After(spacing):
			}
		}
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(rtts) == 0 {
		if sendErr != nil {
			return nil, sendErr
		}
		return nil, errors.New("no reply")
	}
	return append([]float64(nil), rtts...), nil
}

func pingRaw(ctx context.Context, ip net.IP, count int, timeout time.Duration) ([]float64, error) {
	c, err := net.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	id := uint16(rand.IntN(0xffff) + 1)
	dst := &net.IPAddr{IP: ip}
	buf := make([]byte, 1500)
	return burst(ctx, count, timeout,
		func(seq uint16) error { _, err := c.WriteTo(echoRequest(id, seq), dst); return err },
		func(deadline time.Time) (uint16, error) {
			c.SetReadDeadline(deadline)
			for {
				n, from, err := c.ReadFrom(buf)
				if err != nil {
					return 0, err
				}
				msg := buf[:n]
				// Some kernels hand the IPv4 header over; skip it when present.
				if n >= 20 && msg[0]>>4 == 4 {
					hl := int(msg[0]&0x0f) * 4
					if hl < n {
						msg = msg[hl:]
					}
				}
				if len(msg) < 8 || msg[0] != 0 { // echo reply only
					continue
				}
				fa, ok := from.(*net.IPAddr)
				if !ok || !fa.IP.Equal(ip) || binary.BigEndian.Uint16(msg[4:]) != id {
					continue
				}
				return binary.BigEndian.Uint16(msg[6:]), nil
			}
		})
}

func pingDgram(ctx context.Context, ip net.IP, count int, timeout time.Duration) ([]float64, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_ICMP)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	closeFD := func() { once.Do(func() { syscall.Close(fd) }) }
	defer closeFD()
	sa := &syscall.SockaddrInet4{}
	copy(sa.Addr[:], ip)
	buf := make([]byte, 1500)
	return burst(ctx, count, timeout,
		func(seq uint16) error { return syscall.Sendto(fd, echoRequest(0, seq), 0, sa) },
		func(deadline time.Time) (uint16, error) {
			for {
				left := time.Until(deadline)
				if left <= 0 {
					return 0, errors.New("timeout")
				}
				tv := syscall.NsecToTimeval(left.Nanoseconds())
				syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
				n, from, err := syscall.Recvfrom(fd, buf, 0)
				if err != nil {
					if err == syscall.EINTR {
						continue
					}
					return 0, err
				}
				f4, ok := from.(*syscall.SockaddrInet4)
				if !ok || !net.IP(f4.Addr[:]).Equal(ip) || n < 8 || buf[0] != 0 {
					continue
				}
				return binary.BigEndian.Uint16(buf[6:]), nil
			}
		})
}

// tcpPorts are tried in order; a refused connection still proves the host is up.
var tcpPorts = []string{"443", "80", "53"}

func pingTCP(ctx context.Context, ip net.IP, count int, timeout time.Duration) ([]float64, error) {
	var rtts []float64
	var lastErr error
	port := ""
	for i := 0; i < count; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return rtts, ctx.Err()
			case <-time.After(spacing):
			}
		}
		ports := tcpPorts
		if port != "" {
			ports = []string{port}
		}
		for _, pt := range ports {
			start := time.Now()
			d := net.Dialer{Timeout: timeout}
			c, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), pt))
			el := time.Since(start)
			if err == nil {
				c.Close()
				rtts = append(rtts, ms(el))
				port = pt
				break
			}
			if errors.Is(err, syscall.ECONNREFUSED) {
				rtts = append(rtts, ms(el))
				port = pt
				break
			}
			lastErr = err
		}
		if port == "" { // nothing answered: keep the next probes short
			port = tcpPorts[0]
		}
	}
	if len(rtts) == 0 && lastErr != nil {
		return nil, fmt.Errorf("tcp: %w", lastErr)
	}
	return rtts, nil
}
