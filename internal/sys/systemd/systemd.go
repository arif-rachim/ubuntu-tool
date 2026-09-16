// Package systemd membaca daftar dan detail unit systemd lewat systemctl, dan membangun command
// untuk mengelolanya. Murni pengumpul data.
package systemd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// ErrNoSystemd dikembalikan bila sistem tidak dijalankan dengan systemd (mis. container).
var ErrNoSystemd = errors.New("sistem ini tidak dijalankan dengan systemd sebagai PID 1 (umum di dalam container)")

// Unit adalah satu service.
type Unit struct {
	Name        string
	Load        string // loaded, not-found, masked
	Active      string // active, inactive, failed, activating
	Sub         string // running, exited, dead, failed, auto-restart
	Description string
	FileState   string // enabled, disabled, static, masked, indirect, generated
}

// Failed melaporkan apakah unit dalam status gagal.
func (u Unit) Failed() bool {
	return u.Active == "failed" || u.Sub == "failed" || u.Sub == "auto-restart"
}

// Enabled melaporkan apakah unit menyala otomatis saat boot.
func (u Unit) Enabled() bool { return u.FileState == "enabled" || u.FileState == "enabled-runtime" }

func scopeArgs(user bool) []string {
	if user {
		return []string{"systemctl", "--user"}
	}
	return []string{"systemctl"}
}

// ListArgs adalah command daftar service (JSON).
func ListArgs(user bool) []string {
	return append(scopeArgs(user), "list-units", "--type=service", "--all", "--no-pager", "--output=json")
}

// FilesArgs adalah command status unit file (enabled/disabled).
func FilesArgs(user bool) []string {
	return append(scopeArgs(user), "list-unit-files", "--type=service", "--no-pager", "--output=json")
}

// ParseListUnits mem-parse output JSON list-units.
func ParseListUnits(out string) ([]Unit, error) {
	var raw []struct {
		Unit, Load, Active, Sub, Description string
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return nil, err
	}
	units := make([]Unit, len(raw))
	for i, r := range raw {
		units[i] = Unit{Name: r.Unit, Load: r.Load, Active: r.Active, Sub: r.Sub, Description: r.Description}
	}
	return units, nil
}

// ParseListUnitsText mem-parse `list-units --no-legend --plain --full` sebagai cadangan
// (5 kolom; deskripsi boleh mengandung spasi).
func ParseListUnitsText(out string) []Unit {
	var units []Unit
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		u := Unit{Name: f[0], Load: f[1], Active: f[2], Sub: f[3]}
		if len(f) > 4 {
			u.Description = strings.Join(f[4:], " ")
		}
		units = append(units, u)
	}
	return units
}

// ParseUnitFiles mem-parse output JSON list-unit-files menjadi peta nama → state.
func ParseUnitFiles(out string) map[string]string {
	var raw []struct {
		UnitFile string `json:"unit_file"`
		State    string `json:"state"`
	}
	states := map[string]string{}
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &raw) == nil {
		for _, r := range raw {
			states[r.UnitFile] = r.State
		}
	}
	return states
}

func noSystemd(out string) bool {
	return strings.Contains(out, "System has not been booted with systemd") || strings.Contains(out, "Failed to connect to bus")
}

// List membaca semua service beserta status boot-nya, terurut: gagal, aktif, lalu sisanya.
func List(ctx context.Context, r run.Runner, user bool) ([]Unit, error) {
	out, stderr, err := r.Capture(ctx, run.Command{Argv: ListArgs(user)})
	if noSystemd(stderr + out) {
		return nil, ErrNoSystemd
	}
	var units []Unit
	if err == nil {
		units, err = ParseListUnits(out)
	}
	if err != nil {
		text, stderr2, err2 := r.Capture(ctx, run.Command{Argv: append(scopeArgs(user), "list-units", "--type=service", "--all", "--no-legend", "--plain", "--full", "--no-pager")})
		if err2 != nil {
			if noSystemd(stderr2) {
				return nil, ErrNoSystemd
			}
			return nil, fmt.Errorf("systemctl gagal: %s", strings.TrimSpace(stderr+stderr2))
		}
		units = ParseListUnitsText(text)
	}
	if files, _, err := r.Capture(ctx, run.Command{Argv: FilesArgs(user)}); err == nil {
		states := ParseUnitFiles(files)
		for i := range units {
			units[i].FileState = states[units[i].Name]
		}
	}
	sort.SliceStable(units, func(i, j int) bool {
		rank := func(u Unit) int {
			switch {
			case u.Failed():
				return 0
			case u.Active == "active":
				return 1
			}
			return 2
		}
		if rank(units[i]) != rank(units[j]) {
			return rank(units[i]) < rank(units[j])
		}
		return units[i].Name < units[j].Name
	})
	return units, nil
}

