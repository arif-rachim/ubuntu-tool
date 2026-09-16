package docker

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
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

const psOut = `{"ID":"1","Image":"postgres:17","Labels":"com.docker.compose.project=shop","Names":"shop_db","Ports":"0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp","State":"running","Status":"Up 2 hours (healthy)"}
{"ID":"2","Image":"alpine","Names":"coba","State":"exited","Status":"Exited (0) 3 days ago"}`

func TestDashboard(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, sysdocker.Client{Bin: "/usr/bin/docker"})
	m.Update(statusMsg{owner: m, status: sysdocker.Status{Avail: sysdocker.Ready, Version: "29.1.3", Containers: sysdocker.ParseContainers(psOut),
		Disk: []sysdocker.DiskUsage{{Type: "Images", TotalCount: "3", Active: "2", Size: "8GB", Reclaimable: "2GB (25%)"}}}})
	view := ansi.Strip(m.View(120, 30))
	for _, s := range []string{"Docker 29.1.3", "compose belum terpasang", "images 8GB (bisa dibebaskan 2GB (25%))", "1 berjalan dari 2", "shop_db", "5432→5432", "shop"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	// Container kedua berhenti: e memberi pesan, bukan membuka shell.
	m.Update(testutil.Key("down"))
	if _, cmd := m.Update(testutil.Key("e")); cmd != nil || !strings.Contains(m.message, "tidak berjalan") {
		t.Errorf("shell di container berhenti: %q", m.message)
	}
	m.Update(testutil.Key("up"))
	_, cmd := m.Update(testutil.Key("e"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "docker exec -it shop_db sh -c") {
		t.Errorf("shell:\n%s", confirm)
	}

	m.selected = m.status.Containers[0]
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "ctr-action", Answers: ask.Answers{"action": {Values: []string{"commit"}}, "image": {Text: "saya/db:v1"}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "docker commit shop_db saya/db:v1") {
		t.Errorf("commit:\n%s", confirm)
	}
}

func TestTidakTersedia(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, sysdocker.Client{Bin: "/usr/bin/docker"})
	m.Update(statusMsg{owner: m, status: sysdocker.Status{Avail: sysdocker.NoPermission, Error: "permission denied while trying to connect"}})
	view := ansi.Strip(m.View(100, 30))
	if !strings.Contains(view, "tidak punya izin") || !strings.Contains(view, "grup docker") {
		t.Errorf("%s", view)
	}
	_, cmd := m.Update(testutil.Key("s"))
	if confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(100, 30)); !strings.Contains(confirm, "sudo docker info") {
		t.Errorf("%s", confirm)
	}
	m.Update(nav.ResumedMsg{Result: run.Outcome{Plan: run.Plan{Title: "x"}, Approved: true, Results: []run.StepResult{{Status: run.StatusOK}}}})
	if !m.client.Sudo {
		t.Error("setelah sudo berhasil, client harus memakai sudo")
	}
}

func TestWizardFile(t *testing.T) {
	dir := t.TempDir()
	m := newModel(shared.Env{Now: time.Now}, sysdocker.Client{})
	answers := ask.Answers{"kind": {Values: []string{"compose"}}, "dir": {Text: dir}, "service": {Text: "db"}, "source": {Values: []string{"image"}},
		"image": {Values: []string{"postgres:17"}}, "ports": {Text: "127.0.0.1:5432:5432"}, "volumes": {Text: "dbdata:/var/lib/postgresql/data"},
		"env": {Text: "POSTGRES_PASSWORD=x"}, "restart": {Values: []string{"unless-stopped"}}}
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "files", Answers: answers}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 60))
	for _, s := range []string{"install -m 0644 /dev/stdin " + filepath.Join(dir, "docker-compose.yml"), "image: postgres:17", "dbdata:"} {
		if !strings.Contains(confirm, s) {
			t.Errorf("tidak memuat %q:\n%s", s, confirm)
		}
	}
	os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("lama"), 0o644)
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "files", Answers: answers}})
	if q := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 30)); !strings.Contains(q, "sudah ada") {
		t.Errorf("harus bertanya sebelum menimpa:\n%s", q)
	}
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "overwrite", Answers: ask.Answers{"ok": {Values: []string{ask.ValueNo}}}}})
	if cmd != nil || !strings.Contains(m.message, "tidak diubah") {
		t.Error("menolak timpa harus membatalkan")
	}
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "overwrite", Answers: ask.Answers{"ok": {Values: []string{ask.ValueYes}}}}})
	if c := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 60)); !strings.Contains(c, "TIMPA") {
		t.Errorf("timpa:\n%s", c)
	}

	p := pendingFromAnswers(ask.Answers{"kind": {Values: []string{"dockerfile"}}, "dir": {Text: dir}, "app": {Values: []string{"node"}}, "cmd": {Text: "node dist/index.js"}})
	if df := p.files["Dockerfile"]; !strings.Contains(df, `CMD ["node", "dist/index.js"]`) || !strings.Contains(df, "EXPOSE 3000") {
		t.Errorf("%s", df)
	}
	if got := ansi.Strip(shortTimestamp("2026-09-16T16:55:19.541603281Z halo dunia")); !strings.HasSuffix(got, ":55:19 halo dunia") || len(got) > 30 {
		t.Errorf("timestamp: %q", got)
	}
	if shortPorts("127.0.0.1:8080->80/tcp, 9000/tcp") != "lokal:8080→80 9000" {
		t.Error(shortPorts("127.0.0.1:8080->80/tcp, 9000/tcp"))
	}
}
