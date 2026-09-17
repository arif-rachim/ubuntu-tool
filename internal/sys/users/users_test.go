package users

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

// Key uji dibuat dengan ssh-keygen; fingerprint dibandingkan dengan output ssh-keygen -lf.
const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIKc9UpRRs5y1nSnR48NQpEidNpHklXjUdLwr979+7NW budi@laptop"
const testFP = "SHA256:xq4TyxhF6rhX2jK+MQ1NzkT9moRdr8mO/T6SWQj1kG0"

func TestParseKey(t *testing.T) {
	k, err := ParseKey(testKey)
	if err != nil || k.Fingerprint != testFP || k.Comment != "budi@laptop" || k.Type != "ssh-ed25519" {
		t.Fatalf("%+v %v", k, err)
	}
	withOpts, err := ParseKey(`from="10.0.0.0/8,192.168.1.1",no-port-forwarding,command="echo hi there" ` + testKey)
	if err != nil || withOpts.Fingerprint != testFP || !strings.HasPrefix(withOpts.Options, `from="10.0.0.0/8`) {
		t.Errorf("opsi dengan spasi dalam kutip: %+v %v", withOpts, err)
	}
	for _, bad := range []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIKc9UpRRs5y1nSnR48NQp",                   // terpotong
		"ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIIKc9UpRRs5y1nSnR48NQpEidNpHklXjUdLwr979+7NW", // jenis tidak cocok
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"ssh-ed25519",
	} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("harus ditolak: %q", bad)
		}
	}
	keys := ParseAuthorizedKeys("# komentar\n\n" + testKey + "\nrusak di sini\n")
	if len(keys) != 2 || keys[0].Err != nil || keys[1].Err == nil {
		t.Errorf("authorized_keys: %+v", keys)
	}
}

func TestPasswdGroupLastlogWho(t *testing.T) {
	users := ParsePasswd("root:x:0:0:root:/root:/bin/bash\nnobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin\ndeveloper:x:1000:1000:Dev,,,:/home/developer:/bin/bash\nsvc:x:1001:1001::/srv:/usr/sbin/nologin\n")
	var human []string
	for _, u := range users {
		if Human(u) {
			human = append(human, u.Name)
		}
	}
	if !reflect.DeepEqual(human, []string{"root", "developer"}) {
		t.Errorf("akun manusia: %v", human)
	}
	groups := ParseGroup("sudo:x:27:developer,budi\ndocker:x:124:\n")
	if len(groups) != 2 || !reflect.DeepEqual(groups[0].Members, []string{"developer", "budi"}) || groups[1].Members != nil {
		t.Errorf("group: %+v", groups)
	}
	last := ParseLastlog("Username         Port     From                                       Latest\nroot                                                                **Never logged in**\ndeveloper        pts/0    203.0.113.9                                Wed Sep 16 19:37:00 +0400 2026\n")
	if _, ok := last["root"]; ok || last["developer"].Day() != 16 {
		t.Errorf("lastlog: %v", last)
	}
	who := ParseWho("developer tty2         2026-09-16 20:51 (tty2)\nbudi     pts/1        2026-09-16 21:00 (203.0.113.9)\ndeveloper seat0        Sep 16 20:51 (login screen)\nroot     tty1         Sep 16 20:55\n")
	if len(who) != 4 || who[1].From != "203.0.113.9" || who[2].From != "login screen" || who[2].Time != "Sep 16 20:51" || who[3].From != "" {
		t.Errorf("who: %+v", who)
	}
	if !ValidUsername("budi_2") || ValidUsername("Budi") || ValidUsername("1budi") || ValidUsername("a b") {
		t.Error("ValidUsername salah")
	}
}

func TestFailedLogins(t *testing.T) {
	now := time.Now()
	lines := []string{
		"Failed password for invalid user admin from 203.0.113.9 port 51234 ssh2",
		"Failed password for root from 203.0.113.9 port 51235 ssh2",
		"Invalid user test from 198.51.100.7 port 4444",
		"Accepted publickey for developer from 10.0.0.2 port 5555 ssh2",
	}
	got := ParseFailedLogins(lines, []time.Time{now, now, now, now})
	if len(got) != 2 || got[0].IP != "203.0.113.9" || got[0].Count != 2 || !reflect.DeepEqual(got[0].Users, []string{"admin", "root"}) {
		t.Errorf("%+v", got)
	}
}

