// Command fakenet simulates a home network on loopback addresses for end-to-end tests:
//
//	127.0.0.2   the router (answers pings and DNS)
//	127.0.0.3/4 two "public" servers (answer pings and DNS)
//	127.0.0.5   a web server (/generate_204 and /cdn-cgi/trace)
//
// Run it inside an isolated network namespace (see test/e2e.sh), then break links with iptables.
package main

import (
	"encoding/binary"
	"log"
	"net"
	"net/http"
)

func main() {
	for _, ip := range []string{"127.0.0.2", "127.0.0.3", "127.0.0.4"} {
		pc, err := net.ListenPacket("udp4", ip+":53")
		if err != nil {
			log.Fatal(err)
		}
		go serveDNS(pc)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/generate_204", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/cdn-cgi/trace", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fl=1\nip=203.0.113.9\nts=0\n"))
	})
	log.Fatal(http.ListenAndServe("127.0.0.5:80", mux))
}

// serveDNS answers every A question with 127.0.0.5 and every other type with an empty answer.
func serveDNS(pc net.PacketConn) {
	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 12 {
			continue
		}
		q := buf[:n]
		// Find the end of the question name.
		i := 12
		for i < n && q[i] != 0 {
			i += int(q[i]) + 1
		}
		if i+5 > n {
			continue
		}
		qtype := binary.BigEndian.Uint16(q[i+1:])
		question := q[12 : i+5]
		resp := make([]byte, 12, 64+len(question))
		copy(resp, q[:2])                            // id
		binary.BigEndian.PutUint16(resp[2:], 0x8180) // response, recursion available, no error
		binary.BigEndian.PutUint16(resp[4:], 1)      // one question
		if qtype == 1 {
			binary.BigEndian.PutUint16(resp[6:], 1) // one answer
		}
		resp = append(resp, question...)
		if qtype == 1 {
			resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 127, 0, 0, 5)
		}
		pc.WriteTo(resp, addr)
	}
}
