package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/diagnose"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	diagscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/diagnose"
	diskscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/disk"
	dockerscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/docker"
	fwscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/history"
	logscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/network"
	pkgscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/packages"
	portscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/ports"
	resscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/resource"
	schedscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/schedule"
	svcscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/services"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	userscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/users"
	webscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/web"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// recorder mencatat setiap command yang diminta untuk dibaca, tanpa menjalankannya.
type recorder struct {
	mu    sync.Mutex
	calls []run.Command
	// succeed: kembalikan output kosong yang "berhasil" supaya kode melanjutkan ke pembacaan berikutnya.
	succeed bool
}

func (r *recorder) Capture(_ context.Context, c run.Command) (string, string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
	if r.succeed {
		return "", "", nil
	}
	return "", "tidak tersedia di test", errors.New("exit status 1")
}

func (r *recorder) snapshot() []run.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]run.Command(nil), r.calls...)
}

func sub(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func anyPrefix(args []string, prefixes ...string) bool {
	for _, a := range args {
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
	}
	return false
}

func oneOf(s string, set ...string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// readOnly memutuskan apakah command hanya membaca. Daftar ini sengaja ketat: command baru yang tidak
// dikenal membuat test gagal sampai ditinjau dan ditambahkan.
func readOnly(argv []string) bool {
	p, args := filepath.Base(argv[0]), argv[1:]
	switch p {
	case "systemctl":
		return oneOf(sub(args), "list-units", "list-unit-files", "list-timers", "list-sockets", "show", "status", "is-active", "is-enabled", "is-failed", "cat")
	case "journalctl":
		return !anyPrefix(args, "--vacuum", "--rotate", "--flush", "--sync", "--relinquish", "--smart-relinquish", "--setup-keys", "--update-catalog")
	case "docker":
		s := sub(args)
		return oneOf(s, "info", "ps", "images", "version", "logs", "inspect") ||
			(s == "system" && len(args) > 1 && args[1] == "df") || (s == "compose" && len(args) > 1 && args[1] == "version")
	case "apt":
		return oneOf(sub(args), "list", "policy", "show", "search")
	case "apt-cache", "dpkg-query", "apt-mark":
		return p != "apt-mark" || oneOf(sub(args), "showauto", "showmanual", "showhold")
	case "apt-get":
		return anyPrefix(args, "-s", "--simulate", "--dry-run")
	case "dpkg":
		return anyPrefix(args, "-l", "--list", "-s", "--status", "-L", "--listfiles", "--audit", "-C", "--get-selections", "-S", "--search", "--print-architecture")
	case "ip":
		for _, a := range args {
			if oneOf(a, "add", "del", "delete", "set", "flush", "change", "replace", "append") {
				return false
			}
		}
		return true
	case "resolvectl":
		return sub(args) == "" || oneOf(sub(args), "status", "dns", "domain", "query", "statistics")
	case "ufw":
		return oneOf(sub(args), "status", "show", "version", "app")
	case "sshd":
		return anyPrefix(args, "-T", "-t", "-G")
	case "nginx":
		return anyPrefix(args, "-t", "-T", "-v", "-V")
	case "certbot":
		return oneOf(sub(args), "certificates")
	case "crontab":
		return anyPrefix(args, "-l")
	case "swapon":
		return anyPrefix(args, "--show", "-s", "--summary")
	case "snap":
		return oneOf(sub(args), "list", "info", "changes")
	case "systemd-analyze":
		return oneOf(sub(args), "calendar", "verify", "timespan", "timestamp")
	case "loginctl":
		return strings.HasPrefix(sub(args), "list-") || strings.HasPrefix(sub(args), "show-")
	case "ss", "getent", "who", "w", "last", "lastb", "lastlog", "id", "groups", "lsblk", "findmnt", "df", "du",
		"uptime", "free", "nproc", "uname", "hostname", "cat", "head", "tail", "ls", "stat", "readlink", "lsof", "ps",
		"dig", "host", "nslookup", "tracepath", "ping", "hostnamectl", "timedatectl", "needrestart", "ssh-keygen":
		// ssh-keygen hanya dianggap membaca bila -l (fingerprint); needrestart hanya -b (batch, daftar).
		switch p {
		case "ssh-keygen":
			return anyPrefix(args, "-l")
		case "needrestart":
			return anyPrefix(args, "-b")
		case "hostnamectl", "timedatectl":
			return oneOf(sub(args), "", "status", "show")
		}
		return true
	}
	return false
}

func TestDaftarReadOnlyTidakTerlaluLonggar(t *testing.T) {
	mutating := [][]string{
		{"systemctl", "stop", "nginx"}, {"systemctl", "--user", "restart", "x"}, {"journalctl", "--vacuum-size=1M"},
		{"docker", "rm", "x"}, {"docker", "system", "prune"}, {"apt", "install", "x"}, {"apt-get", "install", "x"},
		{"dpkg", "-i", "x.deb"}, {"ip", "addr", "add", "1.2.3.4/24", "dev", "eth0"}, {"ufw", "allow", "22"},
		{"crontab", "-r"}, {"swapon", "/swapfile"}, {"snap", "remove", "x"}, {"ssh-keygen", "-t", "ed25519"},
		{"rm", "-f", "x"}, {"kill", "1"}, {"tee", "x"}, {"certbot", "renew"},
	}
	for _, argv := range mutating {
		if readOnly(argv) {
			t.Errorf("%v dianggap read-only", argv)
		}
	}
}

// drive menjalankan Init sebuah layar, meneruskan pesan hasilnya, lalu menjalankan command lanjutan
// beberapa tingkat (mis. daftar service → detail). Tick dan command yang lama diabaikan.
func drive(s nav.Screen, depth int) {
	queue := []tea.Cmd{s.Init()}
	for level := 0; level < depth && len(queue) > 0; level++ {
		var next []tea.Cmd
		for _, cmd := range queue {
			for _, msg := range collect(cmd, 3*time.Second) {
				switch msg.(type) {
				case nav.PushMsg, nav.PopMsg, nav.ReplaceMsg:
					continue // layar lain (mis. layar konfirmasi) tidak dibuka dalam test ini
				}
				var c tea.Cmd
				s, c = s.Update(msg)
				if c != nil {
					next = append(next, c)
				}
			}
		}
		queue = next
	}
	s.View(120, 40)
}

func collect(cmd tea.Cmd, timeout time.Duration) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, collect(c, timeout)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(timeout):
		return nil
	}
}