// ShowProperties adalah properti yang dibaca untuk detail unit.
var ShowProperties = []string{
	"Id", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState", "MainPID", "ExecStart",
	"ExecReload", "WorkingDirectory", "User", "Group", "FragmentPath", "DropInPaths", "Environment", "Restart",
	"NRestarts", "Result", "ActiveEnterTimestamp", "InactiveEnterTimestamp", "MemoryCurrent", "CPUUsageNSec",
	"TriggeredBy", "CanReload",
}

// ShowArgs adalah command detail unit.
func ShowArgs(unit string, user bool) []string {
	return append(scopeArgs(user), "show", unit, "--property="+strings.Join(ShowProperties, ","), "--no-pager")
}

// ParseShow mem-parse output `systemctl show` (Key=Value; nilai boleh kosong atau mengandung '=').
// Beberapa unit dipisahkan baris kosong.
func ParseShow(out string) []map[string]string {
	var blocks []map[string]string
	cur := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(cur) > 0 {
				blocks = append(blocks, cur)
				cur = map[string]string{}
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			cur[k] = v
		}
	}
	if len(cur) > 0 {
		blocks = append(blocks, cur)
	}
	return blocks
}

// Details adalah detail satu unit.
type Details struct {
	Props map[string]string
}

// Get mengembalikan properti, dengan "[not set]" dianggap kosong.
func (d Details) Get(k string) string {
	v := d.Props[k]
	if v == "[not set]" || v == "infinity" {
		return ""
	}
	return v
}

// Int membaca properti angka.
func (d Details) Int(k string) int64 {
	n, _ := strconv.ParseInt(d.Get(k), 10, 64)
	return n
}

