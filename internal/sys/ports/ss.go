package ports

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

var ssUserRe = regexp.MustCompile(`\("([^"]*)",pid=(\d+),fd=\d+\)`)

// ParseSS mem-parse output `ss -tulpnH`. Dipakai sebagai cadangan bila /proc/net tidak tersedia.
//
//	tcp LISTEN 0 4096 127.0.0.1:631 0.0.0.0:* users:(("cupsd",pid=1043,fd=7))
//	udp UNCONN 0 0 [fe80::1]%wlp1s0:546 [::]:*
func ParseSS(out string) []Listener {
	var ls []Listener
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		var proto Proto
		switch f[0] {
		case "tcp":
			proto = TCP
		case "udp":
			proto = UDP
		default:
			continue
		}
		local, ok := parseSSAddr(f[4])
		if !ok {
			continue
		}
		l := Listener{Socket: Socket{Proto: proto, IPv6: local.Addr().Is6(), Local: local}}
		if proto == TCP {
			l.State = StateListen
		}
		if len(f) > 6 {
			for _, m := range ssUserRe.FindAllStringSubmatch(strings.Join(f[6:], " "), -1) {
				pid, _ := strconv.Atoi(m[2])
				if !containsInt(l.PIDs, pid) {
					l.PIDs = append(l.PIDs, pid)
				}
				if l.Process == "" {
					l.Process = m[1]
				}
			}
		}
		ls = append(ls, l)
	}
	SortListeners(ls)
	return ls
}

// parseSSAddr mem-parse "127.0.0.53%lo:53", "[::]:22", "*:68".
func parseSSAddr(s string) (netip.AddrPort, bool) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return netip.AddrPort{}, false
	}
	host, portStr := s[:i], s[i+1:]
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if j := strings.IndexByte(host, '%'); j >= 0 {
		host = strings.TrimSuffix(host[:j], "]")
	}
	if host == "*" {
		host = "0.0.0.0"
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(addr, uint16(port)), true
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
