package packages

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspkg "github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func testModel(t *testing.T) (*Model, string) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "reboot-required"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "reboot-required.pkgs"), []byte("linux-image-6.8.0-45-generic\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "history.log"), []byte("Start-Date: 2026-09-16  20:49:19\nCommandline: apt-get install -y docker.io\nInstall: docker.io:amd64 (29.1)\nEnd-Date: 2026-09-16  20:49:27\n"), 0o644)
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"apt list --upgradable":                 {Stdout: "Listing...\nopenssl/noble-updates,noble-security 3.0.13-5 amd64 [upgradable from: 3.0.13-4]\ncurl/noble-updates 8.5-2 amd64 [upgradable from: 8.5-1]\n"},
		"dpkg --audit":                          {Stdout: ""},
		"snap list":                             {Err: errors.New("x")},
		"apt-cache search htop":                 {Stdout: "btop - resource monitor\nhtop - interactive processes viewer\n"},
		"apt-cache policy htop":                 {Stdout: "htop:\n  Installed: (none)\n  Candidate: 3.3.0-4build1\n  Version table:\n     3.3.0-4build1 500\n        500 http://archive.ubuntu.com/ubuntu noble/main amd64 Packages\n"},
		"apt-cache show --no-all-versions htop": {Stdout: "Package: htop\nSection: utils\nInstalled-Size: 434\nDescription: interactive processes viewer\n"},
	}}
	env := shared.Env{Runner: fake, Now: time.Now, ProcRoot: t.TempDir()}
	m := New(env)
	m.paths = syspkg.Paths{RebootRequired: filepath.Join(dir, "reboot-required"), RebootPkgs: filepath.Join(dir, "reboot-required.pkgs"), AutoUpgrades: filepath.Join(dir, "tidak-ada"), History: filepath.Join(dir, "history.log"), AptDir: dir, ProcRoot: env.ProcRoot}
	m.Update(testutil.Run(m.Init())[0])
	return m, dir
}

func TestDashboard(t *testing.T) {
	m, _ := testModel(t)
	view := ansi.Strip(m.View(120, 50))
	for _, s := range []string{"Server perlu restart karena linux-image-6.8.0-45-generic", "2 update tersedia, 1 di antaranya update keamanan", "openssl", "keamanan", "tidak aktif"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	_, cmd := m.Update(testutil.Key("g"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(100, 30))
	if !strings.Contains(confirm, "sudo apt-get upgrade") || !strings.Contains(confirm, "1 keamanan") {
		t.Errorf("konfirmasi upgrade:\n%s", confirm)
	}
}

func TestCariDetailInstall(t *testing.T) {
	m, _ := testModel(t)
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "search", Answers: ask.Answers{"kata": {Text: "htop"}}}})
	s := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*searchModel)
	s.Update(testutil.Run(s.Init())[0])
	if s.results[0].Name != "htop" {
		t.Errorf("nama persis harus paling atas: %+v", s.results)
	}
	_, cmd = s.Update(testutil.Key("enter"))
	d := testutil.Run(cmd)[0].(nav.PushMsg).Screen.(*detailModel)
	d.Update(testutil.Run(d.Init())[0])
	view := ansi.Strip(d.View(100, 40))
	if !strings.Contains(view, "belum terpasang") || !strings.Contains(view, "3.3.0-4build1") || !strings.Contains(view, "interactive processes viewer") {
		t.Errorf("detail:\n%s", view)
	}
	f := actionForm("htop", d.policy)
	if f.Questions[0].Options[0].Value != "install" {
		t.Errorf("paket belum terpasang harus menawarkan install: %+v", f.Questions[0].Options)
	}
	_, cmd = d.Update(nav.ResumedMsg{Result: ask.Result{ID: "pkg-action", Answers: ask.Answers{"aksi": {Values: []string{"install"}}}}})
	if confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(100, 30)); !strings.Contains(confirm, "sudo apt-get install htop") {
		t.Errorf("konfirmasi install:\n%s", confirm)
	}
	installed := actionForm("curl", syspkg.Policy{Installed: "8.5-1", Candidate: "8.5-2"})
	if len(installed.Questions[0].Options) != 3 || installed.Questions[0].Options[0].Value != "upgrade" {
		t.Errorf("paket terpasang: %+v", installed.Questions[0].Options)
	}
}

func TestRiwayatDanPPA(t *testing.T) {
	m, dir := testModel(t)
	h := newHistory(m.env, filepath.Join(dir, "history.log"))
	if view := ansi.Strip(h.View(100, 20)); !strings.Contains(view, "apt-get install -y docker.io") || !strings.Contains(view, "install: docker.io") {
		t.Errorf("riwayat:\n%s", view)
	}
	for _, ok := range []string{"ppa:ondrej/php", "ppa:deadsnakes/ppa"} {
		if !ppaRe.MatchString(ok) {
			t.Errorf("%s harus valid", ok)
		}
	}
	for _, bad := range []string{"ondrej/php", "ppa:ondrej", "ppa:x/y; rm -rf /", "ppa:Ondrej/php"} {
		if ppaRe.MatchString(bad) {
			t.Errorf("%s harus ditolak", bad)
		}
	}
}
