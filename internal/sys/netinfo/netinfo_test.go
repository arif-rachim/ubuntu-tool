package netinfo

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/tlscheck"
)

const ipAddr = `[{"ifindex":1,"ifname":"lo","flags":["LOOPBACK","UP","LOWER_UP"],"mtu":65536,"operstate":"UNKNOWN","link_type":"loopback","address":"00:00:00:00:00:00","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},
{"ifindex":2,"ifname":"eth0","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"52:54:00:12:34:56","addr_info":[{"family":"inet","local":"10.0.0.5","prefixlen":24,"scope":"global"},{"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link"}]},
{"ifindex":3,"ifname":"veth1234","flags":["UP"],"mtu":1500,"operstate":"UP","link_type":"ether","addr_info":[]}]`

const ipRoute = `[{"dst":"default","gateway":"10.0.0.1","dev":"eth0","protocol":"dhcp","prefsrc":"10.0.0.5","metric":100,"flags":[]},
{"dst":"default","gateway":"10.9.9.1","dev":"eth1","metric":600,"flags":[]},
{"dst":"10.0.0.0/24","dev":"eth0","protocol":"kernel","scope":"link","prefsrc":"10.0.0.5","flags":[]}]`

func TestParseIP(t *testing.T) {
	ifs, err := ParseInterfaces(ipAddr)
	if err != nil || len(ifs) != 3 {
		t.Fatal(err, len(ifs))
	}
	if !ifs[0].Up() || !ifs[1].Up() || ifs[1].GlobalIPv4() != "10.0.0.5" || ifs[1].Virtual() || !ifs[2].Virtual() {
		t.Errorf("%+v", ifs)
	}
	rs, _ := ParseRoutes(ipRoute)
	if def, ok := DefaultRoute(rs); !ok || def.Gateway != "10.0.0.1" {
		t.Errorf("route default dengan metric terkecil: %+v", def)
	}
}

func TestParseResolvectl(t *testing.T) {
	out := `Global
         Protocols: -LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
  resolv.conf mode: stub
       DNS Servers: 9.9.9.9
                    2001:4860:4860::8888

Link 3 (wlp1s0)
    Current Scopes: DNS
Current DNS Server: 192.168.1.1
       DNS Servers: 192.168.1.1 8.8.4.4

Link 4 (docker0)
    Current Scopes: none
`
	d := ParseResolvectl(out)
	if !d.Stub || !reflect.DeepEqual(d.Global, []string{"9.9.9.9", "2001:4860:4860::8888"}) || !reflect.DeepEqual(d.PerLink["wlp1s0"], []string{"192.168.1.1", "8.8.4.4"}) {
		t.Errorf("%+v", d)
	}
	if got := strings.Join(d.Servers(), ","); got != "9.9.9.9,2001:4860:4860::8888,192.168.1.1,8.8.4.4" {
		t.Errorf("Servers: %s", got)
	}
}

func sock(local, remote string, state int) ports.Socket {
	return ports.Socket{Proto: ports.TCP, Local: netip.MustParseAddrPort(local), Remote: netip.MustParseAddrPort(remote), State: state}
}

func TestGroupEstablished(t *testing.T) {
	socks := []ports.Socket{
		sock("0.0.0.0:22", "0.0.0.0:0", ports.StateListen),
		sock("10.0.0.5:22", "203.0.113.9:50000", ports.StateEstablished),
		sock("10.0.0.5:22", "203.0.113.9:50001", ports.StateEstablished),
		sock("10.0.0.5:40000", "140.82.112.3:443", ports.StateEstablished),
		sock("127.0.0.1:5432", "127.0.0.1:40001", ports.StateEstablished),
	}
	groups, total := GroupEstablished(socks)
	if total != 3 || len(groups) != 2 || groups[0].IP != "203.0.113.9" || groups[0].Count != 2 || !groups[0].Inbound || groups[1].Inbound {
		t.Errorf("%+v total %d", groups, total)
	}
	if !reflect.DeepEqual(groups[0].LocalPorts, []int{22}) {
		t.Errorf("port lokal: %v", groups[0].LocalPorts)
	}
}

