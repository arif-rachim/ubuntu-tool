package packages

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseInstalled(t *testing.T) {
	pkgs := ParseInstalled("acl\t2.3.2\t192\tii \nlibreoffice-core\t1:24.2\t105000\tii \nold-config\t1.0\t\trc \n")
	if len(pkgs) != 3 || pkgs[0].Name != "libreoffice-core" || pkgs[2].Status != "rc" {
		t.Errorf("%+v", pkgs)
	}
}

func TestParseUpgradable(t *testing.T) {
	out := `Listing...
krb5-locales/noble-updates 1.20.1-6ubuntu2.10 all [upgradable from: 1.20.1-6ubuntu2.8]
openssl/noble-updates,noble-security 3.0.13-0ubuntu3.5 amd64 [upgradable from: 3.0.13-0ubuntu3.4]
`
	ups := ParseUpgradable(out)
	if len(ups) != 2 || ups[0].Name != "openssl" || !ups[0].Security || ups[1].Security || ups[1].Old != "1.20.1-6ubuntu2.8" {
		t.Errorf("keamanan harus di atas: %+v", ups)
	}
}

func TestParsePolicyDanSearch(t *testing.T) {
	p := ParsePolicy(`nginx:
  Installed: (none)
  Candidate: 1.24.0-2ubuntu7.18
  Version table:
     1.24.0-2ubuntu7.18 500
        500 http://archive.ubuntu.com/ubuntu noble-updates/main amd64 Packages
        500 http://security.ubuntu.com/ubuntu noble-security/main amd64 Packages
`)
	if p.Name != "nginx" || p.Installed != "" || p.Candidate != "1.24.0-2ubuntu7.18" || len(p.Sources) != 2 {
		t.Errorf("%+v", p)
	}
	res := ParseSearch("htop - interactive processes viewer\nbtop - Modern and colorful command line resource monitor - with extras\n")
	if len(res) != 2 || res[1].Description != "Modern and colorful command line resource monitor - with extras" {
		t.Errorf("%+v", res)
	}
}

func TestParseHistory(t *testing.T) {
	h := ParseHistory(`
Start-Date: 2026-09-16  20:35:36
Commandline: aptdaemon role='role-commit-packages' sender=':1.203'
Upgrade: libpolkit-agent-1-0:amd64 (124-2ubuntu1.24.04.3, 124-2ubuntu1.24.04.4), perl:amd64 (5.38.2-3.2ubuntu0.4, 5.38.2-3.2ubuntu0.6)
End-Date: 2026-09-16  20:35:42

Start-Date: 2026-09-16  20:49:19
Commandline: apt-get install -y docker.io
Requested-By: developer (1000)
Install: bridge-utils:amd64 (1.7.1-1ubuntu2, automatic), docker.io:amd64 (29.1.3-0ubuntu3~24.04.2)
End-Date: 2026-09-16  20:49:27
`)
	if len(h) != 2 || h[0].Commandline != "apt-get install -y docker.io" || h[0].RequestedBy != "developer (1000)" {
		t.Fatalf("terbaru dulu: %+v", h)
	}
	if !reflect.DeepEqual(h[0].Install, []string{"bridge-utils", "docker.io"}) || !reflect.DeepEqual(h[1].Upgrade, []string{"libpolkit-agent-1-0", "perl"}) {
		t.Errorf("nama paket: %v / %v", h[0].Install, h[1].Upgrade)
	}
	if h[0].Start.IsZero() || h[0].Summary() != "install 2" {
		t.Errorf("start/summary: %v %q", h[0].Start, h[0].Summary())
	}
}

func TestReadSources(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sources.list.d"), 0o755)
	os.WriteFile(filepath.Join(dir, "sources.list.d", "ubuntu.sources"), []byte(`Types: deb
URIs: http://archive.ubuntu.com/ubuntu/
Suites: noble noble-updates
Components: main

Types: deb
URIs: http://security.ubuntu.com/ubuntu/
Suites: noble-security
Enabled: no
`), 0o644)
	os.WriteFile(filepath.Join(dir, "sources.list.d", "chrome.list"), []byte("deb [arch=amd64 signed-by=/k.gpg] https://dl.google.com/linux/chrome/deb/ stable main\n# deb-src http://x/ y z\n"), 0o644)
	src := ReadSources(dir)
	if len(src) != 4 {
		t.Fatalf("%d sumber: %+v", len(src), src)
	}
	var enabled []string
	for _, s := range src {
		enabled = append(enabled, s.URIs+"="+map[bool]string{true: "on", false: "off"}[s.Enabled])
	}
	want := "https://dl.google.com/linux/chrome/deb/=on,http://x/=off,http://archive.ubuntu.com/ubuntu/=on,http://security.ubuntu.com/ubuntu/=off"
	if strings.Join(enabled, ",") != want {
		t.Errorf("%s", strings.Join(enabled, ","))
	}
}

func TestAutoUpgradeDanNama(t *testing.T) {
	a := ParseAutoUpgrade("APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"0\";\n")
	if !a.Configured || !a.UpdateList || a.Upgrade {
		t.Errorf("%+v", a)
	}
	for _, ok := range []string{"nginx", "libc6", "g++", "python3.12"} {
		if ValidName(ok) != nil {
			t.Errorf("%s harus valid", ok)
		}
	}
	for _, bad := range []string{"Nginx", "a", "nginx;rm", "-nginx", "a b"} {
		if ValidName(bad) == nil {
			t.Errorf("%q harus ditolak", bad)
		}
	}
}

func TestPlans(t *testing.T) {
	if c := RemovePlan("nginx", true).Steps[0]; c.Preview(false) != "sudo apt-get purge nginx" || c.Risk != 2 || !c.Interactive {
		t.Errorf("purge: %+v", c)
	}
	if c := UpgradePlan(9, 2).Steps[0]; !c.Interactive || !strings.Contains(c.Title, "2 keamanan") {
		t.Errorf("upgrade: %+v", c)
	}
	if c := AddPPAPlan("ppa:ondrej/php").Steps[0]; c.Risk != 2 {
		t.Error("PPA harus berbahaya")
	}
	if len(FixBrokenPlan().Steps) != 2 {
		t.Error("fix broken 2 langkah")
	}
}

func TestFindAptProcess(t *testing.T) {
	root := t.TempDir()
	mk := func(pid, comm, cmdline string) {
		dir := filepath.Join(root, pid)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "status"), []byte("Name:\t"+comm+"\nUid:\t0\t0\t0\t0\n"), 0o644)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.ReplaceAll(cmdline, " ", "\x00")), 0o644)
	}
	mk("1259", "unattended-upgr", "/usr/bin/python3 /usr/share/unattended-upgrades/unattended-upgrade-shutdown --wait-for-signal")
	if p := FindAptProcess(root); p != nil {
		t.Fatalf("unattended-upgrade-shutdown bukan pemegang lock: %+v", p)
	}
	mk("4000", "apt-get", "apt-get install nginx")
	if p := FindAptProcess(root); p == nil || p.PID != 4000 {
		t.Fatalf("apt-get harus terdeteksi: %+v", p)
	}
}
