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
