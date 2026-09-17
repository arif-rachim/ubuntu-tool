package procs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseCgroup(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Cgroup
	}{
		{"service sistem", "0::/system.slice/nginx.service\n", Cgroup{Path: "/system.slice/nginx.service", Unit: "nginx.service"}},
		{
			"unit user",
			"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome.scope\n",
			Cgroup{Path: "/user.slice/user-1000.slice/user@1000.service/app.slice/app-gnome.scope", Unit: "app-gnome.scope", UserUnit: true},
		},
		{"sesi login", "0::/user.slice/user-1000.slice/session-2.scope", Cgroup{Path: "/user.slice/user-1000.slice/session-2.scope", Unit: "session-2.scope"}},
		{"user manager sendiri", "0::/user.slice/user-1000.slice/user@1000.service", Cgroup{Path: "/user.slice/user-1000.slice/user@1000.service", Unit: "user@1000.service"}},
		{
			"docker",
			"0::/system.slice/docker-3f2a9c.scope",
			Cgroup{Path: "/system.slice/docker-3f2a9c.scope", Unit: "docker-3f2a9c.scope", Container: "3f2a9c"},
		},
		{
			"cgroup v1",
			"12:pids:/system.slice/ssh.service\n1:name=systemd:/system.slice/ssh.service\n0::/",
			Cgroup{Path: "/system.slice/ssh.service", Unit: "ssh.service"},
		},
		{"init", "0::/init.scope", Cgroup{Path: "/init.scope", Unit: "init.scope"}},
		{"kosong", "", Cgroup{}},
	}
	for _, tt := range tests {
		if got := ParseCgroup(tt.in); got != tt.want {
			t.Errorf("%s: %+v, ingin %+v", tt.name, got, tt.want)
		}
	}
	if !ParseCgroup("0::/system.slice/nginx.service").Service() || ParseCgroup("0::/init.scope").Service() {
		t.Error("Service() salah")
	}
}

func TestParseStatDenganNamaAneh(t *testing.T) {
	stat := "28822 (my (weird) app) S 28801 28822 28801 0 -1 4194304 90 0 0 0 150 50 0 0 20 0 3 0 87421 17522688 439"
	ticks, ok := parseStartTicks(stat)
	if !ok || ticks != 87421 {
		t.Errorf("starttime %d %v", ticks, ok)
	}
	cpu, ok := ParseStatTimes(stat)
	if !ok || cpu != 200 {
		t.Errorf("utime+stime %d %v", cpu, ok)
	}
}

func TestSplitCmdline(t *testing.T) {
	got := SplitCmdline([]byte("python3\x00-m\x00http.server\x008080\x00"))
	if !reflect.DeepEqual(got, []string{"python3", "-m", "http.server", "8080"}) {
		t.Errorf("%q", got)
	}
	if SplitCmdline([]byte("")) != nil {
		t.Error("kernel thread harus kosong")
	}
}

func TestReadFakeProc(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "4321")
	os.MkdirAll(dir, 0o755)
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("status", "Name:\tpython3\nState:\tS (sleeping)\nPPid:\t100\nUid:\t1000\t0\t0\t0\nThreads:\t4\nVmRSS:\t  20480 kB\n")
	write("cmdline", "python3\x00-m\x00http.server\x008080\x00")
	write("stat", "4321 (python3) S 100 4321 100 0 -1 0 0 0 0 0 5 5 0 0 20 0 4 0 12345 0 0")
	write("cgroup", "0::/system.slice/demo.service\n")
	os.Symlink("/usr/bin/python3.12 (deleted)", filepath.Join(dir, "exe"))
	os.Symlink("/srv/app", filepath.Join(dir, "cwd"))
	os.WriteFile(filepath.Join(root, "stat"), []byte("cpu  1 2 3\nbtime 1700000000\n"), 0o644)

	p, err := Read(root, 4321)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "python3" || p.PPID != 100 || p.UID != 0 || p.User != "root" || p.Threads != 4 || p.RSSKB != 20480 {
		t.Errorf("status: %+v", p)
	}
	if p.Exe != "/usr/bin/python3.12" || p.Cwd != "/srv/app" || p.Cgroup.Unit != "demo.service" {
		t.Errorf("exe/cwd/unit: %+v", p)
	}
	wantStart := time.Unix(1700000000, 0).Add(123450 * time.Millisecond)
	if !p.Started.Equal(wantStart) {
		t.Errorf("mulai %v, ingin %v", p.Started, wantStart)
	}
	if !strings.Contains(p.CommandLine(), "http.server 8080") {
		t.Errorf("cmdline %q", p.CommandLine())
	}
	if !Exists(root, 4321) || Exists(root, 9999) {
		t.Error("Exists salah")
	}
	if _, err := Read(root, 9999); err == nil {
		t.Error("proses tidak ada harus error")
	}
}

func TestReadDiriSendiri(t *testing.T) {
	p, err := Read("/proc", os.Getpid())
	if err != nil {
		t.Skipf("tidak ada /proc: %v", err)
	}
	exe, _ := os.Executable()
	if p.Exe != exe || p.Started.IsZero() || time.Since(p.Started) > time.Hour {
		t.Errorf("proses sendiri: exe %q (ingin %q) mulai %v", p.Exe, exe, p.Started)
	}
}