func TestReadSSHDFirstMatchDanInclude(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sshd_config.d"), 0o755)
	os.WriteFile(filepath.Join(dir, "sshd_config"), []byte("Include "+dir+"/sshd_config.d/*.conf\nPasswordAuthentication no\nPort 22\nMatch User deploy\n  PasswordAuthentication yes\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sshd_config.d", "50-cloud-init.conf"), []byte("PasswordAuthentication yes\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sshd_config.d", "00-ubt.conf"), []byte("PermitRootLogin no\nMaxAuthTries 3\n"), 0o644)
	bin := filepath.Join(dir, "sshd")
	os.WriteFile(bin, nil, 0o755)

	s := ReadSSHD(filepath.Join(dir, "sshd_config"), bin)
	if !s.Installed {
		t.Error("sshd harus terdeteksi")
	}
	// 50-cloud-init.conf dibaca lewat Include sebelum baris PasswordAuthentication di file utama: nilai pertama menang.
	if s.Values["passwordauthentication"] != "yes" || !strings.HasSuffix(s.Sources["passwordauthentication"], "50-cloud-init.conf") {
		t.Errorf("first match: %q dari %q", s.Values["passwordauthentication"], s.Sources["passwordauthentication"])
	}
	if s.Values["permitrootlogin"] != "no" || s.Values["maxauthtries"] != "3" || s.Values["pubkeyauthentication"] != "yes" {
		t.Errorf("nilai: %+v", s.Values)
	}
	audit := map[string]Finding{}
	for _, f := range s.Audit() {
		audit[f.Key] = f
	}
	if audit["passwordauthentication"].OK || !audit["permitrootlogin"].OK || !audit["maxauthtries"].OK {
		t.Errorf("audit: %+v", audit)
	}
	if ReadSSHD(filepath.Join(dir, "tidak-ada"), filepath.Join(dir, "x")).Installed {
		t.Error("tanpa binary sshd tidak terpasang")
	}
}

func TestGuardAntiTerkunci(t *testing.T) {
	ctx := SafetyContext{CurrentUser: "developer", CurrentIsSudo: true}
	if err := Guard(HardeningInput{DisablePassword: true}, ctx); err == nil || !strings.Contains(err.Error(), "belum ada satu pun") {
		t.Errorf("tanpa key sama sekali harus ditolak: %v", err)
	}
	ctx.SudoUsersWithKey = []string{"budi"}
	if err := Guard(HardeningInput{DisablePassword: true}, ctx); err == nil || !strings.Contains(err.Error(), "budi") {
		t.Errorf("user sendiri tanpa key harus ditolak: %v", err)
	}
	ctx.CurrentHasKey = true
	if err := Guard(HardeningInput{DisablePassword: true, DisableRoot: true}, ctx); err != nil {
		t.Errorf("dengan key harus boleh: %v", err)
	}
	if err := Guard(HardeningInput{DisableRoot: true}, SafetyContext{CurrentUser: "tamu"}); err == nil {
		t.Error("matikan root oleh user non-sudo harus ditolak")
	}
	if err := Guard(HardeningInput{Port: "70000"}, ctx); err == nil {
		t.Error("port tidak valid harus ditolak")
	}
}

