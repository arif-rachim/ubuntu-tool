package history

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

func TestRiwayatDanEkspor(t *testing.T) {
	dir := t.TempDir()
	h := &run.History{Path: filepath.Join(dir, "history.log")}
	old := time.Date(2026, 9, 1, 8, 0, 0, 0, time.Local)
	h.Append(run.Entry{Time: old, Title: "Install nginx", Argv: []string{"apt-get", "install", "-y", "nginx"}, Sudo: true})
	h.Append(run.Entry{Time: old.Add(time.Hour), Title: "Reload nginx", Argv: []string{"systemctl", "reload", "nginx"}, Sudo: true, ExitCode: 1})
	m := New(shared.Env{Now: time.Now}, h)
	for _, msg := range testutil.Run(m.Init()) {
		m.Update(msg)
	}
	view := ansi.Strip(m.View(120, 20))
	if !strings.Contains(view, "Riwayat (2 command)") || strings.Index(view, "Reload nginx") > strings.Index(view, "Install nginx") || !strings.Contains(view, "gagal") {
		t.Errorf("terbaru harus di atas:\n%s", view)
	}
	_, cmd := m.Update(testutil.Key("enter"))
	detail := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 20))
	if !strings.Contains(detail, "gagal (exit 1)") || !strings.Contains(detail, "# sudo systemctl reload nginx") {
		t.Errorf("detail:\n%s", detail)
	}

	path := filepath.Join(dir, "setup.sh")
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "export", Answers: ask.Answers{"last": {Values: []string{"0"}}, "failed": {Values: []string{ask.ValueNo}}, "path": {Text: path}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 60))
	if !strings.Contains(confirm, "install -m 0700 /dev/stdin "+path) || !strings.Contains(confirm, "sudo apt-get install -y nginx") || strings.Contains(confirm, "│ sudo systemctl reload") {
		t.Errorf("konfirmasi ekspor:\n%s", confirm)
	}
	if ExportForm(2, time.Now()).Questions[2].Validate("relatif.sh") == nil {
		t.Error("path relatif harus ditolak")
	}
	os.WriteFile(path, nil, 0o600)
	if err := ExportForm(2, time.Now()).Questions[2].Validate(path); err == nil || !strings.Contains(err.Error(), "ditimpa") {
		t.Errorf("file ada harus diberi peringatan: %v", err)
	}
}