func assertReadOnly(t *testing.T, where string, calls []run.Command) {
	t.Helper()
	if os.Getenv("UBT_SAFETY_DEBUG") != "" {
		for _, c := range calls {
			fmt.Println("DBG", where, "|", c.Preview(false))
		}
	}
	for _, c := range calls {
		if !readOnly(c.Argv) {
			t.Errorf("%s menjalankan command yang tidak read-only tanpa konfirmasi: %s", where, c.Preview(false))
		}
	}
}

// Membuka layar mana pun hanya membaca kondisi server. Perubahan hanya boleh terjadi setelah user
// menyetujui layar konfirmasi (yang di test ini tidak pernah dibuka).
func TestMembukaLayarHanyaMembaca(t *testing.T) {
	if testing.Short() {
		t.Skip("membaca sistem sungguhan")
	}
	for _, succeed := range []bool{false, true} {
		rec := &recorder{succeed: succeed}
		env := shared.Env{Runner: rec, ProcRoot: "/proc", UID: 1000, Now: time.Now, Deps: runflow.Deps{}}
		open := func(string) nav.Screen { return nil }
		screens := map[string]nav.Screen{
			"ports":           portscreen.New(env),
			"resource":        resscreen.New(env),
			"disk":            diskscreen.New(env),
			"logs":            logscreen.New(env),
			"logs.entries":    logscreen.NewEntries(env, "Error", syslogs.Query{Boot: syslogs.BootPtr(0), Priority: 3}),
			"services":        svcscreen.New(env),
			"services.detail": svcscreen.NewDetail(env, "ssh.service", false),
			"packages":        pkgscreen.New(env),
			"schedule":        schedscreen.New(env),
			"users":           userscreen.New(env),
			"firewall":        fwscreen.New(env),
			"network":         network.New(env, open),
			"web":             webscreen.New(env),
			"docker":          dockerscreen.New(env),
			"history":         history.New(env, &run.History{Path: filepath.Join(t.TempDir(), "h.log")}),
			"diagnose (menu)": diagscreen.New(diagnose.Env{Runner: rec}, nil, nil),
		}
		for name, s := range screens {
			before := len(rec.snapshot())
			drive(s, 3)
			calls := rec.snapshot()[before:]
			assertReadOnly(t, fmt.Sprintf("layar %s (runner berhasil=%v)", name, succeed), calls)
		}
		if len(rec.snapshot()) < 10 {
			t.Errorf("hanya %d command tercatat; layar mungkin tidak benar-benar memuat data", len(rec.snapshot()))
		}
	}
}

// Diagnosa dijanjikan "hanya membaca": semua langkah di semua gejala, dan ringkasan di menu utama.
func TestDiagnosaHanyaMembaca(t *testing.T) {
	if testing.Short() {
		t.Skip("membaca sistem sungguhan")
	}
	for _, succeed := range []bool{false, true} {
		rec := &recorder{succeed: succeed}
		env := diagnose.DefaultEnv(rec, "/proc", 1000, time.Now)
		for _, sym := range diagnose.Symptoms() {
			if sym.Steps == nil {
				continue
			}
			before := len(rec.snapshot())
			for _, step := range sym.Steps(env) {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				step.Run(ctx)
				cancel()
			}
			assertReadOnly(t, "diagnosa "+sym.ID, rec.snapshot()[before:])
		}
		before := len(rec.snapshot())
		diagnose.Quick(context.Background(), env)
		assertReadOnly(t, "ringkasan menu utama", rec.snapshot()[before:])
	}
}
