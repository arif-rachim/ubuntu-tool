package runflow

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// fakeSys mensimulasikan sistem: stream per command dan terminal interaktif.
type fakeSys struct {
	streams     map[string]*run.FakeStream // kunci: argv yang dijalankan, digabung spasi
	started     []string
	interactive []string
	interErr    map[string]error // kunci: argv interaktif (setelah "ubt" pada pembungkus sh)
}

func (f *fakeSys) deps(isRoot bool, sudo run.SudoState, hist *run.History) Deps {
	return Deps{
		Env: run.Env{IsRoot: isRoot, SudoCheck: func(context.Context) run.SudoState { return sudo }},
		Start: func(_ context.Context, argv []string, stdin string) (run.Stream, error) {
			k := strings.Join(argv, " ")
			f.started = append(f.started, k)
			s, ok := f.streams[k]
			if !ok {
				return nil, &run.NotFoundError{Program: argv[0]}
			}
			return s, nil
		},
		Interactive: func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
			args := c.Args
			// Buang pembungkus `sh -c <script> ubt` supaya test melihat argv aslinya.
			if len(args) > 3 && args[0] == "sh" && args[3] == "ubt" {
				args = args[4:]
			}
			k := strings.Join(args, " ")
			f.interactive = append(f.interactive, k)
			err := f.interErr[k]
			return func() tea.Msg { return fn(err) }
		},
		History: hist,
	}
}

