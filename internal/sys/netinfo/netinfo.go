// Package netinfo membaca interface, route, DNS, dan koneksi aktif, serta menjalankan pemeriksaan
// konektivitas bertahap. Murni pengumpul data.
package netinfo

import (
	"bufio"
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"sort"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
)

// Addr adalah satu alamat IP di interface.
type Addr struct {
	Family string `json:"family"` // inet / inet6
	Local  string `json:"local"`
	Prefix int    `json:"prefixlen"`
	Scope  string `json:"scope"` // global, link, host
}

// Interface adalah satu antarmuka jaringan.
type Interface struct {
	Name     string   `json:"ifname"`
	State    string   `json:"operstate"`
	LinkType string   `json:"link_type"`
	Flags    []string `json:"flags"`
	MAC      string   `json:"address"`
	MTU      int      `json:"mtu"`
	Addrs    []Addr   `json:"addr_info"`
}

// Up melaporkan interface menyala dan kabel/sinyal terhubung.
func (i Interface) Up() bool {
	return i.State == "UP" || (i.State == "UNKNOWN" && hasFlag(i.Flags, "LOWER_UP"))
}

// Virtual melaporkan interface buatan Docker/VM/bridge yang jarang relevan bagi pemula.
func (i Interface) Virtual() bool {
	for _, p := range []string{"veth", "docker", "br-", "virbr", "vnet", "lxcbr", "tun", "tap", "cni", "flannel", "cali"} {
		if strings.HasPrefix(i.Name, p) {
			return true
		}
	}
	return false
}

// GlobalIPv4 mengembalikan IPv4 non-loopback pertama.
func (i Interface) GlobalIPv4() string {
	for _, a := range i.Addrs {
		if a.Family == "inet" && a.Scope == "global" {
			return a.Local
		}
	}
	return ""
}

func hasFlag(flags []string, f string) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// Route adalah satu baris tabel routing.
type Route struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol"`
	PrefSrc  string `json:"prefsrc"`
	Metric   int    `json:"metric"`
}

// ParseInterfaces mem-parse `ip -j addr`.
func ParseInterfaces(out string) ([]Interface, error) {
	var ifs []Interface
	err := json.Unmarshal([]byte(strings.TrimSpace(out)), &ifs)
	return ifs, err
}

// ParseRoutes mem-parse `ip -j route`.
func ParseRoutes(out string) ([]Route, error) {
	var rs []Route
	err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rs)
	return rs, err
}

// DefaultRoute mengembalikan route default dengan metric terkecil.
func DefaultRoute(rs []Route) (Route, bool) {
	best, found := Route{}, false
	for _, r := range rs {
		if r.Dst == "default" && (!found || r.Metric < best.Metric) {
			best, found = r, true
		}
	}
	return best, found
}

// DNS adalah konfigurasi resolver.
type DNS struct {
	Mode    string              // stub, uplink, foreign, static (resolv.conf mode dari resolvectl)
	Global  []string            // server DNS global
	PerLink map[string][]string // server DNS per interface
	Stub    bool                // /etc/resolv.conf menunjuk 127.0.0.53
}

