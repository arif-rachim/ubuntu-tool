package ports

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func listener(proto sysports.Proto, addr string, pids ...int) sysports.Listener {
	ap := netip.MustParseAddrPort(addr)
	return sysports.Listener{
		Socket: sysports.Socket{Proto: proto, IPv6: ap.Addr().Is6(), Local: ap, State: sysports.StateListen},
		PIDs:   pids,
	}
}

func proc(pid, ppid, uid int, name, unit string) procs.Process {
	return procs.Process{PID: pid, PPID: ppid, UID: uid, Name: name, User: "u" + name, Cgroup: procs.ParseCgroup("0::/system.slice/" + unit)}
}

func optionByValue(opts []ask.Option, v string) (ask.Option, bool) {
	for _, o := range opts {
		if o.Value == v {
			return o, true
		}
	}
	return ask.Option{}, false
}

func TestMainPIDMemilihInduk(t *testing.T) {
	parents := map[int]int{2001: 1, 2002: 2001, 2003: 2001}
	if got := MainPID([]int{2002, 2003, 2001}, func(p int) int { return parents[p] }); got != 2001 {
		t.Errorf("MainPID = %d, ingin 2001 (master nginx)", got)
	}
	if MainPID(nil, nil) != 0 {
		t.Error("tanpa PID harus 0")
	}
}

func TestOptionsUnitSystemdDisarankan(t *testing.T) {
	p := proc(2001, 1, 33, "nginx", "nginx.service")
	tg := Target{Listener: listener(sysports.TCP, "0.0.0.0:80", 2001, 2002), Proc: &p, Shared: 2}
	opts := Options(tg, Context{UID: 1000})
	if opts[0].Value != ActStopUnit || !opts[0].Recommended {
		t.Fatalf("stop unit harus pilihan pertama & disarankan: %+v", opts[0])
	}
	term, _ := optionByValue(opts, ActTerm)
	if !strings.Contains(term.Description, "systemd") || !strings.Contains(term.Description, "2 proses") {
		t.Errorf("penjelasan SIGTERM harus menyebut systemd & proses berbagi: %s", term.Description)
	}
	if kill, _ := optionByValue(opts, ActKill); kill.Risk != risk.Dangerous {
		t.Error("SIGKILL harus berbahaya")
	}

	plan := Plan(ActStopUnit, tg, Context{UID: 1000})
	c := plan.Steps[0]
	if c.Preview(false) != "sudo systemctl stop nginx.service" || c.Risk != risk.Caution {
		t.Errorf("plan stop unit: %s risk %v", c.Preview(false), c.Risk)
	}
}

func TestPlanKillTERM(t *testing.T) {
	p := proc(4321, 100, 1000, "python3", "")
	p.Cgroup = procs.ParseCgroup("0::/user.slice/user-1000.slice/session-2.scope")
	tg := Target{Listener: listener(sysports.TCP, "0.0.0.0:8080", 4321), Proc: &p, Shared: 1}

	opts := Options(tg, Context{UID: 1000})
	if _, ok := optionByValue(opts, ActStopUnit); ok {
		t.Error("scope sesi bukan service; tidak boleh ada opsi stop unit")
	}
	if opts[0].Value != ActTerm || !opts[0].Recommended {
		t.Errorf("tanpa unit, SIGTERM harus disarankan: %+v", opts[0])
	}

	c := Plan(ActTerm, tg, Context{UID: 1000}).Steps[0]
	if got := c.Preview(false); got != "kill -TERM 4321" {
		t.Errorf("preview %q, ingin kill -TERM 4321 (proses milik sendiri, tanpa sudo)", got)
	}
	if !strings.Contains(c.Effect, "port 8080/tcp menjadi kosong") {
		t.Errorf("efek: %s", c.Effect)
	}

	other := Plan(ActKill, tg, Context{UID: 1001}).Steps[0]
	if other.Preview(false) != "sudo kill -KILL 4321" || other.Risk != risk.Dangerous {
		t.Errorf("proses user lain butuh sudo & KILL berbahaya: %s %v", other.Preview(false), other.Risk)
	}
}

