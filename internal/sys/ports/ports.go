// Package ports membaca socket yang terbuka langsung dari /proc/net/* dan memetakan pemiliknya
// lewat /proc/*/fd. Paket ini murni pengumpul data: tidak mengimpor Bubble Tea.
package ports

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Proto adalah protokol transport.
type Proto string

const (
	TCP Proto = "tcp"
	UDP Proto = "udp"
)

// State TCP dari kolom "st" /proc/net/tcp (heksadesimal).
const (
	StateEstablished = 0x01
	StateListen      = 0x0A
)

// Socket adalah satu baris /proc/net/{tcp,tcp6,udp,udp6}.
type Socket struct {
	Proto  Proto
	IPv6   bool
	Local  netip.AddrPort
	Remote netip.AddrPort
	State  int
	UID    int
	Inode  uint64
}

// Listening melaporkan apakah socket menunggu koneksi: TCP LISTEN, atau UDP yang ter-bind
// tanpa tujuan (tidak "connected").
func (s Socket) Listening() bool {
	if s.Proto == TCP {
		return s.State == StateListen
	}
	return s.Local.Port() != 0 && s.Remote.Port() == 0
}

// ParseProcNet mem-parse isi /proc/net/tcp, tcp6, udp, atau udp6.
func ParseProcNet(r io.Reader, proto Proto, ipv6 bool) ([]Socket, error) {
	var out []Socket
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if strings.Contains(line, "local_address") {
				continue
			}
		}
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		local, err := parseAddrPort(f[1], ipv6)
		if err != nil {
			return nil, fmt.Errorf("alamat lokal %q: %w", f[1], err)
		}
		remote, err := parseAddrPort(f[2], ipv6)
		if err != nil {
			return nil, fmt.Errorf("alamat remote %q: %w", f[2], err)
		}
		state, err := strconv.ParseInt(f[3], 16, 32)
		if err != nil {
			return nil, fmt.Errorf("state %q: %w", f[3], err)
		}
		uid, _ := strconv.Atoi(f[7])
		inode, _ := strconv.ParseUint(f[9], 10, 64)
		out = append(out, Socket{Proto: proto, IPv6: ipv6, Local: local, Remote: remote, State: int(state), UID: uid, Inode: inode})
	}
	return out, sc.Err()
}

// parseAddrPort mengubah "0100007F:1F90" menjadi 127.0.0.1:8080.
// Kernel mencetak alamat sebagai word 32-bit dalam urutan byte mesin, jadi urutan byte
// dikembalikan dengan NativeEndian (benar di amd64/arm64 maupun mesin big-endian).
func parseAddrPort(s string, ipv6 bool) (netip.AddrPort, error) {
	hexAddr, hexPort, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, errors.New("format tidak dikenal")
	}
	port, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	words := 1
	if ipv6 {
		words = 4
	}
	if len(hexAddr) != words*8 {
		return netip.AddrPort{}, fmt.Errorf("panjang alamat %d", len(hexAddr))
	}
	buf := make([]byte, 0, words*4)
	for i := 0; i < words; i++ {
		v, err := strconv.ParseUint(hexAddr[i*8:(i+1)*8], 16, 32)
		if err != nil {
			return netip.AddrPort{}, err
		}
		buf = binary.NativeEndian.AppendUint32(buf, uint32(v))
	}
	addr, _ := netip.AddrFromSlice(buf)
	return netip.AddrPortFrom(addr, uint16(port)), nil
}

// ReadSockets membaca keempat file /proc/net. File yang tidak ada (IPv6 mati) dilewati.
func ReadSockets(procRoot string) ([]Socket, error) {
	files := []struct {
		name  string
		proto Proto
		ipv6  bool
	}{{"tcp", TCP, false}, {"tcp6", TCP, true}, {"udp", UDP, false}, {"udp6", UDP, true}}

	var all []Socket
	found := false
	for _, f := range files {
		fh, err := os.Open(filepath.Join(procRoot, "net", f.name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		found = true
		socks, err := ParseProcNet(fh, f.proto, f.ipv6)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		all = append(all, socks...)
	}
	if !found {
		return nil, fmt.Errorf("%s/net tidak tersedia", procRoot)
	}
	return all, nil
}

// InodeOwners adalah hasil pemetaan inode socket → PID.
type InodeOwners struct {
	PIDs map[uint64][]int
	// Denied adalah jumlah proses yang /proc/PID/fd-nya tidak bisa dibaca (milik user lain).
	Denied int
}

// MapInodes menelusuri /proc/*/fd/* dan mencari symlink berbentuk socket:[inode].
func MapInodes(procRoot string) (InodeOwners, error) {
	res := InodeOwners{PIDs: map[uint64][]int{}}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		fdDir := filepath.Join(procRoot, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				res.Denied++
			}
			continue // proses bisa sudah selesai di tengah pembacaan
		}
		seen := map[uint64]bool{}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), 10, 64)
			if err != nil || seen[inode] {
				continue
			}
			seen[inode] = true
			res.PIDs[inode] = append(res.PIDs[inode], pid)
		}
	}
	for inode := range res.PIDs {
		sort.Ints(res.PIDs[inode])
	}
	return res, nil
}

// Listener adalah port yang sedang menunggu koneksi beserta pemiliknya.
type Listener struct {
	Socket
	PIDs    []int  // kosong bila pemilik tidak terlihat (butuh root)
	Process string // nama proses dari fallback ss bila /proc tidak memberi PID
}

// Scope menjelaskan dari mana port bisa diakses.
type Scope int

const (
	ScopeAll      Scope = iota // 0.0.0.0 atau :: — dari mana saja (bila firewall mengizinkan)
	ScopeLoopback              // 127.0.0.0/8 atau ::1 — hanya dari server ini
	ScopeSpecific              // IP tertentu
)

// Scope alamat lokal listener.
func (l Listener) Scope() Scope {
	a := l.Local.Addr()
	switch {
	case a.IsUnspecified():
		return ScopeAll
	case a.IsLoopback():
		return ScopeLoopback
	}
	return ScopeSpecific
}

// Snapshot adalah hasil satu kali pembacaan daftar port.
type Snapshot struct {
	Listeners []Listener
	Denied    int // proses yang fd-nya tidak terbaca → sebagian pemilik mungkin tidak terlihat
}

// ReadListeners membaca semua port yang listening dari /proc dan memetakan pemiliknya.
func ReadListeners(procRoot string) (Snapshot, error) {
	socks, err := ReadSockets(procRoot)
	if err != nil {
		return Snapshot{}, err
	}
	owners, err := MapInodes(procRoot)
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Denied: owners.Denied}
	seen := map[string]bool{}
	for _, s := range socks {
		if !s.Listening() {
			continue
		}
		key := fmt.Sprintf("%s|%s|%d", s.Proto, s.Local, s.Inode)
		if seen[key] {
			continue
		}
		seen[key] = true
		snap.Listeners = append(snap.Listeners, Listener{Socket: s, PIDs: owners.PIDs[s.Inode]})
	}
	SortListeners(snap.Listeners)
	return snap, nil
}

// SortListeners mengurutkan berdasarkan port, lalu protokol, lalu alamat.
func SortListeners(ls []Listener) {
	sort.SliceStable(ls, func(i, j int) bool {
		a, b := ls[i], ls[j]
		if a.Local.Port() != b.Local.Port() {
			return a.Local.Port() < b.Local.Port()
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		if a.IPv6 != b.IPv6 {
			return !a.IPv6
		}
		return a.Local.Addr().Less(b.Local.Addr())
	})
}
