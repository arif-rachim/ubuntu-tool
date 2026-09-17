package users

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysusers "github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIKc9UpRRs5y1nSnR48NQpEidNpHklXjUdLwr979+7NW budi@laptop"

func option(f ask.Form, value string) (ask.Option, bool) {
	for _, o := range f.Questions[0].Options {
		if o.Value == value {
			return o, true
		}
	}
	return ask.Option{}, false
}

func TestHardeningFormPengaman(t *testing.T) {
	sshd := sysusers.SSHD{Installed: true, Values: map[string]string{"passwordauthentication": "yes", "permitrootlogin": "yes", "maxauthtries": "6", "port": "22"}}
	noKey := sysusers.SafetyContext{CurrentUser: "developer", CurrentIsSudo: true}
	f := HardeningForm(sshd, noKey)
	pw, _ := option(f, "password")
	if pw.Disabled == "" || pw.Recommended {
		t.Errorf("matikan password tanpa key harus disabled: %+v", pw)
	}
	root, _ := option(f, "root")
	if root.Disabled != "" || !root.Recommended {
		t.Errorf("larang root boleh: %+v", root)
	}
	withKey := noKey
	withKey.CurrentHasKey = true
	if pw, _ := option(HardeningForm(sshd, withKey), "password"); pw.Disabled != "" {
		t.Errorf("dengan key harus boleh: %+v", pw)
	}
	in := HardeningFromAnswers(ask.Answers{"changes": {Values: []string{"password", "maxauth", "port"}}, "port": {Text: " 2222 "}})
	if !in.DisablePassword || in.DisableRoot || in.MaxAuthTries != 3 || in.Port != "2222" {
		t.Errorf("%+v", in)
	}
}

func TestDetailPengamanDanKey(t *testing.T) {
	home := t.TempDir()
	d := sysusers.Data{Groups: map[string]sysusers.Group{"sudo": {Name: "sudo", Members: []string{"developer"}}, "docker": {Name: "docker"}}}
	u := sysusers.User{Name: "developer", UID: 1000, Home: home, Shell: "/bin/bash", Groups: []string{"developer", "sudo"}}
	m := newDetail(shared.Env{Now: time.Now}, u, d, "developer")
	f := m.ActionForm()
	for _, v := range []string{"delete", "expire", "lock", "remove-sudo"} {
		if o, ok := option(f, v); !ok || o.Disabled == "" {
			t.Errorf("%s pada diri sendiri/admin terakhir harus disabled: %+v", v, o)
		}
	}
	if o, _ := option(f, "add-key"); !o.Recommended {
		t.Error("user tanpa key: tambah key harus disarankan")
	}

	cmd := m.onAnswer(ask.Result{Answers: ask.Answers{"aksi": {Values: []string{"add-key"}}}})
	if cmd == nil {
		t.Fatal("harus membuka form paste key")
	}
	// Validasi paste key: private key ditolak, public key valid diterima, key duplikat ditolak.
	validate := keyValidator(m)
	if err := validate("-----BEGIN OPENSSH PRIVATE KEY-----\nabc"); err == nil || !strings.Contains(err.Error(), "PRIVATE") {
		t.Errorf("private key harus ditolak: %v", err)
	}
	if err := validate(testKey); err != nil {
		t.Errorf("public key valid: %v", err)
	}
	k, _ := sysusers.ParseKey(testKey)
	m.keys = []sysusers.Key{k}
	if err := validate(testKey); err == nil {
		t.Error("key duplikat harus ditolak")
	}
	if !strings.Contains(ansi.Strip(m.View(120, 40)), "budi@laptop") {
		t.Error("key harus tampil di detail")
	}
}
