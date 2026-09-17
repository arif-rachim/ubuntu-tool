package safety

import (
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/netinfo"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/web"
)

// hostile adalah input yang tidak boleh lolos validator mana pun yang hasilnya dipakai sebagai argumen
// command: metakarakter shell, spasi/baris baru, dan awalan "-" yang akan dibaca sebagai opsi.
var hostile = []string{
	"; rm -rf /", "a;b", "a&&b", "a|b", "$(id)", "`id`", "a b", "a\nb", "a\tb", "a'b", `a"b`, "a>b", "a<b",
	"-rf", "--help", "-oProxyCommand=id", "--output=/etc/passwd", "../../etc/passwd", "a\x00b",
}

func TestValidatorMenolakInputBerbahaya(t *testing.T) {
	validators := map[string]func(string) error{
		"web.ValidateDomain":        web.ValidateDomain,
		"docker.ValidImage":         docker.ValidImage,
		"docker.ValidName":          docker.ValidName,
		"docker.ValidModule":        docker.ValidModule,
		"docker.ValidPortMapping":   docker.ValidPortMapping,
		"packages.ValidName":        packages.ValidName,
		"firewall.ValidatePort":     firewall.ValidatePort,
		"firewall.ValidateSource":   firewall.ValidateSource,
		"users.ValidUsername":       func(s string) error { return boolErr(users.ValidUsername(s)) },
		"schedule.SlugName":         func(s string) error { _, err := schedule.SlugName(s); return err },
		"netinfo.ParseTarget(host)": func(s string) error { _, err := netinfo.ParseTarget(s); return err },
		"netinfo.ValidHost":         netinfo.ValidHost,
	}
	for name, v := range validators {
		for _, in := range hostile {
			if name == "schedule.SlugName" {
				// SlugName membersihkan, bukan menolak: hasilnya harus aman.
				if slug, err := schedule.SlugName(in); err == nil && !safeToken(slug) {
					t.Errorf("%s(%q) menghasilkan %q yang tidak aman", name, in, slug)
				}
				continue
			}
			if v(in) == nil {
				t.Errorf("%s menerima input berbahaya %q", name, in)
			}
		}
	}
}

// Input yang wajar tetap diterima, supaya validator tidak memaksa user mengakali batasan.
func TestValidatorMenerimaInputWajar(t *testing.T) {
	ok := map[string][]string{
		"web.ValidateDomain":      {"contoh.com", "app.contoh.co.id"},
		"docker.ValidImage":       {"nginx", "python:3.12-slim", "ghcr.io/org/app:1.0"},
		"docker.ValidName":        {"db", "my_app-1"},
		"packages.ValidName":      {"nginx", "libc6", "g++", "python3.12-venv"},
		"firewall.ValidatePort":   {"22", "6000:6007"},
		"firewall.ValidateSource": {"203.0.113.5", "10.0.0.0/8"},
		"netinfo.ValidHost":       {"contoh.com", "localhost", "1.1.1.1", "2606:4700::1111", "[::1]", "_dmarc.contoh.com"},
	}
	fns := map[string]func(string) error{
		"web.ValidateDomain": web.ValidateDomain, "docker.ValidImage": docker.ValidImage, "docker.ValidName": docker.ValidName,
		"packages.ValidName": packages.ValidName, "firewall.ValidatePort": firewall.ValidatePort, "firewall.ValidateSource": firewall.ValidateSource,
		"netinfo.ValidHost": netinfo.ValidHost,
	}
	for name, inputs := range ok {
		for _, in := range inputs {
			if err := fns[name](in); err != nil {
				t.Errorf("%s menolak input wajar %q: %v", name, in, err)
			}
		}
	}
	if !users.ValidUsername("budi") || !users.ValidUsername("deploy_1") {
		t.Error("username wajar ditolak")
	}
}

func boolErr(ok bool) error {
	if ok {
		return nil
	}
	return errInvalid
}

type invalid struct{}

func (invalid) Error() string { return "tidak valid" }

var errInvalid error = invalid{}

func safeToken(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, " \t\n;&|$`'\"<>()/\\\x00")
}
