package chainverify

import (
	"fmt"
	"net"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

var (
	ipA      = net.IPv4(127, 0, 1, 1)
	ipB      = net.IPv4(127, 0, 2, 1)
	ipC      = net.IPv4(127, 0, 3, 1)
	ipD      = net.IPv4(127, 0, 4, 1)
	ipTarget = net.IPv4(127, 0, 9, 1)
)

type hop struct {
	IP       net.IP
	Host     string
	Password string
	Proxy    *isolation.Proxy
	Log      *isolation.Log
}

type topo struct {
	CA         *isolation.CertificateAuthority
	Target     *isolation.Target
	TargetLog  *isolation.Log
	A, B, C, D hop
}

func (t *topo) close() {
	if t == nil {
		return
	}
	_ = t.A.Proxy.Close()
	_ = t.B.Proxy.Close()
	_ = t.C.Proxy.Close()
	_ = t.D.Proxy.Close()
	_ = t.Target.Close()
}

func (t *topo) targetHostPort() (string, int, error) {
	host, port, err := net.SplitHostPort(t.Target.Addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	return host, n, nil
}

func (h hop) endpoint() Endpoint {
	host, port, err := net.SplitHostPort(h.Proxy.Addr)
	if err != nil {
		return Endpoint{}
	}
	n, _ := strconv.Atoi(port)
	return Endpoint{Host: host, Port: n, SNI: h.Host, Password: h.Password, Name: h.Host}
}

func startHop(ca *isolation.CertificateAuthority, ip net.IP, host, password, role string, allow []net.IP) (hop, error) {
	leaf, err := ca.Issue(host)
	if err != nil {
		return hop{}, err
	}
	log := &isolation.Log{}
	proxy, err := isolation.StartTrojan(isolation.ProxyConfig{
		Bind: net.JoinHostPort(ip.String(), "0"), Host: host, Password: password,
		Certificate: leaf.Certificate, AllowFrom: allow, DialLocal: ip, Role: role, Log: log,
	})
	if err != nil {
		return hop{}, err
	}
	return hop{IP: ip, Host: host, Password: password, Proxy: proxy, Log: log}, nil
}

func startTarget() (*isolation.Target, *isolation.Log, error) {
	log := &isolation.Log{}
	target, err := isolation.StartHTTPTarget(net.JoinHostPort(ipTarget.String(), "0"), log)
	if err != nil {
		return nil, nil, err
	}
	return target, log, nil
}

func startForwardTopo(ca *isolation.CertificateAuthority) (*topo, error) {
	target, targetLog, err := startTarget()
	if err != nil {
		return nil, err
	}
	b, err := startHop(ca, ipB, "b.proxyloom.test", "EXAMPLE_ONLY_B", "b", []net.IP{ipA})
	if err != nil {
		_ = target.Close()
		return nil, err
	}
	a, err := startHop(ca, ipA, "a.proxyloom.test", "EXAMPLE_ONLY_A", "a", nil)
	if err != nil {
		_ = b.Proxy.Close()
		_ = target.Close()
		return nil, err
	}
	return &topo{CA: ca, Target: target, TargetLog: targetLog, A: a, B: b}, nil
}

func startReverseTopo(ca *isolation.CertificateAuthority) (*topo, error) {
	target, targetLog, err := startTarget()
	if err != nil {
		return nil, err
	}
	a, err := startHop(ca, ipA, "a.proxyloom.test", "EXAMPLE_ONLY_A", "a", []net.IP{ipB})
	if err != nil {
		_ = target.Close()
		return nil, err
	}
	b, err := startHop(ca, ipB, "b.proxyloom.test", "EXAMPLE_ONLY_B", "b", nil)
	if err != nil {
		_ = a.Proxy.Close()
		_ = target.Close()
		return nil, err
	}
	return &topo{CA: ca, Target: target, TargetLog: targetLog, A: a, B: b}, nil
}

func startReuseTopo(ca *isolation.CertificateAuthority) (*topo, error) {
	target, targetLog, err := startTarget()
	if err != nil {
		return nil, err
	}
	b, err := startHop(ca, ipB, "b.proxyloom.test", "EXAMPLE_ONLY_B", "b", []net.IP{ipA, ipD})
	if err != nil {
		_ = target.Close()
		return nil, err
	}
	c, err := startHop(ca, ipC, "c.proxyloom.test", "EXAMPLE_ONLY_C", "c", []net.IP{ipA})
	if err != nil {
		_ = b.Proxy.Close()
		_ = target.Close()
		return nil, err
	}
	d, err := startHop(ca, ipD, "d.proxyloom.test", "EXAMPLE_ONLY_D", "d", nil)
	if err != nil {
		_ = c.Proxy.Close()
		_ = b.Proxy.Close()
		_ = target.Close()
		return nil, err
	}
	a, err := startHop(ca, ipA, "a.proxyloom.test", "EXAMPLE_ONLY_A", "a", nil)
	if err != nil {
		_ = d.Proxy.Close()
		_ = c.Proxy.Close()
		_ = b.Proxy.Close()
		_ = target.Close()
		return nil, err
	}
	return &topo{CA: ca, Target: target, TargetLog: targetLog, A: a, B: b, C: c, D: d}, nil
}

func startOpenBTopo(ca *isolation.CertificateAuthority) (*topo, error) {
	target, targetLog, err := startTarget()
	if err != nil {
		return nil, err
	}
	b, err := startHop(ca, ipB, "b.proxyloom.test", "EXAMPLE_ONLY_B", "b", nil)
	if err != nil {
		_ = target.Close()
		return nil, err
	}
	return &topo{CA: ca, Target: target, TargetLog: targetLog, B: b}, nil
}

func (t *topo) requireExit(role, wantIP string) error {
	event, ok := t.TargetLog.Last("target", "ok")
	if !ok {
		return fmt.Errorf("target missing ok")
	}
	if isolation.RemoteIP(event.Remote) != wantIP {
		return fmt.Errorf("target source %s, want %s (%s)", event.Remote, wantIP, role)
	}
	return nil
}

func (t *topo) unexpectedTargetTraffic(seq int) []isolation.Event {
	var out []isolation.Event
	for _, event := range t.TargetLog.After(seq) {
		if event.Result == "ok" || event.Result == "accept" {
			out = append(out, event)
		}
	}
	return out
}