func TestHardeningPlan(t *testing.T) {
	ctx := SafetyContext{CurrentUser: "developer", CurrentHasKey: true, CurrentIsSudo: true, UFWActive: true, SocketActivated: true, CurrentPort: "22"}
	now := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	p := HardeningPlan(HardeningInput{DisablePassword: true, Port: "2222"}, ctx, map[string]string{"PermitRootLogin": "no"}, now)
	var cmds []string
	for _, s := range p.Sequence() {
		cmds = append(cmds, s.Command.Preview(false))
	}
	want := []string{
		"sudo ufw allow 2222/tcp",
		"sudo install -m 0644 /dev/stdin /etc/ssh/sshd_config.d/00-ubt.conf",
		"sudo systemctl daemon-reload",
		"sudo sshd -t",
		"sudo systemctl restart ssh.socket",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("urutan plan:\n%s", strings.Join(cmds, "\n"))
	}
	content := p.Steps[1].Stdin
	for _, s := range []string{"PasswordAuthentication no", "KbdInteractiveAuthentication no", "PermitRootLogin no", "Port 2222"} {
		if !strings.Contains(content, s) {
			t.Errorf("drop-in tidak memuat %q:\n%s", s, content)
		}
	}
	last := p.Steps[len(p.Steps)-1]
	if !strings.Contains(last.Effect, "JANGAN TUTUP SESI INI") || !strings.Contains(last.Effect, "ssh -p 2222 developer@") {
		t.Errorf("peringatan sesi: %s", last.Effect)
	}
	reload := HardeningPlan(HardeningInput{DisableRoot: true}, SafetyContext{CurrentPort: "22"}, nil, now)
	if got := reload.Steps[len(reload.Steps)-1].Preview(false); got != "sudo systemctl reload ssh" {
		t.Errorf("tanpa ganti port cukup reload: %s", got)
	}
}

func TestGuardUserChangeDanPlan(t *testing.T) {
	d := Data{Groups: map[string]Group{"sudo": {Name: "sudo", Members: []string{"developer"}}}}
	ctx := Context{CurrentUser: "developer"}
	if GuardUserChange("delete", User{Name: "developer", UID: 1000}, d, ctx) == nil {
		t.Error("hapus diri sendiri harus ditolak")
	}
	if GuardUserChange("remove-sudo", User{Name: "developer", UID: 1000}, d, Context{CurrentUser: "root"}) == nil {
		t.Error("keluarkan anggota sudo terakhir harus ditolak")
	}
	d.Groups["sudo"] = Group{Name: "sudo", Members: []string{"developer", "budi"}}
	if err := GuardUserChange("delete", User{Name: "budi", UID: 1001}, d, ctx); err != nil {
		t.Errorf("hapus user lain yang bukan admin terakhir boleh: %v", err)
	}
	if c := GroupPlan("budi", "sudo", true).Steps[0]; c.Preview(false) != "sudo usermod -aG sudo budi" || c.Risk != risk.Dangerous {
		t.Errorf("grup sudo: %+v", c)
	}
	k, _ := ParseKey(testKey)
	add := AddKeyPlan("budi", "/home/budi", k, false)
	if len(add.Steps) != 5 || add.Steps[2].Stdin != testKey+"\n" || !add.Steps[0].NeedsRoot {
		t.Errorf("tambah key user lain: %+v", add.Steps)
	}
	if self := AddKeyPlan("developer", "/home/developer", k, true); len(self.Steps) != 3 || self.Steps[0].NeedsRoot {
		t.Errorf("tambah key sendiri tanpa sudo: %+v", self.Steps)
	}
	all := ParseAuthorizedKeys(testKey + "\n" + strings.Replace(testKey, "budi@laptop", "lama", 1) + "\n")
	rm := RemoveKeyPlan("developer", "/home/developer", all, 1, true, time.Now())
	if rm.Steps[1].Stdin != testKey+"\n" {
		t.Errorf("hapus key: %q", rm.Steps[1].Stdin)
	}
}

func TestCheckPermissions(t *testing.T) {
	home := t.TempDir()
	os.Chmod(home, 0o775)
	os.MkdirAll(filepath.Join(home, ".ssh"), 0o755)
	os.Chmod(filepath.Join(home, ".ssh"), 0o755)
	os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys"), []byte(testKey), 0o666)
	os.Chmod(filepath.Join(home, ".ssh", "authorized_keys"), 0o666)
	probs := CheckPermissions(home, os.Getuid())
	if len(probs) != 3 {
		t.Errorf("3 masalah izin: %+v", probs)
	}
	keys, err := ReadAuthorizedKeys(home)
	if err != nil || len(keys) != 1 {
		t.Errorf("baca authorized_keys: %v %v", keys, err)
	}
}