// Command mengambil argv[] dari ExecStart/ExecReload yang berformat { path=… ; argv[]=… ; … }.
func (d Details) Command(k string) string {
	v := d.Get(k)
	i := strings.Index(v, "argv[]=")
	if i < 0 {
		return v
	}
	rest := v[i+len("argv[]="):]
	if j := strings.Index(rest, " ; "); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// Since mengembalikan waktu dari timestamp systemd ("Wed 2026-09-16 20:51:10 +04").
func (d Details) Since(k string) time.Time {
	v := d.Get(k)
	for _, layout := range []string{"Mon 2006-01-02 15:04:05 -07", "Mon 2006-01-02 15:04:05 MST"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

// CrashLooping melaporkan unit yang berulang kali di-restart systemd atau gagal.
func (d Details) CrashLooping() bool {
	return d.Int("NRestarts") > 0 && (d.Get("Result") != "success" || d.Get("SubState") == "auto-restart")
}

// Show membaca detail satu unit.
func Show(ctx context.Context, r run.Runner, unit string, user bool) (Details, error) {
	out, stderr, err := r.Capture(ctx, run.Command{Argv: ShowArgs(unit, user)})
	if noSystemd(stderr) {
		return Details{}, ErrNoSystemd
	}
	if err != nil {
		return Details{}, fmt.Errorf("systemctl show gagal: %s", strings.TrimSpace(stderr))
	}
	blocks := ParseShow(out)
	if len(blocks) == 0 {
		return Details{}, errors.New("unit tidak ditemukan")
	}
	return Details{Props: blocks[0]}, nil
}

// Aksi yang tersedia untuk unit.
const (
	ActStart       = "start"
	ActStop        = "stop"
	ActRestart     = "restart"
	ActReload      = "reload"
	ActEnable      = "enable"
	ActDisable     = "disable"
	ActEnableNow   = "enable-now"
	ActResetFailed = "reset-failed"
)

// critical adalah unit yang bila dihentikan bisa memutus akses ke server atau mematikan sistem.
var critical = map[string]string{
	"ssh.service":              "SSH: sesi remote kamu bisa terputus dan tidak bisa login lagi",
	"sshd.service":             "SSH: sesi remote kamu bisa terputus dan tidak bisa login lagi",
	"systemd-networkd.service": "jaringan server bisa putus",
	"NetworkManager.service":   "jaringan server bisa putus",
	"networking.service":       "jaringan server bisa putus",
	"systemd-resolved.service": "resolusi nama domain (DNS) berhenti",
	"dbus.service":             "banyak layanan sistem bergantung pada D-Bus; sistem bisa tidak stabil",
	"systemd-logind.service":   "semua sesi login bisa terputus",
	"systemd-journald.service": "pencatatan log berhenti",
	"gdm.service":              "sesi desktop grafis akan ditutup",
	"gdm3.service":             "sesi desktop grafis akan ditutup",
	"docker.service":           "semua container berhenti",
	"containerd.service":       "semua container berhenti",
}

// Critical mengembalikan alasan unit berisiko tinggi, atau string kosong.
func Critical(unit string) string { return critical[unit] }

// Plan membangun command untuk aksi pada unit.
func Plan(action, unit string, user bool) run.Plan {
	base := scopeArgs(user)
	root := !user
	crit := Critical(unit)
	danger := func(lvl risk.Level) risk.Level {
		if crit != "" && lvl < risk.Dangerous {
			return risk.Dangerous
		}
		return lvl
	}
	userLine := []run.Line{}
	if user {
		userLine = append(userLine, run.Line{Token: "--user", Meaning: "unit milik user kamu, bukan unit sistem"})
	}
	unitLine := run.Line{Token: unit, Meaning: "nama unit"}
	cmd := func(verb, title, meaning, effect string, lvl risk.Level, extra ...string) run.Command {
		argv := append(append(append([]string(nil), base...), verb), extra...)
		argv = append(argv, unit)
		lines := append(append([]run.Line{}, userLine...), run.Line{Token: strings.Join(append([]string{verb}, extra...), " "), Meaning: meaning}, unitLine)
		return run.Command{Title: title, Argv: argv, NeedsRoot: root, Explain: lines, Effect: effect, Risk: lvl}
	}

	switch action {
	case ActStart:
		return run.Single(cmd("start", "Jalankan "+unit, "jalankan sekarang (tidak mengubah apakah menyala saat boot)", "Service mulai berjalan. Bila gagal, lihat log unit ini.", risk.Caution))
	case ActStop:
		effect := "Service berhenti sampai dijalankan lagi atau server reboot (bila enabled)."
		if crit != "" {
			effect += " PERINGATAN: " + crit + "."
		}
		return run.Single(cmd("stop", "Hentikan "+unit, "hentikan sekarang", effect, danger(risk.Caution)))
	case ActRestart:
		c := cmd("restart", "Restart "+unit, "hentikan lalu jalankan lagi (koneksi yang sedang berjalan terputus)", "Layanan terputus sebentar. Konfigurasi baru ikut dimuat.", risk.Caution)
		if crit != "" && unit != "ssh.service" && unit != "sshd.service" {
			c.Risk = risk.Dangerous
			c.Effect += " PERINGATAN: " + crit + " selama restart."
		}
		c.Safer = "reload bila service mendukungnya: memuat konfigurasi tanpa memutus koneksi"
		return run.Single(c)
	case ActReload:
		return run.Single(cmd("reload", "Muat ulang konfigurasi "+unit, "minta service membaca ulang konfigurasinya tanpa berhenti", "Koneksi yang sedang berjalan tidak terputus.", risk.Safe))
	case ActEnable:
		return run.Single(cmd("enable", "Nyalakan "+unit+" saat boot", "buat symlink supaya unit dijalankan otomatis saat boot", "Tidak menjalankan service sekarang; hanya saat boot berikutnya.", risk.Safe))
	case ActEnableNow:
		return run.Single(cmd("enable", "Nyalakan "+unit+" sekarang dan saat boot", "jalankan otomatis saat boot", "Service langsung berjalan dan akan menyala lagi setiap boot.", risk.Caution, "--now"))
	case ActDisable:
		effect := "Service tidak menyala otomatis saat boot berikutnya. Yang sedang berjalan tidak dihentikan."
		if crit != "" {
			effect += " PERINGATAN: setelah reboot, " + crit + "."
		}
		return run.Single(cmd("disable", "Matikan "+unit+" saat boot", "hapus symlink boot", effect, danger(risk.Caution)))
	case ActResetFailed:
		return run.Single(cmd("reset-failed", "Bersihkan status gagal "+unit, "hapus tanda gagal & hitungan restart", "Hanya membersihkan status; service tidak dijalankan.", risk.Safe))
	}
	return run.Plan{}
}
