package ports

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// hexWord mengubah 4 byte alamat menjadi word hex seperti yang dicetak kernel di mesin ini.
func hexWord(b []byte) string { return fmt.Sprintf("%08X", binary.NativeEndian.Uint32(b)) }

func TestParseAddrPort(t *testing.T) {
	v4 := netip.MustParseAddr("192.168.1.215").As4()
	v6 := netip.MustParseAddr("fe80::1234:abcd:5678:9").As16()
	v6hex := hexWord(v6[0:4]) + hexWord(v6[4:8]) + hexWord(v6[8:12]) + hexWord(v6[12:16])

	tests := []struct {
		in   string
		ipv6 bool
		want string
	}{
		{hexWord(v4[:]) + ":1F90", false, "192.168.1.215:8080"},
		{hexWord([]byte{127, 0, 0, 1}) + ":0016", false, "127.0.0.1:22"},
		{"00000000:0035", false, "0.0.0.0:53"},
		{v6hex + ":01BB", true, "[fe80::1234:abcd:5678:9]:443"},
		{"00000000000000000000000000000000:0016", true, "[::]:22"},
	}
	for _, tt := range tests {
		got, err := parseAddrPort(tt.in, tt.ipv6)
		if err != nil || got.String() != tt.want {
			t.Errorf("parseAddrPort(%s) = %v, %v; ingin %s", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"0100007F", "ZZZZZZZZ:0016", "0100007F:ZZZZ", "01007F:0016"} {
		if _, err := parseAddrPort(bad, false); err == nil {
			t.Errorf("parseAddrPort(%q) harus error", bad)
		}
	}
}

func TestParseProcNetFixture(t *testing.T) {
	if binary.NativeEndian.Uint16([]byte{1, 0}) != 1 {
		t.Skip("fixture direkam di mesin little-endian")
	}
	f, _ := os.Open("testdata/proc-net-tcp.txt")
	defer f.Close()
	socks, err := ParseProcNet(f, TCP, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 4 {
		t.Fatalf("%d socket, ingin 4", len(socks))
	}
	want := []struct {
		local  string
		listen bool
		uid    int
		inode  uint64
	}{
		{"127.0.0.1:631", true, 0, 17421},
		{"127.0.0.53:53", true, 991, 8988},
		{"0.0.0.0:8080", true, 1000, 50001},
		{"192.168.1.215:22", false, 0, 50002},
	}
	for i, w := range want {
		s := socks[i]
		if s.Local.String() != w.local || s.Listening() != w.listen || s.UID != w.uid || s.Inode != w.inode {
			t.Errorf("socket %d: %+v, ingin %+v", i, s, w)
		}
	}
	if socks[3].Remote.String() != "192.168.1.10:50000" || socks[3].State != StateEstablished {
		t.Errorf("koneksi established: %+v", socks[3])
	}

	f6, _ := os.Open("testdata/proc-net-tcp6.txt")
	defer f6.Close()
	socks6, err := ParseProcNet(f6, TCP, true)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, s := range socks6 {
		got = append(got, s.Local.String())
	}
	if !reflect.DeepEqual(got, []string{"[::1]:631", "[::]:22", "[fe80::212:abff:bc56:34fe]:8080"}) {
		t.Errorf("tcp6: %v", got)
	}
}

func TestUDPListening(t *testing.T) {
	f, _ := os.Open("testdata/proc-net-udp.txt")
	defer f.Close()
	socks, err := ParseProcNet(f, UDP, false)
	if err != nil {
		t.Fatal(err)
	}
	if !socks[0].Listening() || socks[1].Listening() {
		t.Errorf("UDP ter-bind tanpa tujuan harus listening, UDP connected tidak: %v %v", socks[0].Listening(), socks[1].Listening())
	}
}

// fakeProc membangun pohon /proc tiruan.
func fakeProc(t *testing.T, withV6 bool) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "net"), 0o755))
	copyFile := func(src, dst string) {
		data, err := os.ReadFile(src)
		must(err)
		must(os.WriteFile(filepath.Join(root, "net", dst), data, 0o644))
	}
	copyFile("testdata/proc-net-tcp.txt", "tcp")
	copyFile("testdata/proc-net-udp.txt", "udp")
	if withV6 {
		copyFile("testdata/proc-net-tcp6.txt", "tcp6")
	}
	addFD := func(pid int, fd int, target string) {
		dir := filepath.Join(root, fmt.Sprint(pid), "fd")
		must(os.MkdirAll(dir, 0o755))
		must(os.Symlink(target, filepath.Join(dir, fmt.Sprint(fd))))
	}
	addFD(1043, 7, "socket:[17421]")
	addFD(1043, 8, "socket:[17420]")
	addFD(1043, 3, "/var/log/cups/access_log")
	addFD(2001, 6, "socket:[50001]")
	addFD(2002, 6, "socket:[50001]")
	addFD(2002, 9, "socket:[50001]") // fd ganda ke inode yang sama tidak boleh menggandakan PID
	must(os.MkdirAll(filepath.Join(root, "self"), 0o755))
	return root
}

