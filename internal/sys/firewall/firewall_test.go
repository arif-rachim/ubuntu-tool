package firewall

import (
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

const numbered = `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 22/tcp                     LIMIT IN    Anywhere
[ 2] Nginx Full                 ALLOW IN    Anywhere                   # web
[ 3] 5432/tcp                   ALLOW IN    10.0.0.0/8
[ 4] 6000:6007/tcp              DENY IN     Anywhere
[ 5] 22/tcp (v6)                LIMIT IN    Anywhere (v6)
[ 6] 53                         ALLOW OUT   Anywhere
`

func TestParseNumbered(t *testing.T) {
	active, rules := ParseNumbered(numbered)
	if !active || len(rules) != 6 {
		t.Fatalf("active=%v %d aturan: %+v", active, len(rules), rules)
	}
	if r := rules[1]; r.To != "Nginx Full" || r.Action != "ALLOW" || r.Comment != "web" || r.From != "Anywhere" {
		t.Errorf("aturan 2: %+v", r)
	}
	if r := rules[4]; !r.V6 || r.To != "22/tcp" || r.From != "Anywhere" {
		t.Errorf("aturan v6: %+v", r)
	}
	if r := rules[5]; r.Dir != "OUT" || r.To != "53" {
		t.Errorf("aturan OUT: %+v", r)
	}
	inactive, none := ParseNumbered("Status: inactive\n")
	if inactive || len(none) != 0 {
		t.Error("inactive")
	}
	d, l := ParseVerbose("Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n")
	if d != "deny (incoming), allow (outgoing), disabled (routed)" || l != "on (low)" {
		t.Errorf("%q %q", d, l)
	}
	if apps := ParseApps("Available applications:\n  Nginx Full\n  OpenSSH\n"); len(apps) != 2 || apps[0] != "Nginx Full" {
		t.Errorf("apps: %v", apps)
	}
}

func TestAllows(t *testing.T) {
	_, rules := ParseNumbered(numbered)
	checks := []struct {
		rule int
		port int
		want bool
	}{
		{0, 22, true},  // LIMIT dihitung mengizinkan
		{1, 443, true}, // profil Nginx Full
		{1, 22, false},
		{2, 5432, false}, // hanya dari 10.0.0.0/8
		{3, 6003, false}, // DENY
		{5, 53, false},   // OUT
	}
	for _, c := range checks {
		if got := rules[c.rule].Allows(c.port); got != c.want {
			t.Errorf("aturan %d port %d = %v, ingin %v", c.rule+1, c.port, got, c.want)
		}
	}
}

func TestLockoutDanEnable(t *testing.T) {
	_, rules := ParseNumbered(numbered)
	s := Status{Installed: true, Readable: true, Rules: rules}
	ssh := SSHContext{Port: 22, InSession: true}
	if w := Lockout(s, ssh, nil); w != "" {
		t.Errorf("ada aturan SSH: tidak berisiko, dapat %q", w)
	}
	// Menghapus aturan 1 masih aman karena aturan 5 (v6) juga mengizinkan 22; hapus keduanya → berisiko.
	s2 := Status{Rules: []Rule{rules[0], rules[1]}}
	if w := Lockout(s2, ssh, &rules[0]); !strings.Contains(w, "sesi SSH kamu") {
		t.Errorf("hapus satu-satunya aturan SSH harus berisiko: %q", w)
	}

	p := EnablePlan(Status{Installed: true}, SSHContext{Port: 2222, SSHRunning: true})
	if len(p.Steps) != 2 || p.Steps[0].Preview(false) != "sudo ufw limit 2222/tcp comment 'SSH (ditambahkan ubt)'" || p.Steps[1].Preview(false) != "sudo ufw --force enable" {
		t.Errorf("enable tanpa aturan SSH harus menambah limit dulu: %+v", p.Steps)
	}
	if p := EnablePlan(s, ssh); len(p.Steps) != 1 {
		t.Errorf("enable dengan aturan SSH cukup 1 langkah: %d", len(p.Steps))
	}
	if p := EnablePlan(Status{}, SSHContext{}); len(p.Steps) != 1 {
		t.Error("tanpa SSH tidak perlu aturan tambahan")
	}
}

func TestRulePlanDanValidasi(t *testing.T) {
	cases := []struct {
		action string
		t      Target
		want   string
	}{
		{"allow", Target{Port: "8080", Proto: "tcp"}, "sudo ufw allow 8080/tcp"},
		{"allow", Target{Port: "6000:6007"}, "sudo ufw allow 6000:6007/tcp"},
		{"allow", Target{Port: "5432", Proto: "tcp", From: "10.0.0.0/8"}, "sudo ufw allow from 10.0.0.0/8 to any port 5432 proto tcp"},
		{"allow", Target{App: "Nginx Full"}, "sudo ufw allow 'Nginx Full'"},
		{"deny", Target{Port: "3306"}, "sudo ufw deny 3306"},
		{"limit", Target{Port: "22", Proto: "tcp"}, "sudo ufw limit 22/tcp"},
	}
	for _, c := range cases {
		if got := RulePlan(c.action, c.t, "").Steps[0].Preview(false); got != c.want {
			t.Errorf("%s %+v = %s, ingin %s", c.action, c.t, got, c.want)
		}
	}
	if got := RulePlan("allow", Target{Port: "3000", Proto: "tcp"}, "app node").Steps[0].Preview(false); got != "sudo ufw allow 3000/tcp comment 'app node'" {
		t.Errorf("comment: %s", got)
	}
	for _, ok := range []string{"22", "6000:6007"} {
		if ValidatePort(ok) != nil {
			t.Errorf("%s harus valid", ok)
		}
	}
	for _, bad := range []string{"0", "70000", "7:3", "1:2:3", "ssh"} {
		if ValidatePort(bad) == nil {
			t.Errorf("%s harus ditolak", bad)
		}
	}
	if ValidateSource("10.0.0.0/8") != nil || ValidateSource("203.0.113.9") != nil || ValidateSource("") != nil || ValidateSource("kantor") == nil {
		t.Error("ValidateSource salah")
	}
	_, rules := ParseNumbered(numbered)
	if d := DeletePlan(rules[0], "berisiko"); d.Steps[0].Risk != risk.Dangerous || d.Steps[0].Preview(false) != "sudo ufw --force delete 1" {
		t.Errorf("delete: %+v", d.Steps[0])
	}
}
