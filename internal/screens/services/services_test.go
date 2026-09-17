package services

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/systemd"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func fakeRunner() *run.Fake {
	return &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(systemd.ListArgs(false)): {Stdout: `[{"unit":"cron.service","load":"loaded","active":"active","sub":"running","description":"Regular background program processing daemon"},
{"unit":"nginx.service","load":"loaded","active":"failed","sub":"failed","description":"A high performance web server"}]`},
		run.JoinShell(systemd.FilesArgs(false)):                 {Stdout: `[{"unit_file":"cron.service","state":"enabled"},{"unit_file":"nginx.service","state":"enabled"}]`},
		run.JoinShell(systemd.ShowArgs("nginx.service", false)): {Stdout: "Id=nginx.service\nDescription=A high performance web server\nLoadState=loaded\nActiveState=failed\nSubState=failed\nUnitFileState=enabled\nMainPID=0\nExecStart={ path=/usr/sbin/nginx ; argv[]=/usr/sbin/nginx -g daemon on; master_process on; ; ignore_errors=no }\nRestart=on-failure\nNRestarts=5\nResult=exit-code\nCanReload=yes\nFragmentPath=/usr/lib/systemd/system/nginx.service\n"},
		run.JoinShell(syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1, Unit: "nginx.service", Lines: 12}.JSONArgs()): {
			Stdout: `{"MESSAGE":"nginx: [emerg] bind() to 0.0.0.0:80 failed (98: Address already in use)","PRIORITY":"3","SYSLOG_IDENTIFIER":"nginx"}` + "\n",
		},
	}}
}

func TestListDanDetail(t *testing.T) {
	env := shared.Env{Runner: fakeRunner(), Now: time.Now}
	m := New(env)
	m.Update(testutil.Run(m.Init())[0])
	view := ansi.Strip(m.View(120, 20))
	for _, s := range []string{"2 service · 1 berjalan · 1 gagal", "nginx", "failed (failed)", "cron"} {
		if !strings.Contains(view, s) {
			t.Errorf("daftar tidak memuat %q:\n%s", s, view)
		}
	}
	if m.rows[0].Name != "nginx.service" {
		t.Error("service gagal harus paling atas")
	}
	m.Update(testutil.Key("v"))
	m.Update(testutil.Key("v"))
	if len(m.rows) != 1 || m.mode != viewFailed {
		t.Errorf("mode gagal: %d baris", len(m.rows))
	}

	_, cmd := m.Update(testutil.Key("enter"))
	d := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*DetailModel)
	d.Update(testutil.Run(d.Init())[0])
	view = ansi.Strip(d.View(120, 50))
	for _, s := range []string{"bermasalah", "exit-code", "5 kali", "/usr/sbin/nginx -g daemon on; master_process on;", "Address already in use", "systemctl status nginx.service"} {
		if !strings.Contains(view, s) {
			t.Errorf("detail tidak memuat %q:\n%s", s, view)
		}
	}

	_, cmd = d.Update(testutil.Key("a"))
	form := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*ask.Model)
	if !strings.Contains(form.Title(), "nginx.service") {
		t.Error("form aksi")
	}
	_, cmd = d.Update(nav.ResumedMsg{Result: ask.Result{ID: "service-action", Answers: ask.Answers{"aksi": {Values: []string{systemd.ActRestart}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(100, 30))
	if !strings.Contains(confirm, "sudo systemctl restart nginx.service") {
		t.Errorf("konfirmasi:\n%s", confirm)
	}
}

func TestActionFormSesuaiKondisi(t *testing.T) {
	values := func(f ask.Form) map[string]ask.Option {
		out := map[string]ask.Option{}
		for _, o := range f.Questions[0].Options {
			out[o.Value] = o
		}
		return out
	}
	running := systemd.Details{Props: map[string]string{"ActiveState": "active", "SubState": "running", "UnitFileState": "enabled", "CanReload": "yes"}}
	opts := values(ActionForm("nginx.service", running))
	if !opts[systemd.ActReload].Recommended || opts[systemd.ActStart].Value != "" || opts[systemd.ActDisable].Value == "" {
		t.Errorf("service berjalan: %+v", opts)
	}
	stopped := systemd.Details{Props: map[string]string{"ActiveState": "inactive", "SubState": "dead", "UnitFileState": "disabled"}}
	opts = values(ActionForm("myapp.service", stopped))
	if !opts[systemd.ActStart].Recommended || opts[systemd.ActEnableNow].Value == "" || opts[systemd.ActStop].Value != "" {
		t.Errorf("service berhenti: %+v", opts)
	}
	ssh := values(ActionForm("ssh.service", running))
	if ssh[systemd.ActStop].Risk != risk.Dangerous || ssh[systemd.ActDisable].Risk != risk.Dangerous {
		t.Error("stop/disable ssh harus berbahaya")
	}
}
