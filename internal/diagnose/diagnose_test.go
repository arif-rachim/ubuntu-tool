package diagnose

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
)

var now = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func runAll(steps []check.Step) []check.Result {
	var out []check.Result
	for _, s := range steps {
		out = append(out, s.Run(context.Background()))
	}
	return out
}

func testEnv(t *testing.T) Env {
	dir := t.TempDir()
	return Env{
		Runner: &run.Fake{}, ProcRoot: filepath.Join(dir, "proc"), Now: func() time.Time { return now }, Home: dir, Username: "budi",
		Packages:   packages.Paths{RebootRequired: filepath.Join(dir, "reboot-required"), RebootPkgs: filepath.Join(dir, "reboot-required.pkgs"), History: filepath.Join(dir, "history.log")},
		SSHDBinary: filepath.Join(dir, "sshd-tidak-ada"), SSHDConfig: filepath.Join(dir, "sshd_config"), CertLive: filepath.Join(dir, "live"),
	}
}

func TestLambat(t *testing.T) {
	env := testEnv(t)
	calls := 0
	env.Sample = func(context.Context) (resource.Snapshot, error) {
		calls++
		s := resource.Snapshot{Cores: 2, CPUBusy: 99}
		s.Load.One = 6
		s.Memory = resource.Memory{TotalKB: 8 << 20, AvailableKB: 6 << 20, SwapTotalKB: 1 << 20, SwapFreeKB: 1 << 20}
		s.Procs = []resource.Proc{{Name: "php-fpm", CPU: 97, RSSKB: 1024}, {Name: "mysqld", CPU: 40, RSSKB: 4096}}
		return s, nil
	}
	env.Runner = &run.Fake{Responses: map[string]run.FakeResponse{
		run.JoinShell(resource.OOMCommand().Argv): {Stdout: "2026-09-16T10:00:00+0000 host kernel: Out of memory: Killed process 1234 (java) total-vm:1kB\n"},
	}}
	res := runAll(SlowSteps(env))
	if calls != 1 {
		t.Errorf("sampel harus diambil sekali untuk semua langkah, dapat %d", calls)
	}
	if res[0].Status != check.Fail || res[0].Next != "resource" || !strings.Contains(res[0].Summary, "load 6.00") {
		t.Errorf("load: %+v", res[0])
	}
	if res[1].Status != check.OK {
		t.Errorf("RAM lega harus OK: %+v", res[1])
	}
	if res[3].Status != check.Warn || !strings.Contains(res[3].Summary, "php-fpm 97%") || !strings.Contains(res[3].Summary, "mysqld") {
		t.Errorf("proses: %+v", res[3])
	}
	if res[4].Status != check.Fail || !strings.Contains(res[4].Summary, "java") {
		t.Errorf("oom: %+v", res[4])
	}
}

func TestSSHTanpaServerDanReboot(t *testing.T) {
	env := testEnv(t)
	env.UID = os.Getuid()
	os.Chmod(env.Home, 0o755)
	res := runAll(SSHSteps(env))
	if res[0].Status != check.Fail || res[0].Next != "packages" || res[1].Status != check.Skip || res[3].Status != check.Skip {
		t.Errorf("%+v", res)
	}
	if res[2].Status != check.Warn || !strings.Contains(res[2].Summary, "authorized_keys") {
		t.Errorf("tanpa key: %+v", res[2])
	}

	if r := stepReboot(env).Run(context.Background()); r.Status != check.OK {
		t.Errorf("%+v", r)
	}
	os.WriteFile(env.Packages.RebootRequired, nil, 0o644)
	os.WriteFile(env.Packages.RebootPkgs, []byte("linux-image-6.8\nlibc6\n"), 0o644)
	if r := stepReboot(env).Run(context.Background()); r.Status != check.Warn || !strings.Contains(r.Explain, "linux-image-6.8, libc6") {
		t.Errorf("%+v", r)
	}
}

func TestServiceGagal(t *testing.T) {
	env := testEnv(t)
	env.Runner = &run.Fake{Responses: map[string]run.FakeResponse{
		"systemctl list-units --type=service --all --no-pager --output=json": {Stdout: `[{"unit":"app.service","load":"loaded","active":"failed","sub":"failed","description":"App"},{"unit":"cron.service","load":"loaded","active":"active","sub":"running","description":"Cron"}]`},
	}}
	r := stepFailedUnits(env).Run(context.Background())
	if r.Status != check.Fail || !strings.Contains(r.Summary, "1 unit gagal: app") || r.Next != "services" {
		t.Errorf("%+v", r)
	}
}

func writeCert(t *testing.T, path string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "contoh.com"}, DNSNames: []string{"contoh.com"},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func TestSertifikat(t *testing.T) {
	env := testEnv(t)
	if r := certResult(env); r.Status != check.Skip {
		t.Errorf("tanpa folder: %+v", r)
	}
	writeCert(t, filepath.Join(env.CertLive, "aman.com", "cert.pem"), now.Add(60*24*time.Hour))
	if r := certResult(env); r.Status != check.OK {
		t.Errorf("%+v", r)
	}
	writeCert(t, filepath.Join(env.CertLive, "mepet.com", "cert.pem"), now.Add(5*24*time.Hour))
	if r := certResult(env); r.Status != check.Warn || !strings.Contains(r.Summary, "mepet.com (4 hari)") && !strings.Contains(r.Summary, "mepet.com (5 hari)") {
		t.Errorf("%+v", r)
	}
	writeCert(t, filepath.Join(env.CertLive, "lewat.com", "cert.pem"), now.Add(-24*time.Hour))
	if r := certResult(env); r.Status != check.Fail || r.Next != "web" {
		t.Errorf("%+v", r)
	}
}

func TestSymptoms(t *testing.T) {
	env := testEnv(t)
	for _, s := range Symptoms() {
		if (s.Open == "") == (s.Steps == nil) {
			t.Errorf("%s harus punya tepat satu dari Open atau Steps", s.ID)
		}
		if s.Steps != nil && len(s.Steps(env)) == 0 {
			t.Errorf("%s tanpa langkah", s.ID)
		}
	}
}
