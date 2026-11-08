// Package model holds the data types shared by every BlameBox component.
package model

import "time"

// PingResult is the outcome of a short burst of echo requests to one target.
type PingResult struct {
	Target string  `json:"t"`
	Sent   int     `json:"s"`
	Recv   int     `json:"r"`
	RTTms  float64 `json:"rtt"`         // median round-trip time of the answered probes, -1 when none answered
	Method string  `json:"m,omitempty"` // "icmp" or "tcp"
	Err    string  `json:"e,omitempty"`
}

// OK reports whether at least one probe was answered.
func (p PingResult) OK() bool { return p.Recv > 0 }

// Loss returns the share of unanswered probes, between 0 and 1.
func (p PingResult) Loss() float64 {
	if p.Sent == 0 {
		return 0
	}
	return float64(p.Sent-p.Recv) / float64(p.Sent)
}

// DNSResult is the outcome of name lookups against one resolver.
type DNSResult struct {
	Server string  `json:"srv"`
	Name   string  `json:"n,omitempty"`
	OK     bool    `json:"ok"`
	Ms     float64 `json:"ms"`
	Skip   bool    `json:"skip,omitempty"` // check disabled (e.g. the router does not run a resolver)
	Err    string  `json:"e,omitempty"`
}

// HTTPResult is the outcome of a web request.
type HTTPResult struct {
	URL    string  `json:"u"`
	OK     bool    `json:"ok"`
	Status int     `json:"st,omitempty"`
	Ms     float64 `json:"ms"`
	Err    string  `json:"e,omitempty"`
}

// Sample is one measurement cycle.
type Sample struct {
	T        time.Time    `json:"t"`
	Gateway  PingResult   `json:"gw"`
	Internet []PingResult `json:"inet"`
	DNSLocal DNSResult    `json:"dnsl"`
	DNSPub   []DNSResult  `json:"dnsp"`
	Web      HTTPResult   `json:"web"`
	PublicIP string       `json:"ip,omitempty"`
	Diag     Diagnosis    `json:"d"`
}

// InternetOK reports whether at least one public IP answered.
func (s Sample) InternetOK() bool {
	for _, p := range s.Internet {
		if p.OK() {
			return true
		}
	}
	return false
}

// InternetLoss returns the packet loss over every public target of the cycle.
func (s Sample) InternetLoss() float64 {
	sent, recv := 0, 0
	for _, p := range s.Internet {
		sent += p.Sent
		recv += p.Recv
	}
	if sent == 0 {
		return 0
	}
	return float64(sent-recv) / float64(sent)
}

// InternetRTT returns the best median RTT among public targets, -1 if none answered.
func (s Sample) InternetRTT() float64 {
	best := -1.0
	for _, p := range s.Internet {
		if p.RTTms >= 0 && p.Recv > 0 && (best < 0 || p.RTTms < best) {
			best = p.RTTms
		}
	}
	return best
}

// PublicDNSOK reports whether at least one public resolver answered.
func (s Sample) PublicDNSOK() bool {
	for _, d := range s.DNSPub {
		if d.OK {
			return true
		}
	}
	return false
}

// Status is the overall health of the connection.
type Status string

const (
	StatusOK       Status = "ok"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Cause names the layer at fault.
type Cause string

const (
	CauseNone    Cause = "none"
	CauseLAN     Cause = "lan"      // the router does not answer: home side
	CauseISP     Cause = "isp"      // router fine, Internet unreachable: line or ISP
	CauseDNSISP  Cause = "dns_isp"  // router/ISP resolver down, public resolvers fine
	CauseDNSAll  Cause = "dns_all"  // every resolver fails while IPs answer
	CauseWeb     Cause = "web"      // names resolve but web requests fail
	CauseLossLAN Cause = "loss_lan" // packet loss already between the probe and the router
	CauseLossISP Cause = "loss_isp" // packet loss beyond the router
)

// IsOutage reports whether the cause makes the connection unusable.
func (c Cause) IsOutage() bool {
	switch c {
	case CauseLAN, CauseISP, CauseDNSISP, CauseDNSAll, CauseWeb:
		return true
	}
	return false
}

// HomeSide reports whether the cause is on the user's side of the router.
func (c Cause) HomeSide() bool { return c == CauseLAN || c == CauseLossLAN }

// Layer states.
const (
	LayerOK   = "ok"
	LayerFail = "fail"
	LayerWarn = "warn"
	LayerSkip = "skip" // not tested because an earlier layer failed
)

// Layers is the state of each step of the chain, from the router to the web.
type Layers struct {
	Box      string `json:"box"`
	Internet string `json:"internet"`
	DNS      string `json:"dns"`
	Web      string `json:"web"`
}

// Diagnosis is the verdict for one cycle.
type Diagnosis struct {
	Status Status `json:"s"`
	Cause  Cause  `json:"c"`
	Layers Layers `json:"l"`
}

// Incident is a period during which the connection was down or degraded.
type Incident struct {
	ID       string        `json:"id"`
	Kind     string        `json:"kind"` // "outage" or "degradation"
	Cause    Cause         `json:"cause"`
	Start    time.Time     `json:"start"`
	End      *time.Time    `json:"end,omitempty"` // nil while ongoing
	Cycles   int           `json:"cycles"`        // failing cycles observed
	Counts   map[Cause]int `json:"counts"`        // failing cycles per cause
	Evidence Evidence      `json:"evidence"`
	Note     string        `json:"note,omitempty"`
}

// Duration returns the incident length, up to now when it is ongoing.
func (i Incident) Duration(now time.Time) time.Duration {
	if i.End != nil {
		return i.End.Sub(i.Start)
	}
	return now.Sub(i.Start)
}

// Evidence holds the measured facts that support the incident's cause.
type Evidence struct {
	GatewayAnswered int     `json:"gw_ok"` // failing cycles where the router still answered
	InternetLossMax float64 `json:"inet_loss_max"`
	InternetLossAvg float64 `json:"inet_loss_avg"`
	GatewayLossAvg  float64 `json:"gw_loss_avg"`
	PublicDNSOK     int     `json:"pub_dns_ok"` // failing cycles where public resolvers answered
	IPBefore        string  `json:"ip_before,omitempty"`
	IPAfter         string  `json:"ip_after,omitempty"`
}

// Event is a notable fact that is not an incident (e.g. a public IP change).
type Event struct {
	T      time.Time `json:"t"`
	Kind   string    `json:"kind"` // "ip_change", "start", "dns_local_disabled"
	Detail string    `json:"detail,omitempty"`
}
