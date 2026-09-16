package systemd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

const listJSON = `[{"unit":"cron.service","load":"loaded","active":"active","sub":"running","description":"Regular background program processing daemon"},
{"unit":"nginx.service","load":"loaded","active":"failed","sub":"failed","description":"A high performance web server"},
{"unit":"apt-daily.service","load":"loaded","active":"inactive","sub":"dead","description":"Daily apt download activities"}]`

const filesJSON = `[{"unit_file":"cron.service","state":"enabled","preset":"enabled"},{"unit_file":"nginx.service","state":"disabled","preset":"enabled"},{"unit_file":"apt-daily.service","state":"static","preset":null}]`

func TestList(t *testing.T) {
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(ListArgs(false)):  {Stdout: listJSON},
		run.JoinShell(FilesArgs(false)): {Stdout: filesJSON},
	}}
	units, err := List(context.Background(), fake, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range units {
		names = append(names, u.Name)
	}
	if strings.Join(names, ",") != "nginx.service,cron.service,apt-daily.service" {
		t.Errorf("urutan gagal→aktif→lainnya: %v", names)
	}
	if !units[0].Failed() || units[0].Enabled() || !units[1].Enabled() || units[2].FileState != "static" {
		t.Errorf("%+v", units)
	}
}

func TestListCadanganTeksDanTanpaSystemd(t *testing.T) {
	text := "cron.service loaded active running Regular background program processing daemon\nssh.service loaded inactive dead OpenBSD Secure Shell server\n"
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(ListArgs(false)): {Stderr: "unknown output", Err: errors.New("exit 1")},
		"systemctl list-units --type=service --all --no-legend --plain --full --no-pager": {Stdout: text},
		run.JoinShell(FilesArgs(false)): {Err: errors.New("x")},
	}}
	units, err := List(context.Background(), fake, false)
	if err != nil || len(units) != 2 || units[1].Description != "OpenBSD Secure Shell server" {
		t.Fatalf("cadangan teks: %+v %v", units, err)
	}

	container := &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(ListArgs(false)): {Stderr: "System has not been booted with systemd as init system (PID 1). Can't operate.", Err: errors.New("exit 1")},
	}}
	if _, err := List(context.Background(), container, false); !errors.Is(err, ErrNoSystemd) {
		t.Errorf("harus ErrNoSystemd: %v", err)
	}
}

func TestParseShowDanDetails(t *testing.T) {
	out := `Id=cron.service
ExecStart={ path=/usr/sbin/cron ; argv[]=/usr/sbin/cron -f -P $EXTRA_OPTS ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }
Environment=FOO=bar BAZ=1
MemoryCurrent=540672
CPUUsageNSec=[not set]
NRestarts=3
Result=exit-code
ActiveEnterTimestamp=Wed 2026-09-16 20:51:10 +04
WorkingDirectory=

Id=ssh.service
LoadState=not-found
`
	blocks := ParseShow(out)
	if len(blocks) != 2 || blocks[1]["LoadState"] != "not-found" {
		t.Fatalf("%+v", blocks)
	}
	d := Details{Props: blocks[0]}
	if d.Command("ExecStart") != "/usr/sbin/cron -f -P $EXTRA_OPTS" {
		t.Errorf("ExecStart: %q", d.Command("ExecStart"))
	}
	if d.Get("Environment") != "FOO=bar BAZ=1" || d.Get("CPUUsageNSec") != "" || d.Int("MemoryCurrent") != 540672 {
		t.Errorf("nilai: env=%q cpu=%q mem=%d", d.Get("Environment"), d.Get("CPUUsageNSec"), d.Int("MemoryCurrent"))
	}
	if ts := d.Since("ActiveEnterTimestamp"); ts.IsZero() || ts.Hour() != 20 {
		t.Errorf("timestamp: %v", ts)
	}
	if !d.CrashLooping() {
		t.Error("NRestarts 3 + Result exit-code harus crash loop")
	}
}

func TestPlan(t *testing.T) {
	c := Plan(ActRestart, "nginx.service", false).Steps[0]
	if c.Preview(false) != "sudo systemctl restart nginx.service" || c.Risk != risk.Caution || c.Safer == "" {
		t.Errorf("restart: %s %v", c.Preview(false), c.Risk)
	}
	if c := Plan(ActStop, "ssh.service", false).Steps[0]; c.Risk != risk.Dangerous || !strings.Contains(c.Effect, "SSH") {
		t.Errorf("stop ssh harus berbahaya: %+v", c)
	}
	if c := Plan(ActRestart, "ssh.service", false).Steps[0]; c.Risk != risk.Caution {
		t.Errorf("restart ssh tidak memutus sesi yang ada: %v", c.Risk)
	}
	if c := Plan(ActEnableNow, "myapp.service", true).Steps[0]; c.Preview(false) != "systemctl --user enable --now myapp.service" {
		t.Errorf("enable --now user: %s", c.Preview(false))
	}
	if c := Plan(ActDisable, "docker.service", false).Steps[0]; c.Risk != risk.Dangerous {
		t.Error("disable docker harus berbahaya")
	}
	if len(Plan("tidak-ada", "x", false).Steps) != 0 {
		t.Error("aksi tidak dikenal harus kosong")
	}
}
