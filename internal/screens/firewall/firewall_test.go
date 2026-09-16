package firewall

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysfw "github.com/arif-rachim/ubuntu-tool/internal/sys/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func TestRuleFromAnswers(t *testing.T) {
	action, tg, comment := RuleFromAnswers(ask.Answers{
		"action": {Values: []string{"allow"}}, "target": {Values: []string{"custom"}}, "port": {Text: "5432"},
		"proto": {Values: []string{"tcp"}}, "from": {Other: "10.1.0.0/16"}, "comment": {Text: " db "},
	})
	plan := sysfw.RulePlan(action, tg, comment).Steps[0].Preview(false)
	if plan != "sudo ufw allow from 10.1.0.0/16 to any port 5432 proto tcp comment db" {
		t.Errorf("%s", plan)
	}
	action, tg, _ = RuleFromAnswers(ask.Answers{"action": {Values: []string{"allow"}}, "target": {Values: []string{"port:80,443/tcp"}}, "from": {Values: []string{""}}})
	if got := sysfw.RulePlan(action, tg, "").Steps[0].Preview(false); got != "sudo ufw allow 80,443/tcp" {
		t.Errorf("preset web: %s", got)
	}
}

func TestViewLockoutDanTidakTerbaca(t *testing.T) {
	m := New(shared.Env{Now: time.Now})
	_, rules := sysfw.ParseNumbered("Status: inactive\n[ 1] 80/tcp                     ALLOW IN    Anywhere\n")
	m.Update(statusMsg{owner: m, status: sysfw.Status{Installed: true, Readable: true, Rules: rules}, ssh: sysfw.SSHContext{Port: 22, InSession: true}})
	view := ansi.Strip(m.View(110, 30))
	for _, s := range []string{"tidak aktif", "Tidak ada aturan lain yang mengizinkan port SSH 22", "otomatis menambahkan aturan SSH", "80/tcp", "mana saja"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	_, cmd := m.Update(testutil.Key("e"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(110, 40))
	if !strings.Contains(confirm, "sudo ufw limit 22/tcp") || !strings.Contains(confirm, "sudo ufw --force enable") {
		t.Errorf("aktifkan harus menambah aturan SSH dulu:\n%s", confirm)
	}

	m2 := New(shared.Env{Now: time.Now})
	m2.Update(statusMsg{owner: m2, status: sysfw.Status{Installed: true}})
	if !strings.Contains(ansi.Strip(m2.View(100, 20)), "Tekan s") {
		t.Error("tanpa sudo harus menawarkan membaca dengan sudo")
	}
}