// Servers mengembalikan semua server DNS unik.
func (d DNS) Servers() []string {
	seen := map[string]bool{}
	var out []string
	add := func(xs []string) {
		for _, x := range xs {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	add(d.Global)
	links := make([]string, 0, len(d.PerLink))
	for l := range d.PerLink {
		links = append(links, l)
	}
	sort.Strings(links)
	for _, l := range links {
		add(d.PerLink[l])
	}
	return out
}

// ParseResolvectl mem-parse `resolvectl status`.
func ParseResolvectl(out string) DNS {
	d := DNS{PerLink: map[string][]string{}}
	section := "Global"
	var lastKey string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Global":
			section, lastKey = "Global", ""
			continue
		case strings.HasPrefix(trimmed, "Link ") && strings.HasSuffix(trimmed, ")"):
			if i := strings.Index(trimmed, "("); i >= 0 {
				section = trimmed[i+1 : len(trimmed)-1]
			}
			lastKey = ""
			continue
		}
		// Baris "Kunci: nilai" punya kunci tanpa angka; baris lanjutan daftar server (termasuk IPv6
		// seperti 2001:4860::8888) diperlakukan sebagai nilai tambahan untuk kunci sebelumnya.
		key, val, ok := strings.Cut(trimmed, ":")
		if ok && !strings.ContainsAny(key, "0123456789") {
			lastKey = key
			val = strings.TrimSpace(val)
		} else {
			val = trimmed
		}
		switch lastKey {
		case "resolv.conf mode":
			d.Mode = val
		case "DNS Servers":
			for _, s := range strings.Fields(val) {
				if section == "Global" {
					d.Global = append(d.Global, s)
				} else {
					d.PerLink[section] = append(d.PerLink[section], s)
				}
			}
		}
	}
	d.Stub = d.Mode == "stub"
	return d
}

// ResolvConfServers membaca nameserver dari /etc/resolv.conf (cadangan bila resolvectl tidak ada).
func ResolvConfServers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}

// Info adalah ringkasan jaringan.
type Info struct {
	Interfaces []Interface
	Routes     []Route
	DNS        DNS
	Conns      []RemoteGroup
	ConnTotal  int
}

// Read membaca semua info jaringan.
func Read(ctx context.Context, r run.Runner, procRoot string) (Info, error) {
	var info Info
	out, _, err := r.Capture(ctx, run.Command{Argv: []string{"ip", "-j", "addr"}})
	if err != nil {
		return info, err
	}
	if info.Interfaces, err = ParseInterfaces(out); err != nil {
		return info, err
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"ip", "-j", "route"}}); err == nil {
		info.Routes, _ = ParseRoutes(out)
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"resolvectl", "status"}}); err == nil {
		info.DNS = ParseResolvectl(out)
	} else {
		info.DNS = DNS{Global: ResolvConfServers("/etc/resolv.conf")}
	}
	if socks, err := ports.ReadSockets(procRoot); err == nil {
		info.Conns, info.ConnTotal = GroupEstablished(socks)
	}
	return info, nil
}

// RemoteGroup adalah koneksi aktif yang dikelompokkan per IP remote.
type RemoteGroup struct {
	IP         string
	Count      int
	LocalPorts []int // port lokal yang dituju (untuk koneksi masuk) atau dipakai
	Inbound    bool  // sebagian koneksi masuk ke port yang listening di server ini
}

// GroupEstablished mengelompokkan koneksi TCP ESTABLISHED per IP remote, terbanyak lebih dulu.
// Koneksi loopback diabaikan.
func GroupEstablished(socks []ports.Socket) ([]RemoteGroup, int) {
	listening := map[uint16]bool{}
	for _, s := range socks {
		if s.Listening() {
			listening[s.Local.Port()] = true
		}
	}
	groups := map[netip.Addr]*RemoteGroup{}
	total := 0
	for _, s := range socks {
		if s.Proto != ports.TCP || s.State != ports.StateEstablished {
			continue
		}
		ip := s.Remote.Addr().Unmap()
		if ip.IsLoopback() {
			continue
		}
		total++
		g := groups[ip]
		if g == nil {
			g = &RemoteGroup{IP: ip.String()}
			groups[ip] = g
		}
		g.Count++
		lp := int(s.Local.Port())
		if listening[s.Local.Port()] {
			g.Inbound = true
			if !containsInt(g.LocalPorts, lp) {
				g.LocalPorts = append(g.LocalPorts, lp)
			}
		}
	}
	out := make([]RemoteGroup, 0, len(groups))
	for _, g := range groups {
		sort.Ints(g.LocalPorts)
		out = append(out, *g)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].IP < out[j].IP
	})
	return out, total
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