func TestSSHDianggapBerbahaya(t *testing.T) {
	p := proc(900, 1, 0, "sshd", "ssh.service")
	tg := Target{Listener: listener(sysports.TCP, "[::]:22", 900), Proc: &p, Shared: 1}
	opts := Options(tg, Context{UID: 1000})
	for _, v := range []string{ActStopUnit, ActTerm} {
		if o, _ := optionByValue(opts, v); o.Risk != risk.Dangerous {
			t.Errorf("%s pada sshd harus berbahaya", v)
		}
	}
	if c := Plan(ActTerm, tg, Context{UID: 1000}).Steps[0]; !strings.Contains(c.Effect, "SSH") {
		t.Errorf("efek harus memperingatkan SSH: %s", c.Effect)
	}

	// Port sesi SSH saat ini (mis. sshd di port 2222) juga dianggap berbahaya walau namanya lain.
	q := proc(950, 1, 0, "dropbear", "dropbear.service")
	tg2 := Target{Listener: listener(sysports.TCP, "0.0.0.0:2222", 950), Proc: &q, Shared: 1}
	if o, _ := optionByValue(Options(tg2, Context{UID: 1000, SSHPort: 2222}), ActTerm); o.Risk != risk.Dangerous {
		t.Error("port sesi SSH aktif harus berbahaya")
	}
}

func TestPID1DanDiriSendiriDiblok(t *testing.T) {
	p := proc(1, 0, 0, "systemd", "init.scope")
	tg := Target{Listener: listener(sysports.TCP, "0.0.0.0:111", 1), Proc: &p, Shared: 1}
	for _, o := range Options(tg, Context{UID: 0}) {
		if o.Disabled == "" {
			t.Errorf("semua aksi pada PID 1 harus disabled: %+v", o)
		}
	}
	self := proc(777, 1, 1000, "ubt", "")
	tg.Proc = &self
	for _, o := range Options(tg, Context{UID: 1000, SelfPID: 777}) {
		if o.Disabled == "" {
			t.Errorf("aksi pada ubt sendiri harus disabled: %+v", o)
		}
	}
}

func TestUnitUserDanContainer(t *testing.T) {
	p := proc(3000, 2000, 1000, "node", "")
	p.Cgroup = procs.ParseCgroup("0::/user.slice/user-1000.slice/user@1000.service/app.slice/myapp.service")
	tg := Target{Listener: listener(sysports.TCP, "127.0.0.1:3000", 3000), Proc: &p, Shared: 1}
	if got := Plan(ActStopUnit, tg, Context{UID: 1000}).Steps[0].Preview(false); got != "systemctl --user stop myapp.service" {
		t.Errorf("unit user tanpa sudo: %s", got)
	}

	c := proc(5000, 4990, 0, "postgres", "")
	c.Cgroup = procs.ParseCgroup("0::/system.slice/docker-0123456789abcdef0123.scope")
	tg = Target{Listener: listener(sysports.TCP, "0.0.0.0:5432", 5000), Proc: &c, Shared: 1}
	opts := Options(tg, Context{UID: 1000})
	if opts[0].Value != ActDockerStop || !opts[0].Recommended {
		t.Errorf("proses container: docker stop harus disarankan: %+v", opts[0])
	}
	if _, ok := optionByValue(opts, ActStopUnit); ok {
		t.Error("scope docker bukan service")
	}
	if got := Plan(ActDockerStop, tg, Context{}).Steps[0].Preview(false); got != "docker stop 0123456789ab" {
		t.Errorf("docker stop: %s", got)
	}
}

func TestPemilikTidakTerlihat(t *testing.T) {
	tg := Target{Listener: listener(sysports.UDP, "0.0.0.0:5353")}
	opts := Options(tg, Context{UID: 1000})
	if len(opts) != 1 || opts[0].Value != ActFindOwner {
		t.Fatalf("tanpa proses hanya boleh ada opsi cari pemilik: %+v", opts)
	}
	c := Plan(ActFindOwner, tg, Context{UID: 1000}).Steps[0]
	if c.Preview(false) != "sudo ss -ulpn 'sport = :5353'" {
		t.Errorf("cari pemilik: %s", c.Preview(false))
	}
}

func fakeData() Data {
	nginx := proc(2001, 1, 33, "nginx", "nginx.service")
	worker := proc(2002, 2001, 33, "nginx", "nginx.service")
	py := proc(4321, 100, 1000, "python3", "")
	l1 := listener(sysports.TCP, "0.0.0.0:80", 2001, 2002)
	l1.UID = 0
	return Data{
		Snap: sysports.Snapshot{
			Listeners: []sysports.Listener{
				listener(sysports.UDP, "127.0.0.53:53"),
				l1,
				listener(sysports.TCP, "127.0.0.1:8080", 4321),
			},
			Denied: 3,
		},
		Procs:  map[int]procs.Process{2001: nginx, 2002: worker, 4321: py},
		Source: "proc",
	}
}

