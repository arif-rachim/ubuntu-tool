package resource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func fakeProc(t *testing.T) string {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write("meminfo", "MemTotal: 1048576 kB\nMemAvailable: 52428 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n")
	write("loadavg", "4.00 3.00 2.00 3/200 1\n")
	write("uptime", "90000 1\n")
	write("stat", "cpu  100 0 0 100 0 0 0 0 0 0\ncpu0 100 0 0 100 0 0 0 0 0 0\nbtime 1700000000\n")
	write("4242/stat", "4242 (yes) R 1 1 1 0 -1 0 0 0 0 0 500 0 0 0 20 0 1 0 5 0 0")
	write("4242/status", "Name:\tyes\nPPid:\t1\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t 1024 kB\n")
	write("4242/cmdline", "yes\x00")
	write("4242/cgroup", "0::/user.slice/user-1000.slice/session-2.scope\n")
	return root
}

func TestResourceTampilanDanAksi(t *testing.T) {
	root := fakeProc(t)
	now := time.Unix(1700001000, 0)
	env := shared.Env{
		ProcRoot: root, UID: 1000, Now: func() time.Time { return now },
		Runner: &run.Fake{Responses: map[string]run.FakeResponse{
			"journalctl -k -b --grep 'Out of memory|oom-kill' -o short-iso --no-pager": {
				Stdout: "2023-11-14T23:00:00+0000 h kernel: Out of memory: Killed process 777 (java) total-vm:1kB\n",
			},
		}},
	}
	m := New(env)
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	view := ansi.Strip(m.View(120, 40))
	for _, s := range []string{"RAM hampir habis", "load 4.00 di 1 core", "Tidak ada swap", "1 proses dimatikan OOM killer", "java", "yes", "menyala 1 hari 1 jam"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}

	_, cmd := m.Update(testutil.Key("enter"))
	msgs := testutil.Run(cmd)
	if len(msgs) != 1 {
		t.Fatalf("enter harus membuka pilihan aksi: %v", msgs)
	}
	form := msgs[0].(nav.PushMsg).Screen.(*ask.Model)
	if !strings.Contains(form.Title(), "yes (PID 4242)") {
		t.Errorf("judul form: %s", form.Title())
	}

	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "proc-action", Answers: ask.Answers{"aksi": {Values: []string{"renice"}}}}})
	msgs = testutil.Run(cmd)
	if len(msgs) != 1 {
		t.Fatalf("aksi harus membuka konfirmasi: %v", msgs)
	}
	confirm := ansi.Strip(msgs[0].(nav.PushMsg).Screen.View(100, 30))
	if !strings.Contains(confirm, "renice -n 10 -p 4242") {
		t.Errorf("konfirmasi renice:\n%s", confirm)
	}

	m.Update(testutil.Key("p"))
	if !m.paused || !strings.Contains(ansi.Strip(m.View(120, 40)), "dijeda") {
		t.Error("p harus menjeda")
	}
}

func TestKursorTidakMelompatSebelumDigerakkan(t *testing.T) {
	root := fakeProc(t)
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	// Proses besar RAM tapi CPU kecil: di sampel pertama (urut RAM) ada di atas.
	write("5000/stat", "5000 (big) S 1 1 1 0 -1 0 0 0 0 0 1 0 0 0 20 0 1 0 5 0 0")
	write("5000/status", "Name:\tbig\nUid:\t0\t0\t0\t0\nVmRSS:\t 900000 kB\n")
	now := time.Unix(1700001000, 0)
	env := shared.Env{ProcRoot: root, Now: func() time.Time { return now }, Runner: &run.Fake{}}
	m := New(env)
	m.Update(testutil.Run(m.sample())[0])
	now = now.Add(time.Second)
	write("4242/stat", "4242 (yes) R 1 1 1 0 -1 0 0 0 0 0 600 0 0 0 20 0 1 0 5 0 0")
	m.Update(testutil.Run(m.sample())[0])
	if m.table.Cursor != 0 || m.top[0].PID != 4242 {
		t.Fatalf("kursor harus tetap di baris pertama (proses CPU tertinggi): cursor %d top %d", m.table.Cursor, m.top[0].PID)
	}
}
