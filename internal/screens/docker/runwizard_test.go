package docker

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func answers(kv map[string]ask.Answer) ask.Answers {
	a := ask.Answers{}
	for k, v := range kv {
		a[k] = v
	}
	return a
}

func TestSpecFromAnswersDasar(t *testing.T) {
	a := answers(map[string]ask.Answer{
		"image":   {Values: []string{"nginx:alpine"}},
		"name":    {Text: "web"},
		"mode":    {Values: []string{modeDetach}},
		"ports":   {Text: "127.0.0.1:8080:80"},
		"volumes": {Text: "web-data:/usr/share/nginx/html"},
		"envkind": {Values: []string{"none"}},
		"restart": {Values: []string{"unless-stopped"}},
	})
	s, err := SpecFromAnswers(a)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Detach || s.Interactive || s.Restart != "unless-stopped" {
		t.Errorf("mode = %+v", s)
	}
	if len(s.Ports) != 1 || len(s.Volumes) != 1 || len(s.Env) != 0 {
		t.Errorf("spec = %+v", s)
	}
}

func TestSpecFromAnswersLanjutan(t *testing.T) {
	a := answers(map[string]ask.Answer{
		"image": {Values: []string{"python:3.12-slim"}}, "name": {Text: "worker"},
		"mode": {Values: []string{modeDetach}}, "restart": {Values: []string{"on-failure"}},
		"envkind": {Values: []string{"file"}}, "envfile": {Text: "/etc/hostname"},
		"advanced": {Values: []string{ask.ValueYes}},
		"workdir":  {Text: "/app"}, "command": {Text: "python -m app.main"},
		"user": {Text: "1000:1000"}, "network": {Values: []string{"app-net"}},
		"memory": {Text: "512m"}, "cpus": {Text: "1.5"}, "health": {Text: "python -c 'import sys'"},
	})
	s, err := SpecFromAnswers(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Command) != 3 || s.Command[2] != "app.main" {
		t.Errorf("command = %q", s.Command)
	}
	if s.EnvFile != "/etc/hostname" || len(s.Env) != 0 {
		t.Errorf("env = %+v", s)
	}
	if s.Workdir != "/app" || s.User != "1000:1000" || s.Network != "app-net" || s.Memory != "512m" || s.CPUs != "1.5" {
		t.Errorf("setting lanjutan = %+v", s)
	}
}

func TestSpecFromAnswersModeSekaliJalan(t *testing.T) {
	a := answers(map[string]ask.Answer{
		"image": {Values: []string{"alpine"}}, "name": {Text: "coba"},
		"mode": {Values: []string{modeOnce}}, "envkind": {Values: []string{"none"}},
	})
	s, err := SpecFromAnswers(a)
	if err != nil {
		t.Fatal(err)
	}
	if !s.AutoRemove || !s.Interactive || s.Detach {
		t.Errorf("spec = %+v", s)
	}
}

func TestSpecFromAnswersCommandRusak(t *testing.T) {
	a := answers(map[string]ask.Answer{
		"image": {Values: []string{"alpine"}}, "name": {Text: "coba"}, "mode": {Values: []string{modeDetach}},
		"envkind": {Values: []string{"none"}}, "restart": {Values: []string{"no"}},
		"advanced": {Values: []string{ask.ValueYes}}, "command": {Text: `sh -c "belum ditutup`},
	})
	if _, err := SpecFromAnswers(a); err == nil {
		t.Error("kutip yang tidak ditutup harus jadi error, bukan container salah jalan")
	}
}

// Wizard container di dashboard menghasilkan Plan yang bisa dibaca user sebelum dijalankan.
func TestDashboardMenjalankanContainerBaru(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, sysdocker.Client{Bin: "/usr/bin/docker"})
	m.Update(statusMsg{owner: m, status: sysdocker.Status{Avail: sysdocker.Ready, Version: "29.1.3"}})

	_, cmd := m.Update(testutil.Key("c"))
	msgs := testutil.Run(cmd)
	if len(msgs) == 0 {
		t.Fatal("tombol c tidak membuka wizard")
	}
	if _, ok := msgs[0].(nav.PushMsg); !ok {
		t.Fatalf("pesan = %T", msgs[0])
	}

	a := answers(map[string]ask.Answer{
		"image": {Values: []string{"postgres:17"}}, "name": {Text: "db"},
		"mode": {Values: []string{modeDetach}}, "restart": {Values: []string{"unless-stopped"}},
		"ports": {Text: "127.0.0.1:5432:5432"}, "volumes": {Text: "dbdata:/var/lib/postgresql/data"},
		"envkind": {Values: []string{"inline"}}, "env": {Text: "POSTGRES_PASSWORD=rahasia"},
		"save": {Values: []string{ask.ValueNo}},
	})
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "run", Answers: a}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 50))
	for _, want := range []string{"docker run -d --name db", "-p 127.0.0.1:5432:5432", "-v dbdata:/var/lib/postgresql/data", "--restart unless-stopped"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("layar konfirmasi tidak memuat %q:\n%s", want, confirm)
		}
	}
	// Nilai environment ikut terlihat, dan user diberi tahu risikonya.
	if !strings.Contains(confirm, "Untuk password, pakai") {
		t.Errorf("tidak ada saran memakai berkas environment untuk rahasia:\n%s", confirm)
	}
}

func TestDashboardMembukaLayarBaru(t *testing.T) {
	for _, tc := range []struct{ key, title string }{
		{"v", "Volume & Network"}, {"u", "Registry"}, {"s", "Statistik"},
	} {
		m := newModel(shared.Env{Now: time.Now}, sysdocker.Client{Bin: "/usr/bin/docker"})
		m.Update(statusMsg{owner: m, status: sysdocker.Status{Avail: sysdocker.Ready}})
		_, cmd := m.Update(testutil.Key(tc.key))
		msgs := testutil.Run(cmd)
		if len(msgs) == 0 {
			t.Errorf("tombol %s tidak membuka apa pun", tc.key)
			continue
		}
		push, ok := msgs[0].(nav.PushMsg)
		if !ok {
			t.Errorf("tombol %s: pesan %T", tc.key, msgs[0])
			continue
		}
		if push.Screen.Title() != tc.title {
			t.Errorf("tombol %s membuka %q, ingin %q", tc.key, push.Screen.Title(), tc.title)
		}
	}
}

func TestBuildSpecFromAnswers(t *testing.T) {
	dir := t.TempDir()
	a := answers(map[string]ask.Answer{
		"context": {Text: dir}, "tag": {Text: "app:1.0"},
		"args": {Text: "VERSI=1.0, MODE=produksi"}, "fresh": {Values: []string{"nocache"}},
	})
	s := BuildSpecFromAnswers(a)
	if s.Tag != "app:1.0" || len(s.BuildArgs) != 2 || !s.NoCache || !s.Pull {
		t.Errorf("spec = %+v", s)
	}
	if got := strings.Join(s.Args(), " "); !strings.Contains(got, "--build-arg VERSI=1.0 --build-arg MODE=produksi --no-cache --pull") {
		t.Errorf("argv = %s", got)
	}
}

func TestValidBuildContextButuhDockerfile(t *testing.T) {
	dir := t.TempDir()
	if err := sysdocker.ValidBuildContext(dir); err == nil || !strings.Contains(err.Error(), "Dockerfile") {
		t.Errorf("direktori tanpa Dockerfile: %v", err)
	}
	if err := sysdocker.ValidBuildContext(dir + "/tidak-ada"); err == nil {
		t.Error("direktori tidak ada harus ditolak")
	}
}
