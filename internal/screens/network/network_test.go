package network

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/netinfo"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/checklist"
)

func TestDashboardDanWizard(t *testing.T) {
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"ip -j addr":        {Stdout: `[{"ifname":"eth0","operstate":"UP","link_type":"ether","flags":["UP","LOWER_UP"],"addr_info":[{"family":"inet","local":"10.0.0.5","prefixlen":24,"scope":"global"}]},{"ifname":"veth9","operstate":"UP","flags":["UP"],"addr_info":[]}]`},
		"ip -j route":       {Stdout: `[{"dst":"default","gateway":"10.0.0.1","dev":"eth0","protocol":"dhcp","prefsrc":"10.0.0.5","metric":100}]`},
		"resolvectl status": {Stdout: "Global\n  resolv.conf mode: stub\nLink 2 (eth0)\n       DNS Servers: 10.0.0.1\n"},
	}}
	env := shared.Env{Runner: fake, ProcRoot: t.TempDir(), Now: time.Now}
	opened := ""
	m := New(env, func(id string) nav.Screen { opened = id; return nil })
	m.Update(testutil.Run(m.Init())[0])
	view := ansi.Strip(m.View(120, 40))
	for _, s := range []string{"eth0", "10.0.0.5/24", "1 interface virtual", "gateway default 10.0.0.1 lewat eth0", "server: 10.0.0.1", "127.0.0.53"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}

	var gotTarget netinfo.Target
	m.outbound = func(tg netinfo.Target) []netinfo.Step {
		gotTarget = tg
		return []netinfo.Step{{Title: "x", Run: func(context.Context) check.Result {
			return check.Result{Status: check.Fail, Summary: "gagal", Next: "firewall"}
		}}}
	}
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "outbound", Answers: ask.Answers{"target": {Text: "https://api.contoh.com/v1"}}}})
	cl := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*checklist.Model)
	if gotTarget.Host != "api.contoh.com" || gotTarget.Port != "443" {
		t.Errorf("target: %+v", gotTarget)
	}
	for _, msg := range testutil.Run(cl.Init()) {
		cl.Update(msg)
	}
	cl.Update(testutil.Key("enter"))
	if opened != "firewall" {
		t.Errorf("checklist harus bisa membuka modul saran lewat opener, dapat %q", opened)
	}

	if got := PublicIPPlan().Steps[0].Preview(false); got != "curl -sS --max-time 10 https://ifconfig.me/ip" {
		t.Errorf("IP publik: %s", got)
	}
	if validPort("0") == nil || validPort("22") != nil {
		t.Error("validPort salah")
	}
}