func newTestList(t *testing.T) *ListModel {
	env := shared.Env{UID: 1000, Now: time.Now, ProcRoot: t.TempDir()}
	m := newList(env, func(shared.Env) (Data, error) { return fakeData(), nil })
	msg := m.Init()()
	m.Update(msg)
	return m
}

var pressKey = testutil.Key

func TestListTampilanDanFilter(t *testing.T) {
	m := newTestList(t)
	view := ansi.Strip(m.View(110, 25))
	for _, s := range []string{"3 port sedang menunggu koneksi", "sudo ss -tulpn", "2001+1", "nginx.service", "python3", "Sebagian pemilik port tidak terlihat"} {
		if !strings.Contains(view, s) {
			t.Errorf("tampilan tidak memuat %q:\n%s", s, view)
		}
	}

	m.Update(pressKey("/"))
	if !m.Typing() {
		t.Fatal("/ harus masuk mode filter")
	}
	for _, r := range "8080" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(m.rows) != 1 || m.rows[0].Local.Port() != 8080 {
		t.Fatalf("filter 8080: %d baris", len(m.rows))
	}
	m.Update(pressKey("enter"))
	if m.Typing() {
		t.Fatal("enter harus menutup mode ketik")
	}
	_, cmd := m.Update(pressKey("enter"))
	if cmd == nil {
		t.Fatal("enter pada baris harus membuka detail")
	}
	m.Update(pressKey("esc"))
	if len(m.rows) != 3 {
		t.Fatal("esc harus menghapus filter")
	}

	m.Update(pressKey("t"))
	if len(m.rows) != 2 {
		t.Errorf("t pertama: hanya TCP, dapat %d", len(m.rows))
	}
	m.Update(pressKey("t"))
	if len(m.rows) != 1 || m.rows[0].Proto != sysports.UDP {
		t.Errorf("t kedua: hanya UDP")
	}
}

func TestDetailTampilan(t *testing.T) {
	d := fakeData()
	env := shared.Env{UID: 1000, Now: time.Now, ProcRoot: t.TempDir()}
	m := newDetail(env, d, d.Snap.Listeners[1])
	view := ansi.Strip(m.View(100, 60))
	for _, s := range []string{"Port 80/tcp", "PID", "2001", "Berbagi port", "2 proses", "nginx.service (unit sistem)", "systemctl status nginx.service", "bisa diakses dari luar"} {
		if !strings.Contains(view+m.Title(), s) {
			t.Errorf("detail tidak memuat %q:\n%s", s, view)
		}
	}
}

// conn membuat koneksi TCP yang sedang terbuka ke sebuah port lokal.
func conn(local, remote string) sysports.Connection {
	lp, rp := netip.MustParseAddrPort(local), netip.MustParseAddrPort(remote)
	return sysports.Connection{Socket: sysports.Socket{
		Proto: sysports.TCP, IPv6: lp.Addr().Is6(), Local: lp, Remote: rp, State: sysports.StateEstablished,
	}}
}

// dockerData adalah kondisi khas server dengan Docker: satu proses di dalam container, dan satu
// port host yang dipublikasikan container lewat docker-proxy.
func dockerData() Data {
	d := fakeData()
	proxy := proc(5000, 1, 0, "docker-proxy", "docker.service")
	inside := procs.Process{PID: 6000, PPID: 1, Name: "postgres", User: "postgres",
		Cgroup: procs.ParseCgroup("0::/system.slice/docker-1111aaaa2222bbbb3333cccc4444dddd5555eeee6666ffff7777aaaa8888bbbb.scope")}
	d.Snap.Listeners = append(d.Snap.Listeners,
		listener(sysports.TCP, "0.0.0.0:8081", 5000),
		listener(sysports.TCP, "0.0.0.0:5432", 6000))
	d.Procs[5000] = proxy
	d.Procs[6000] = inside
	d.Snap.Established = []sysports.Connection{
		conn("0.0.0.0:80", "10.0.0.5:51000"),
		conn("0.0.0.0:80", "10.0.0.5:51001"),
		conn("0.0.0.0:80", "127.0.0.1:51002"),
	}
	d.Containers = sysdocker.ParseContainers(
		`{"ID":"1111aaaa2222bbbb3333cccc4444dddd5555eeee6666ffff7777aaaa8888bbbb","Names":"db","Image":"postgres:18","State":"running","Ports":""}
{"ID":"9999cccc","Names":"web-uji","Image":"nginx","State":"running","Ports":"0.0.0.0:8081->80/tcp"}`)
	d.Published = sysdocker.PublishedPorts(d.Containers)
	return d
}