// pump menjalankan cmd dan meneruskan pesannya ke screen sampai tidak ada lagi. tickMsg diabaikan.
func pump(t *testing.T, s nav.Screen, cmd tea.Cmd) (nav.Screen, []tea.Msg) {
	t.Helper()
	var outside []tea.Msg
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 1000 {
			t.Fatal("pump tidak berhenti")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		ch := make(chan tea.Msg, 1)
		go func() { ch <- c() }()
		var msg tea.Msg
		select {
		case msg = <-ch:
		case <-time.After(time.Second):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case tickMsg:
			continue
		case nav.PopMsg, nav.ReplaceMsg, nav.PushMsg:
			outside = append(outside, msg)
			continue
		}
		var next tea.Cmd
		s, next = s.Update(msg)
		queue = append(queue, next)
	}
	return s, outside
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func cmdOf(title string, argv ...string) run.Command {
	return run.Command{Title: title, Argv: argv}
}

func runExec(t *testing.T, p run.Plan, d Deps, sudoCached bool) *ExecModel {
	t.Helper()
	m := newExec(p, d, sudoCached)
	pump(t, m, m.startCurrent())
	return m
}

func TestExecSemuaBerhasilDanRiwayat(t *testing.T) {
	f := &fakeSys{streams: map[string]*run.FakeStream{
		"mkdir -p /tmp/x": {},
		"echo halo":       {Lines: []string{"halo"}},
	}}
	hist := &run.History{Path: filepath.Join(t.TempDir(), "h.log")}
	p := run.Plan{Title: "demo", Steps: []run.Command{cmdOf("buat", "mkdir", "-p", "/tmp/x"), cmdOf("sapa", "echo", "halo")}}
	m := runExec(t, p, f.deps(false, run.SudoNotNeeded, hist), false)

	o := m.Outcome()
	if !m.done || !o.OK() {
		t.Fatalf("harus selesai & OK: %+v", o.Results)
	}
	if got := o.Results[1].Output; len(got) != 1 || got[0] != "halo" {
		t.Errorf("output langkah 2: %q", got)
	}
	entries, _ := hist.Read(0)
	if len(entries) != 2 || entries[1].Title != "sapa" {
		t.Errorf("riwayat harus mencatat 2 langkah: %+v", entries)
	}
	if m.Busy() {
		t.Error("tidak boleh Busy setelah selesai")
	}
	_, out := pump(t, m, m.handleKey("enter"))
	if len(out) != 1 {
		t.Fatal("enter setelah selesai harus pop")
	}
	if res, ok := out[0].(nav.PopMsg).Result.(run.Outcome); !ok || !res.OK() {
		t.Fatalf("hasil pop: %#v", out[0])
	}
	if !strings.Contains(ansi.Strip(m.View(100, 30)), "Semua langkah berhasil") {
		t.Error("ringkasan sukses tidak tampil")
	}
}

func TestExecBerhentiSaatGagal(t *testing.T) {
	f := &fakeSys{streams: map[string]*run.FakeStream{
		"true":        {},
		"ls /tidak":   {Lines: []string{"ls: cannot access '/tidak'"}, ExitCode: 2, Err: errors.New("exit status 2")},
		"echo jangan": {},
	}}
	p := run.Plan{Title: "gagal", Steps: []run.Command{cmdOf("a", "true"), cmdOf("b", "ls", "/tidak"), cmdOf("c", "echo", "jangan")}}
	m := runExec(t, p, f.deps(false, run.SudoNotNeeded, nil), false)

	o := m.Outcome()
	want := []run.StepStatus{run.StatusOK, run.StatusFailed, run.StatusSkipped}
	for i, r := range o.Results {
		if r.Status != want[i] {
			t.Errorf("langkah %d status %v, ingin %v", i+1, r.Status, want[i])
		}
	}
	for _, s := range f.started {
		if s == "echo jangan" {
			t.Error("langkah setelah gagal tidak boleh dijalankan")
		}
	}
	view := ansi.Strip(m.View(120, 30))
	for _, s := range []string{"Gagal di langkah 2 (kode keluar 2)", "Langkah 1 sudah terlanjur dijalankan", "dilewati", "cannot access"} {
		if !strings.Contains(view, s) {
			t.Errorf("tampilan tidak memuat %q:\n%s", s, view)
		}
	}
}

func TestExecCheckGagalMencegahLangkahTerakhir(t *testing.T) {
	chk := cmdOf("cek", "sshd", "-t")
	f := &fakeSys{streams: map[string]*run.FakeStream{
		"tee a":    {},
		"sshd -t":  {ExitCode: 255, Err: errors.New("exit status 255")},
		"reload x": {},
	}}
	p := run.Plan{Steps: []run.Command{cmdOf("tulis", "tee", "a"), cmdOf("reload", "reload", "x")}, Check: &chk}
	m := runExec(t, p, f.deps(false, run.SudoNotNeeded, nil), false)
	if got := strings.Join(f.started, "|"); got != "tee a|sshd -t" {
		t.Fatalf("urutan yang dijalankan: %s", got)
	}
	if m.Outcome().Results[2].Status != run.StatusSkipped {
		t.Error("langkah terakhir harus dilewati bila Check gagal")
	}
}

func TestExecSudoMintaPasswordSekali(t *testing.T) {
	root := run.Command{Title: "r", Argv: []string{"ufw", "status"}, NeedsRoot: true}
	f := &fakeSys{streams: map[string]*run.FakeStream{
		"sudo -n ufw status": {Lines: []string{"Status: active"}},
	}}
	p := run.Plan{Steps: []run.Command{root, root}}
	m := runExec(t, p, f.deps(false, run.SudoNeedsPasswd, nil), false)
	if !m.Outcome().OK() {
		t.Fatalf("harus OK: %+v", m.Outcome().Results)
	}
	if got := strings.Join(f.interactive, "|"); got != "sudo -v" {
		t.Errorf("sudo -v harus diminta tepat sekali, dapat %q", got)
	}
}

func TestExecSudoDitolak(t *testing.T) {
	root := run.Command{Argv: []string{"ufw", "status"}, NeedsRoot: true}
	f := &fakeSys{interErr: map[string]error{"sudo -v": errors.New("exit status 1")}}
	m := runExec(t, run.Single(root), f.deps(false, run.SudoNeedsPasswd, nil), false)
	r := m.Outcome().Results[0]
	if r.Status != run.StatusFailed || !strings.Contains(r.Err.Error(), "sudo") || len(f.started) != 0 {
		t.Fatalf("hasil %+v, started %v", r, f.started)
	}
}

func TestExecSudoRootTanpaSudo(t *testing.T) {
	root := run.Command{Argv: []string{"ufw", "status"}, NeedsRoot: true}
	f := &fakeSys{streams: map[string]*run.FakeStream{"ufw status": {}}}
	m := runExec(t, run.Single(root), f.deps(true, run.SudoNotNeeded, nil), false)
	if !m.Outcome().OK() || len(f.interactive) != 0 {
		t.Fatalf("root harus langsung jalan tanpa sudo: started %v interactive %v", f.started, f.interactive)
	}
}

func TestExecSudoKedaluwarsaDimintaUlang(t *testing.T) {
	root := run.Command{Argv: []string{"ufw", "status"}, NeedsRoot: true}
	first := &run.FakeStream{Lines: []string{"sudo: a password is required"}, ExitCode: 1, Err: errors.New("exit status 1")}
	f := &fakeSys{streams: map[string]*run.FakeStream{"sudo -n ufw status": first}}
	d := f.deps(false, run.SudoCached, nil)
	calls := 0
	d.Start = func(ctx context.Context, argv []string, stdin string) (run.Stream, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return &run.FakeStream{Lines: []string{"Status: active"}}, nil
	}
	m := runExec(t, run.Single(root), d, true)
	if !m.Outcome().OK() || calls != 2 || strings.Join(f.interactive, "|") != "sudo -v" {
		t.Fatalf("OK=%v calls=%d interactive=%v", m.Outcome().OK(), calls, f.interactive)
	}
}

func TestExecInteraktifLewatTerminal(t *testing.T) {
	c := run.Command{Argv: []string{"adduser", "budi"}, NeedsRoot: true, Interactive: true}
	f := &fakeSys{}
	m := runExec(t, run.Single(c), f.deps(false, run.SudoCached, nil), true)
	if got := strings.Join(f.interactive, "|"); got != "sudo adduser budi" {
		t.Fatalf("command interaktif: %q", got)
	}
	if !m.Outcome().OK() {
		t.Fatal("harus OK")
	}
}

func TestInteractiveCmdMenungguEnter(t *testing.T) {
	c := interactiveCmd([]string{"echo", "halo dunia"}, "")
	if c.Args[0] != "sh" || c.Args[len(c.Args)-2] != "echo" || c.Args[len(c.Args)-1] != "halo dunia" {
		t.Fatalf("argv asli harus diteruskan utuh sebagai argumen: %q", c.Args)
	}
	if !strings.Contains(c.Args[2], "read _ </dev/tty") {
		t.Error("pembungkus harus menunggu enter dari terminal")
	}
}

func TestExecDibatalkan(t *testing.T) {
	s := &run.FakeStream{Lines: []string{"mulai"}}
	f := &fakeSys{streams: map[string]*run.FakeStream{"sleep 30": s, "echo nanti": {}}}
	p := run.Plan{Steps: []run.Command{cmdOf("tidur", "sleep", "30"), cmdOf("nanti", "echo", "nanti")}}
	m := newExec(p, f.deps(false, run.SudoNotNeeded, nil), false)

	// Jalankan sampai stream dimulai, lalu tekan ctrl+c sebelum event berikutnya diproses.
	msg := m.startCurrent()()
	var cmd tea.Cmd
	_, cmd = m.Update(msg)
	if !m.Busy() {
		t.Fatal("harus Busy saat berjalan")
	}
	m.Update(keyMsg("ctrl+c"))
	pump(t, m, cmd)

	o := m.Outcome()
	if o.Results[0].Status != run.StatusCancelled || o.Results[1].Status != run.StatusSkipped {
		t.Fatalf("status: %v %v", o.Results[0].Status, o.Results[1].Status)
	}
}

func TestExecProgramTidakAda(t *testing.T) {
	f := &fakeSys{streams: map[string]*run.FakeStream{}}
	m := runExec(t, run.Single(cmdOf("x", "ufw", "status")), f.deps(false, run.SudoNotNeeded, nil), false)
	r := m.Outcome().Results[0]
	if r.Status != run.StatusFailed {
		t.Fatal("harus gagal")
	}
	if !strings.Contains(ansi.Strip(m.View(100, 30)), "tidak ditemukan") {
		t.Errorf("pesan program tidak ditemukan tidak tampil:\n%s", ansi.Strip(m.View(100, 30)))
	}
}

func TestConfirmBatal(t *testing.T) {
	c := Confirm(run.Single(cmdOf("x", "ls")), (&fakeSys{}).deps(false, run.SudoNotNeeded, nil))
	_, out := pump(t, c, c.handleKey("n"))
	if len(out) != 1 {
		t.Fatal("n harus pop")
	}
	o := out[0].(nav.PopMsg).Result.(run.Outcome)
	if o.Approved || o.OK() {
		t.Fatal("batal tidak boleh Approved")
	}
}

func TestConfirmBahayaButuhDuaKaliY(t *testing.T) {
	c := Confirm(run.Single(run.Command{Argv: []string{"rm", "-rf", "/srv/app"}, Risk: risk.Dangerous}), (&fakeSys{}).deps(false, run.SudoNotNeeded, nil))
	if cmd := c.handleKey("y"); cmd != nil || !c.armed {
		t.Fatal("y pertama pada aksi berbahaya hanya boleh mempersenjatai")
	}
	if !strings.Contains(ansi.Strip(c.View(100, 30)), "Tekan y sekali lagi") {
		t.Error("peringatan y kedua tidak tampil")
	}
	c.handleKey("x")
	if c.armed {
		t.Fatal("tombol lain harus membatalkan")
	}
	c.handleKey("y")
	_, out := pump(t, c, c.handleKey("y"))
	if len(out) != 1 {
		t.Fatal("y kedua harus menjalankan (replace ke layar eksekusi)")
	}
	if _, ok := out[0].(nav.ReplaceMsg).Screen.(*ExecModel); !ok {
		t.Fatalf("harus replace ke ExecModel: %#v", out[0])
	}
}

func TestConfirmAmanLangsungJalan(t *testing.T) {
	c := Confirm(run.Single(cmdOf("x", "ls")), (&fakeSys{}).deps(false, run.SudoNotNeeded, nil))
	if _, out := pump(t, c, c.handleKey("y")); len(out) != 1 {
		t.Fatal("y pada aksi aman harus langsung menjalankan")
	}
}

func TestConfirmSudoTidakAda(t *testing.T) {
	root := run.Command{Argv: []string{"ufw", "enable"}, NeedsRoot: true}
	c := Confirm(run.Single(root), (&fakeSys{}).deps(false, run.SudoMissing, nil))
	pump(t, c, c.Init())
	if cmd := c.handleKey("y"); cmd != nil {
		t.Fatal("tanpa sudo, aksi root tidak boleh dijalankan")
	}
	if !strings.Contains(ansi.Strip(c.View(100, 30)), "sudo tidak terinstall") {
		t.Error("pesan sudo tidak ada tidak tampil")
	}
}

func TestConfirmTampilanLengkap(t *testing.T) {
	chk := run.Command{Title: "Validasi config", Argv: []string{"nginx", "-t"}, NeedsRoot: true}
	p := run.Plan{
		Title: "Pasang reverse proxy",
		Steps: []run.Command{
			{
				Title: "Tulis config site", Argv: []string{"tee", "/etc/nginx/sites-available/app"}, NeedsRoot: true,
				Stdin: "server {\n    listen 80;\n}\n", StdinLabel: "(config nginx, 3 baris)", Risk: risk.Caution,
				Explain: []run.Line{{Token: "tee", Meaning: "tulis stdin ke file"}},
				Effect:  "File config baru dibuat.",
			},
			{Title: "Muat ulang nginx", Argv: []string{"systemctl", "reload", "nginx"}, NeedsRoot: true, Safer: "reload tidak memutus koneksi"},
		},
		Check: &chk,
	}
	c := Confirm(p, (&fakeSys{}).deps(false, run.SudoNeedsPasswd, nil))
	pump(t, c, c.Init())
	view := ansi.Strip(c.View(110, 60))
	for _, s := range []string{
		"BERISIKO", "Pasang reverse proxy", "Langkah-langkah yang akan dijalankan (3)",
		"$  sudo tee /etc/nginx/sites-available/app", "tee → tulis stdin ke file",
		"│ server {", "Validasi config (validasi", "sudo nginx -t", "Lebih aman: reload tidak memutus koneksi",
		"sudo akan meminta password", "[ y Jalankan ]",
	} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q", s)
		}
	}
	if t.Failed() {
		t.Log(view)
	}

	// Konten sensitif tidak boleh tampil maupun tersalin.
	secret := run.Command{Title: "key", Argv: []string{"tee", "k"}, Stdin: "RAHASIA", Sensitive: true}
	c = Confirm(run.Single(secret), (&fakeSys{}).deps(false, run.SudoNotNeeded, nil))
	if v := c.View(100, 30); strings.Contains(v, "RAHASIA") || strings.Contains(c.script(), "RAHASIA") {
		t.Error("isi sensitif bocor")
	}
}

