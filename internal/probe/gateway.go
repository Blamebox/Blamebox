package probe

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// ErrNoRoute means the routing table has no default route.
var ErrNoRoute = errors.New("no default route")

// DefaultGateway returns the IPv4 address of the default gateway (the router)
// and the interface used to reach it, read from /proc/net/route.
func DefaultGateway() (ip string, iface string, err error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	return parseRoute(f)
}

// parseRoute picks the default route with the lowest metric.
func parseRoute(r io.Reader) (string, string, error) {
	sc := bufio.NewScanner(r)
	best, bestIface, bestMetric := "", "", -1
	first := true
	for sc.Scan() {
		if first { // header line
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 8 {
			continue
		}
		dest, gw, flags, mask := f[1], f[2], f[3], f[7]
		if dest != "00000000" || mask != "00000000" {
			continue
		}
		fl, err := strconv.ParseUint(flags, 16, 32)
		if err != nil || fl&0x1 == 0 || fl&0x2 == 0 { // RTF_UP and RTF_GATEWAY
			continue
		}
		ip, err := hexToIPv4(gw)
		if err != nil || ip.IsUnspecified() {
			continue
		}
		metric, _ := strconv.Atoi(f[6])
		if bestMetric < 0 || metric < bestMetric {
			best, bestIface, bestMetric = ip.String(), f[0], metric
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if best == "" {
		return "", "", ErrNoRoute
	}
	return best, bestIface, nil
}

// hexToIPv4 decodes the little-endian hexadecimal form used by /proc/net/route.
func hexToIPv4(s string) (net.IP, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return nil, errors.New("bad address")
	}
	v := binary.LittleEndian.Uint32(b)
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, v)
	return ip, nil
}