func TestReadListeners(t *testing.T) {
	if binary.NativeEndian.Uint16([]byte{1, 0}) != 1 {
		t.Skip("fixture direkam di mesin little-endian")
	}
	root := fakeProc(t, false) // tanpa tcp6: file hilang harus dilewati
	snap, err := ReadListeners(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range snap.Listeners {
		got = append(got, fmt.Sprintf("%s %s %v", l.Proto, l.Local, l.PIDs))
	}
	want := []string{
		"udp 0.0.0.0:5353 []",
		"tcp 127.0.0.53:53 []",
		"tcp 127.0.0.1:631 [1043]",
		"tcp 0.0.0.0:8080 [2001 2002]",
	}
	// Urut berdasarkan port: 53, 631, 5353, 8080.
	want = []string{want[1], want[2], want[0], want[3]}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("listeners:\n%s\ningin:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if snap.Listeners[0].Scope() != ScopeLoopback || snap.Listeners[3].Scope() != ScopeAll {
		t.Error("scope alamat salah")
	}
}

func TestMapInodesIzinDitolak(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bisa membaca semua fd")
	}
	root := fakeProc(t, true)
	locked := filepath.Join(root, "4242", "fd")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	owners, err := MapInodes(root)
	if err != nil {
		t.Fatal(err)
	}
	if owners.Denied != 1 {
		t.Errorf("Denied %d, ingin 1", owners.Denied)
	}
	if !reflect.DeepEqual(owners.PIDs[17420], []int{1043}) {
		t.Errorf("tcp6 inode: %v", owners.PIDs[17420])
	}
}

func TestReadSocketsTanpaProcNet(t *testing.T) {
	if _, err := ReadSockets(t.TempDir()); err == nil {
		t.Fatal("tanpa /proc/net harus error supaya fallback ss dipakai")
	}
}

func TestParseSS(t *testing.T) {
	data, _ := os.ReadFile("testdata/ss-tulpn.txt")
	ls := ParseSS(string(data))
	var got []string
	for _, l := range ls {
		got = append(got, fmt.Sprintf("%s %s %v %s", l.Proto, l.Local, l.PIDs, l.Process))
	}
	want := []string{
		"tcp [::]:22 [900] sshd",
		"udp 127.0.0.53:53 [715] systemd-resolve",
		"tcp 0.0.0.0:80 [2001 2002] nginx",
		"udp [fe80::adee:63ac:5b28:5b33]:546 [] ",
		"tcp 127.0.0.1:631 [1043] cupsd",
		"udp 0.0.0.0:5353 [880] avahi-daemon",
		"tcp 0.0.0.0:9090 [] ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseSS:\n%s\ningin:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range ls {
		if l.Proto == TCP && !l.Listening() {
			t.Errorf("TCP dari ss harus LISTEN: %+v", l)
		}
	}
}

func TestReadListenersSistemNyata(t *testing.T) {
	if _, err := os.Stat("/proc/net/tcp"); err != nil {
		t.Skip("tidak ada /proc/net")
	}
	if _, err := ReadListeners("/proc"); err != nil {
		t.Fatalf("membaca /proc nyata gagal: %v", err)
	}
}
