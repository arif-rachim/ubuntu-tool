package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

func testEnv(fake *run.Fake, start run.Starter) shared.Env {
	return shared.Env{Runner: fake, Now: time.Now, UID: 1000, Deps: runflow.Deps{Start: start}}
}

func TestEntriesTampilFilterDetail(t *testing.T) {
	q := syslogs.Query{Boot: syslogs.BootPtr(0), Priority: syslogs.PrioErr}
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(q.JSONArgs()): {
			Stdout: `{"__REALTIME_TIMESTAMP":"1789579028464058","PRIORITY":"3","SYSLOG_IDENTIFIER":"sudo","_SYSTEMD_UNIT":"user@1000.service","MESSAGE":"auth could not identify password"}
{"__REALTIME_TIMESTAMP":"1789579029000000","PRIORITY":"2","SYSLOG_IDENTIFIER":"kernel","MESSAGE":"Out of memory: Killed process 2211 (node)"}
`,
			Stderr: "Hint: You are currently not seeing messages from other users and the system.",
		},
	}}
	m := NewEntries(testEnv(fake, nil), "Error sejak boot ini", q)
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	view := ansi.Strip(m.View(140, 30))
	for _, s := range []string{"journalctl -b -p err -n 300 --no-pager", "Sebagian log", "auth could not identify", "Out of memory", "kritis"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	if m.viewer.Cursor != 1 {
		t.Errorf("kursor harus di entri terbaru, dapat %d", m.viewer.Cursor)
	}

	m.Update(testutil.Key("/"))
	for _, msg := range testutil.Type("memory") {
		m.Update(msg)
	}
	m.Update(testutil.Key("enter"))
	if len(m.shown) != 1 {
		t.Fatalf("filter memory: %d baris", len(m.shown))
	}
	m.Update(testutil.Key("enter"))
	if m.detail == nil || !strings.Contains(ansi.Strip(m.View(140, 30)), "Killed process 2211") {
		t.Fatal("enter harus membuka detail")
	}
	m.Update(testutil.Key("esc"))
	if m.detail != nil {
		t.Error("esc harus menutup detail")
	}

	_, cmd := m.Update(testutil.Key("u"))
	msgs := testutil.Run(cmd)
	if len(msgs) != 1 || !strings.Contains(ansi.Strip(msgs[0].(nav.PushMsg).Screen.View(100, 30)), "usermod -aG adm") {
		t.Errorf("u harus menawarkan usermod -aG adm: %v", msgs)
	}
}

func TestEntriesFollow(t *testing.T) {
	q := syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1}
	fake := &run.Fake{Responses: map[string]run.FakeResponse{run.JoinShell(q.JSONArgs()): {Stdout: ""}}}
	var gotArgv []string
	stream := &run.FakeStream{Lines: []string{`{"MESSAGE":"baris baru","PRIORITY":"6","SYSLOG_IDENTIFIER":"logger"}`}}
	start := func(_ context.Context, argv []string, _ string) (run.Stream, error) {
		gotArgv = argv
		return stream, nil
	}
	m := NewEntries(testEnv(fake, start), "Semua", q)
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	_, cmd := m.Update(testutil.Key("f"))
	if !m.Busy() {
		t.Fatal("mode ikuti harus Busy supaya q/ctrl+c menghentikan follow dulu")
	}
	// Pompa pesan stream sampai selesai.
	for i := 0; cmd != nil && i < 10; i++ {
		msgs := testutil.Run(cmd)
		cmd = nil
		for _, msg := range msgs {
			_, cmd = m.Update(msg)
		}
	}
	joined := strings.Join(gotArgv, " ")
	if !strings.HasSuffix(joined, "-n 0 -f") {
		t.Errorf("argv follow: %s", joined)
	}
	if len(m.entries) != 1 || m.entries[0].Message != "baris baru" {
		t.Errorf("entri dari stream: %+v", m.entries)
	}
	if m.following {
		t.Error("stream selesai harus menghentikan mode ikuti")
	}
}

func TestMenuPresetDanPlan(t *testing.T) {
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"journalctl --disk-usage":                    {Stdout: "Archived and active journals take up 2.0G in the file system."},
		"journalctl --list-boots -o json --no-pager": {Stdout: `[{"index":0,"boot_id":"a","first_entry":1,"last_entry":2}]`},
	}}
	m := New(testEnv(fake, nil))
	m.journalDir = filepath.Join(t.TempDir(), "tidak-ada")
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	view := ansi.Strip(m.View(120, 40))
	for _, s := range []string{"hanya di memori", "ukuran 2,0 GiB — tekan v", "1 boot tercatat", "Error sejak boot ini", "$ journalctl -b -p err"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}

	// Boot sebelumnya tidak tersedia bila hanya 1 boot.
	m.cursor = 4
	if cmd := m.open(m.items[4]); cmd != nil || !strings.Contains(m.message, "hanya menyimpan boot ini") {
		t.Errorf("boot sebelumnya harus dijelaskan tidak tersedia: %q", m.message)
	}

	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "unit", Answers: ask.Answers{"unit": {Text: "nginx"}}}})
	msgs := testutil.Run(cmd)
	if e, ok := msgs[0].(nav.PushMsg).Screen.(*EntriesModel); !ok || e.query.Unit != "nginx.service" {
		t.Errorf("unit harus dinormalisasi: %+v", msgs)
	}

	steps := PersistentPlan().Steps
	if steps[0].Preview(false) != "sudo mkdir -p /var/log/journal" || steps[2].Preview(false) != "sudo systemctl restart systemd-journald" {
		t.Errorf("plan persisten: %s / %s", steps[0].Preview(false), steps[2].Preview(false))
	}
}

func TestFilesDanTail(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "syslog"), []byte("satu\ndua\n"), 0o644)
	m := NewFiles(testEnv(&run.Fake{}, nil), dir)
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	_, cmd := m.Update(testutil.Key("enter"))
	msgs := testutil.Run(cmd)
	tail := msgs[0].(nav.PushMsg).Screen.(*TailModel)
	for _, msg := range testutil.Run(tail.Init()) {
		tail.Update(msg)
	}
	view := ansi.Strip(tail.View(100, 20))
	if !strings.Contains(view, "dua") || !strings.Contains(view, "tail -n 1000") {
		t.Errorf("tail:\n%s", view)
	}
}