func TestParseTarget(t *testing.T) {
	tests := map[string]Target{
		"contoh.com":                 {Host: "contoh.com", Port: "443", Scheme: "https"},
		"contoh.com:5432":            {Host: "contoh.com", Port: "5432"},
		"10.0.0.9:80":                {Host: "10.0.0.9", Port: "80", Scheme: "http"},
		"https://api.contoh.com/x":   {Host: "api.contoh.com", Port: "443", Scheme: "https"},
		"http://contoh.com:8080/app": {Host: "contoh.com", Port: "8080", Scheme: "http"},
		"[2001:db8::1]:22":           {Host: "2001:db8::1", Port: "22"},
	}
	for in, want := range tests {
		got, err := ParseTarget(in)
		if err != nil || got != want {
			t.Errorf("ParseTarget(%q) = %+v, %v; ingin %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "contoh.com:99999", "ftp://x", "a b"} {
		if _, err := ParseTarget(bad); err == nil {
			t.Errorf("ParseTarget(%q) harus error", bad)
		}
	}
}

// runSteps menjalankan langkah sampai gagal pertama, seperti layar diagnosa.
func runSteps(steps []Step) []StepResult {
	var out []StepResult
	for _, s := range steps {
		r := s.Run(context.Background())
		out = append(out, r)
		if r.Status == StatusFail {
			break
		}
	}
	return out
}

func baseOutbound() OutboundEnv {
	ifs, _ := ParseInterfaces(ipAddr)
	rs, _ := ParseRoutes(ipRoute)
	return OutboundEnv{
		Runner:     &run.Fake{Responses: map[string]run.FakeResponse{"ping -c 2 -W 2 10.0.0.1": {}, "ping -c 2 -W 2 contoh.com": {Err: errors.New("100% packet loss")}}},
		Info:       func(context.Context) (Info, error) { return Info{Interfaces: ifs, Routes: rs}, nil },
		Resolve:    func(context.Context, string) ([]string, error) { return []string{"93.184.216.34"}, nil },
		ResolveVia: func(context.Context, string, string) ([]string, error) { return nil, errors.New("x") },
		Dial:       func(context.Context, string) error { return nil },
		HTTP:       func(context.Context, string) (int, time.Duration, error) { return 200, 120 * time.Millisecond, nil },
		TLS: func(context.Context, string, string) tlscheck.Result {
			return tlscheck.Result{Cert: &tlscheck.Cert{Issuer: "R11", NotAfter: time.Now().Add(60 * 24 * time.Hour)}}
		},
		Now: time.Now,
	}
}

func TestOutboundSemuaLolos(t *testing.T) {
	target, _ := ParseTarget("contoh.com")
	res := runSteps(OutboundSteps(baseOutbound(), target))
	if len(res) != 7 {
		t.Fatalf("7 langkah untuk https, dapat %d", len(res))
	}
	if res[3].Status != StatusWarn {
		t.Error("ping tujuan gagal hanya peringatan (ICMP bisa diblok)")
	}
	for i, r := range res {
		if r.Status == StatusFail {
			t.Errorf("langkah %d gagal: %+v", i, r)
		}
	}
}

func TestOutboundDNSLokalRusak(t *testing.T) {
	env := baseOutbound()
	env.Resolve = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	env.ResolveVia = func(context.Context, string, string) ([]string, error) { return []string{"93.184.216.34"}, nil }
	target, _ := ParseTarget("contoh.com")
	res := runSteps(OutboundSteps(env, target))
	last := res[len(res)-1]
	if last.Status != StatusFail || !strings.Contains(last.Summary, "DNS publik 1.1.1.1 berhasil") {
		t.Errorf("harus berhenti di DNS dengan diagnosis resolver lokal: %+v", last)
	}
}

func TestOutboundPortDitolakDanTimeout(t *testing.T) {
	env := baseOutbound()
	env.Dial = func(context.Context, string) error { return &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED} }
	target, _ := ParseTarget("contoh.com:5432")
	res := runSteps(OutboundSteps(env, target))
	if len(res) != 5 || !strings.Contains(res[4].Summary, "koneksi ditolak") {
		t.Errorf("port ditolak: %+v", res)
	}
	sum, _ := DescribeDialError(&net.OpError{Op: "dial", Err: timeoutErr{}})
	if sum != "tidak ada jawaban (timeout)" {
		t.Errorf("timeout: %s", sum)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestInboundBindLocalhost(t *testing.T) {
	l := ports.Listener{Socket: sock("127.0.0.1:3000", "0.0.0.0:0", ports.StateListen)}
	env := InboundEnv{
		ListenRead: func(string) (ports.Snapshot, error) { return ports.Snapshot{Listeners: []ports.Listener{l}}, nil },
		ReadFile:   func(string) ([]byte, error) { return []byte("ENABLED=yes\n"), nil },
	}
	res := runSteps(InboundSteps(env, 3000))
	last := res[len(res)-1]
	if len(res) != 2 || last.Status != StatusFail || !strings.Contains(last.Explain, "0.0.0.0") {
		t.Errorf("harus mendeteksi bind localhost: %+v", res)
	}

	res = runSteps(InboundSteps(env, 8080))
	if len(res) != 1 || !strings.Contains(res[0].Summary, "tidak ada aplikasi") {
		t.Errorf("port tanpa aplikasi: %+v", res)
	}
	if !UFWEnabled([]byte("# comment\nENABLED=yes\n")) || UFWEnabled([]byte("ENABLED=no")) {
		t.Error("UFWEnabled salah")
	}
}
