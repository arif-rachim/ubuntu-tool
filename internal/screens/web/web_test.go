package web

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysweb "github.com/arif-rachim/ubuntu-tool/internal/sys/web"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func TestTanpaNginx(t *testing.T) {
	m := New(shared.Env{Now: time.Now})
	m.Update(stateMsg{owner: m, state: sysweb.State{Servers: []sysweb.Server{{Name: "nginx"}, {Name: "apache2"}}}})
	view := ansi.Strip(m.View(100, 30))
	for _, s := range []string{"tidak terpasang", "Tekan i", "certbot belum terpasang"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	_, cmd := m.Update(testutil.Key("i"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(110, 40))
	if !strings.Contains(confirm, "sudo apt-get install") || !strings.Contains(confirm, "nginx") {
		t.Errorf("install nginx:\n%s", confirm)
	}
	_, cmd = m.Update(testutil.Key("p"))
	if cmd != nil || !strings.Contains(m.View(100, 30), "Pasang nginx dulu") {
		t.Error("p tanpa nginx harus memberi pesan")
	}
}

func TestDashboardDanHTTPS(t *testing.T) {
	m := New(shared.Env{Now: time.Now})
	sites := []sysweb.Site{
		{Name: "app.contoh.com", Enabled: true, ServerNames: []string{"app.contoh.com"}, ProxyPass: []string{"http://127.0.0.1:3000"}},
		{Name: "default", ServerNames: []string{"_"}, Root: "/var/www/html"},
	}
	m.Update(stateMsg{owner: m, state: sysweb.State{Servers: []sysweb.Server{{Name: "nginx", Installed: true, Active: true}, {Name: "apache2", Installed: true, Active: true}}, Sites: sites, Certbot: "/usr/bin/certbot"}})
	view := ansi.Strip(m.View(120, 30))
	for _, s := range []string{"Lebih dari satu web server", "app.contoh.com", "http://127.0.0.1:3000", "file: /var/www/html", "certbot terpasang"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	f := HTTPSForm(sites)
	if opts := f.Questions[0].Options; len(opts) != 1 || opts[0].Value != "app.contoh.com" {
		t.Errorf("opsi https: %+v", opts)
	}
	var cmds []string
	for _, s := range HTTPSPlan("/usr/bin/certbot", "app.contoh.com", false).Sequence() {
		cmds = append(cmds, s.Command.Preview(false))
	}
	if got := strings.Join(cmds, "\n"); got != "getent ahosts app.contoh.com\nsudo nginx -t\nsudo /usr/bin/certbot --nginx -d app.contoh.com" {
		t.Errorf("plan https:\n%s", got)
	}
	spec := ProxyFromAnswers(ask.Answers{"domain": {Text: " App.Contoh.com "}, "port": {Other: "8080"}, "ws": {Values: []string{ask.ValueYes}}})
	if spec.Domain != "app.contoh.com" || spec.Port != "8080" || !spec.WebSocket {
		t.Errorf("%+v", spec)
	}
	if err := ProxyForm("/proc", sites).Questions[0].Validate("app.contoh.com"); err == nil || !strings.Contains(err.Error(), "sudah dipakai") {
		t.Errorf("domain duplikat harus ditolak: %v", err)
	}
}