func TestDaftarMenampilkanKoneksiDanContainer(t *testing.T) {
	env := shared.Env{UID: 1000, Now: time.Now, ProcRoot: t.TempDir()}
	m := newList(env, func(shared.Env) (Data, error) { return dockerData(), nil })
	m.Update(m.Init()())
	view := ansi.Strip(m.View(140, 30))

	// Port host yang dipublikasikan container disebut nama containernya, bukan cuma docker-proxy.
	if !strings.Contains(view, "docker web-uji") {
		t.Errorf("nama container untuk port publish tidak muncul:\n%s", view)
	}
	// Proses yang berjalan DI DALAM container juga dinamai.
	if !strings.Contains(view, "docker db") {
		t.Errorf("nama container untuk proses di dalamnya tidak muncul:\n%s", view)
	}
	// Jumlah koneksi ikut terlihat di daftar.
	if !strings.Contains(view, "Konek") {
		t.Errorf("kolom koneksi tidak ada:\n%s", view)
	}
	var row80 []string
	for i, l := range m.rows {
		if l.Local.Port() == 80 {
			row80 = m.table.Rows[i]
		}
	}
	if len(row80) == 0 || row80[3] != "3" {
		t.Errorf("baris port 80 = %v, ingin 3 koneksi", row80)
	}
}

func TestDetailKoneksiAktif(t *testing.T) {
	d := dockerData()
	env := shared.Env{UID: 1000, Now: time.Now, ProcRoot: t.TempDir()}
	var l80 sysports.Listener
	for _, l := range d.Snap.Listeners {
		if l.Local.Port() == 80 {
			l80 = l
		}
	}
	view := ansi.Strip(newDetail(env, d, l80).View(120, 60))
	for _, want := range []string{"Koneksi aktif", "3 koneksi terbuka", "1 dari server ini sendiri", "10.0.0.5", "2 koneksi", "127.0.0.1 (server ini)"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail tidak memuat %q:\n%s", want, view)
		}
	}

	// Port tanpa koneksi tetap menjelaskan keadaannya, bukan dibiarkan kosong.
	var l5432 sysports.Listener
	for _, l := range d.Snap.Listeners {
		if l.Local.Port() == 5432 {
			l5432 = l
		}
	}
	detail := newDetail(env, d, l5432)
	view = ansi.Strip(detail.View(120, 60))
	if !strings.Contains(view, "Tidak ada koneksi yang sedang terbuka") {
		t.Errorf("port tanpa koneksi:\n%s", view)
	}
	// Proses di dalam container dinamai di detail juga.
	if !strings.Contains(view, "db") {
		t.Errorf("nama container di detail:\n%s", view)
	}
}

// Saat ubt sudah berjalan sebagai root, "milik user lain" adalah alasan yang salah.
func TestDetailPemilikTidakTerlihat(t *testing.T) {
	d := fakeData()
	yatim := listener(sysports.TCP, "0.0.0.0:2024")
	d.Snap.Listeners = append(d.Snap.Listeners, yatim)

	biasa := newDetail(shared.Env{UID: 1000, Now: time.Now, ProcRoot: "/proc"}, d, yatim)
	view := ansi.Strip(biasa.View(120, 40))
	if !strings.Contains(view, "milik user lain") || !strings.Contains(view, "sudo") {
		t.Errorf("sebagai user biasa:\n%s", view)
	}

	root := newDetail(shared.Env{UID: 0, IsRoot: true, Now: time.Now, ProcRoot: "/proc"}, d, yatim)
	view = ansi.Strip(root.View(120, 40))
	if strings.Contains(view, "milik user lain") {
		t.Errorf("sebagai root tidak boleh menyalahkan izin user:\n%s", view)
	}
	for _, want := range []string{"namespace lain", "sudah berjalan sebagai root"} {
		if !strings.Contains(view, want) {
			t.Errorf("penjelasan root tidak memuat %q:\n%s", want, view)
		}
	}

	// Bila ternyata port itu milik container, itulah yang disebut — bukan soal izin sama sekali.
	dd := dockerData()
	proxyPort := listener(sysports.TCP, "0.0.0.0:8081") // tanpa PID: docker-proxy tidak terlihat
	dd.Snap.Listeners = append(dd.Snap.Listeners, proxyPort)
	view = ansi.Strip(newDetail(shared.Env{UID: 0, IsRoot: true, Now: time.Now, ProcRoot: "/proc"}, dd, proxyPort).View(120, 40))
	if !strings.Contains(view, "web-uji") {
		t.Errorf("port container tanpa pemilik terlihat:\n%s", view)
	}
}