func TestConfirmSalinDanGulir(t *testing.T) {
	var steps []run.Command
	for i := 0; i < 30; i++ {
		steps = append(steps, cmdOf("langkah", "echo", "halo dunia"))
	}
	c := Confirm(run.Plan{Title: "banyak", Steps: steps}, (&fakeSys{}).deps(false, run.SudoNotNeeded, nil))
	if cmd := c.handleKey("c"); cmd == nil || !strings.Contains(c.script(), "echo 'halo dunia'") {
		t.Fatal("c harus menyalin script")
	}
	before := ansi.Strip(c.View(80, 20))
	if !strings.Contains(before, "lagi di bawah") {
		t.Error("indikator gulir bawah tidak tampil")
	}
	c.handleKey("pgdown")
	after := ansi.Strip(c.View(80, 20))
	if before == after || !strings.Contains(after, "lagi di atas") {
		t.Error("pgdown tidak menggulir")
	}
	if !strings.Contains(after, "[ y Jalankan ]") {
		t.Error("tombol harus tetap terlihat saat digulir")
	}
	for i, line := range strings.Split(c.View(80, 20), "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("baris %d selebar %d", i, w)
		}
	}
}

func TestViewTidakMengubahOutput(t *testing.T) {
	f := &fakeSys{streams: map[string]*run.FakeStream{"echo halo": {Lines: []string{"halo"}}}}
	m := runExec(t, run.Single(cmdOf("x", "echo", "halo")), f.deps(false, run.SudoNotNeeded, nil), false)
	first := m.View(80, 20)
	for i := 0; i < 3; i++ {
		m.View(80, 20)
	}
	if m.View(80, 20) != first {
		t.Fatal("render berulang mengubah tampilan")
	}
	if got := m.Outcome().Results[0].Output[0]; got != "halo" {
		t.Fatalf("output tersimpan berubah jadi %q", got)
	}
}
