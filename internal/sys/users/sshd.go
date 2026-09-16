package users

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// DropIn adalah file konfigurasi sshd yang dikelola ubt. Diawali 00- karena sshd memakai nilai
// PERTAMA yang ditemukan dan file sshd_config.d dibaca urut nama: 50-cloud-init.conf (yang sering
// menyalakan PasswordAuthentication) tidak boleh mendahului setelan ubt.
const DropIn = "/etc/ssh/sshd_config.d/00-ubt.conf"

// SSHD adalah konfigurasi efektif sshd yang relevan untuk keamanan.
type SSHD struct {
	Installed  bool
	Values     map[string]string // kunci huruf kecil → nilai efektif
	Sources    map[string]string // kunci → file tempat nilai ditetapkan ("" = default)
	Unreadable []string          // file konfigurasi yang tidak bisa dibaca
}

// Defaults OpenSSH 9.x (Ubuntu 24.04) untuk setelan yang ditampilkan.
var sshdDefaults = map[string]string{
	"port": "22", "permitrootlogin": "prohibit-password", "passwordauthentication": "yes",
	"pubkeyauthentication": "yes", "kbdinteractiveauthentication": "no", "maxauthtries": "6",
	"permitemptypasswords": "no", "x11forwarding": "no",
}

// ReadSSHD membaca sshd_config beserta Include-nya dengan aturan "nilai pertama menang",
// berhenti di blok Match (setelan di dalam Match hanya berlaku bersyarat).
func ReadSSHD(configPath, sshdBinary string) SSHD {
	s := SSHD{Values: map[string]string{}, Sources: map[string]string{}}
	if _, err := os.Stat(sshdBinary); err == nil {
		s.Installed = true
	}
	stopped := false
	var readFile func(path string, depth int)
	readFile = func(path string, depth int) {
		if depth > 8 || stopped {
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				s.Unreadable = append(s.Unreadable, path)
			}
			return
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val := splitDirective(line)
			key = strings.ToLower(key)
			switch key {
			case "include":
				for _, pattern := range strings.Fields(val) {
					if !filepath.IsAbs(pattern) {
						pattern = filepath.Join("/etc/ssh", pattern)
					}
					matches, _ := filepath.Glob(pattern)
					sort.Strings(matches)
					for _, m := range matches {
						readFile(m, depth+1)
					}
				}
			case "match":
				stopped = true
				return
			default:
				if _, seen := s.Values[key]; !seen {
					s.Values[key] = strings.Trim(val, `"`)
					s.Sources[key] = path
				}
			}
		}
	}
	readFile(configPath, 0)
	for k, v := range sshdDefaults {
		if _, ok := s.Values[k]; !ok {
			s.Values[k] = v
		}
	}
	return s
}

func splitDirective(line string) (string, string) {
	if i := strings.IndexAny(line, " \t="); i >= 0 {
		return line[:i], strings.TrimSpace(strings.TrimLeft(line[i:], " \t="))
	}
	return line, ""
}

// Finding adalah satu butir checklist hardening SSH.
type Finding struct {
	Key    string
	Value  string
	OK     bool
	Level  risk.Level
	Title  string
	Advice string
	Wanted string // nilai yang disarankan
	Source string
}

// Audit menilai konfigurasi SSH.
func (s SSHD) Audit() []Finding {
	v := s.Values
	var out []Finding
	add := func(key string, ok bool, lvl risk.Level, title, advice, wanted string) {
		out = append(out, Finding{Key: key, Value: v[key], OK: ok, Level: lvl, Title: title, Advice: advice, Wanted: wanted, Source: s.Sources[key]})
	}
	root := v["permitrootlogin"]
	add("permitrootlogin", root == "no" || root == "prohibit-password" || root == "without-password", risk.Dangerous,
		"Login root lewat SSH", "Root tidak boleh login dengan password. \"no\" paling aman: login sebagai user biasa lalu pakai sudo.", "no")
	add("passwordauthentication", v["passwordauthentication"] == "no", risk.Caution,
		"Login dengan password", "Server yang terbuka ke internet terus-menerus dicoba ditebak password-nya. Pakai SSH key lalu matikan login password.", "no")
	add("pubkeyauthentication", v["pubkeyauthentication"] != "no", risk.Dangerous,
		"Login dengan SSH key", "Harus aktif supaya bisa login dengan key.", "yes")
	add("permitemptypasswords", v["permitemptypasswords"] != "yes", risk.Dangerous,
		"Password kosong", "Akun tanpa password tidak boleh bisa login.", "no")
	tries, _ := strconv.Atoi(v["maxauthtries"])
	add("maxauthtries", tries > 0 && tries <= 4, risk.Safe,
		"Batas percobaan per koneksi", "Nilai kecil (3–4) memperlambat penebakan password.", "3")
	add("port", true, risk.Safe, "Port SSH", "Mengganti port bukan pengaman utama, tetapi mengurangi sampah log dari bot. Pastikan firewall mengizinkan port baru sebelum mengganti.", v["port"])
	return out
}

// HardeningInput adalah perubahan yang diminta user.
type HardeningInput struct {
	DisablePassword bool
	DisableRoot     bool
	MaxAuthTries    int    // 0 = tidak diubah
	Port            string // "" = tidak diubah
}

// SafetyContext adalah fakta yang dipakai pengaman anti-terkunci.
type SafetyContext struct {
	CurrentUser      string
	CurrentHasKey    bool     // user saat ini punya minimal 1 key valid di authorized_keys
	SudoUsersWithKey []string // user grup sudo yang punya key valid
	CurrentIsSudo    bool
	UFWActive        bool
	SocketActivated  bool // ssh.socket dipakai (Ubuntu 22.10+)
	CurrentPort      string
}

// Guard memeriksa perubahan yang bisa mengunci user keluar dari server. Error berarti ditolak.
func Guard(in HardeningInput, ctx SafetyContext) error {
	if in.DisablePassword {
		switch {
		case !ctx.CurrentHasKey && len(ctx.SudoUsersWithKey) == 0:
			return errors.New("ditolak: belum ada satu pun user sudo yang punya SSH key valid. Tambahkan key dulu, tes login dengan key, baru matikan login password")
		case !ctx.CurrentHasKey:
			return fmt.Errorf("ditolak: user %s belum punya SSH key. Setelah login password dimatikan, kamu hanya bisa login sebagai %s. Tambahkan key untuk dirimu dulu", ctx.CurrentUser, strings.Join(ctx.SudoUsersWithKey, ", "))
		}
	}
	if in.DisableRoot && !ctx.CurrentIsSudo && ctx.CurrentUser != "root" {
		return errors.New("ditolak: user ini tidak ada di grup sudo, jadi setelah root dimatikan tidak ada jalan untuk jadi admin")
	}
	if in.Port != "" {
		n, err := strconv.Atoi(in.Port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("port harus angka 1-65535")
		}
	}
	return nil
}

// DropInContent menghasilkan isi 00-ubt.conf, digabung dengan nilai lama di file itu.
func DropInContent(in HardeningInput, existing map[string]string, now time.Time) string {
	vals := map[string]string{}
	for k, v := range existing {
		vals[k] = v
	}
	if in.DisablePassword {
		vals["PasswordAuthentication"] = "no"
		vals["KbdInteractiveAuthentication"] = "no"
	}
	if in.DisableRoot {
		vals["PermitRootLogin"] = "no"
	}
	if in.MaxAuthTries > 0 {
		vals["MaxAuthTries"] = strconv.Itoa(in.MaxAuthTries)
	}
	if in.Port != "" {
		vals["Port"] = in.Port
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "# Dikelola ubt (%s). File 00- dibaca paling awal, jadi setelan ini menang.\n", now.Format("2006-01-02"))
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %s\n", k, vals[k])
	}
	return b.String()
}

// ReadDropIn membaca setelan yang sudah ada di 00-ubt.conf (nama kunci asli).
func ReadDropIn(path string) map[string]string {
	vals := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return vals
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v := splitDirective(line)
		vals[k] = v
	}
	return vals
}

// HardeningPlan membangun Plan penerapan hardening SSH.
func HardeningPlan(in HardeningInput, ctx SafetyContext, existing map[string]string, now time.Time) run.Plan {
	var steps []run.Command
	portChanged := in.Port != "" && in.Port != ctx.CurrentPort
	if portChanged && ctx.UFWActive {
		steps = append(steps, run.Command{
			Title: "Buka port " + in.Port + " di firewall dulu", Argv: []string{"ufw", "allow", in.Port + "/tcp"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "ufw allow " + in.Port + "/tcp", Meaning: "izinkan koneksi SSH ke port baru SEBELUM sshd pindah, supaya kamu tidak terkunci"}},
			Risk:    risk.Caution,
		})
	}
	steps = append(steps, run.Command{
		Title: "Tulis " + DropIn, Argv: []string{"install", "-m", "0644", "/dev/stdin", DropIn}, NeedsRoot: true,
		Stdin: DropInContent(in, existing, now), StdinLabel: "(setelan sshd)",
		Explain: []run.Line{
			{Token: "install -m 0644 /dev/stdin", Meaning: "tulis file konfigurasi dari stdin"},
			{Token: DropIn, Meaning: "file tambahan; sshd_config utama tidak diubah sehingga mudah dikembalikan (hapus file ini)"},
		},
		Risk: risk.Dangerous,
	})
	var last run.Command
	switch {
	case portChanged && ctx.SocketActivated:
		steps = append(steps, run.Command{Title: "Muat ulang systemd", Argv: []string{"systemctl", "daemon-reload"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "daemon-reload", Meaning: "Ubuntu 24.04 memakai ssh.socket; port baru dibaca generator systemd"}}})
		last = run.Command{Title: "Restart ssh.socket", Argv: []string{"systemctl", "restart", "ssh.socket"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "restart ssh.socket", Meaning: "socket mulai mendengarkan di port baru; sesi yang sedang terbuka tidak diputus"}}}
	case portChanged:
		last = run.Command{Title: "Restart ssh", Argv: []string{"systemctl", "restart", "ssh"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "restart ssh", Meaning: "sshd mendengarkan di port baru; sesi yang sedang terbuka tidak diputus"}}}
	default:
		last = run.Command{Title: "Muat ulang ssh", Argv: []string{"systemctl", "reload", "ssh"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "reload ssh", Meaning: "terapkan setelan baru tanpa memutus sesi yang ada"}}}
	}
	last.Effect = "JANGAN TUTUP SESI INI. Buka terminal baru dan tes login (" + loginHint(in, ctx) + "). Bila gagal, hapus " + DropIn + " dari sesi ini lalu reload ssh."
	last.Risk = risk.Dangerous
	steps = append(steps, last)
	return run.Plan{
		Title: "Amankan SSH server",
		Steps: steps,
		Check: &run.Command{Title: "Validasi konfigurasi sshd", Argv: []string{"sshd", "-t"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "sshd -t", Meaning: "cek sintaks semua file konfigurasi; bila gagal, sshd tidak di-reload sehingga koneksi tetap aman"}}},
	}
}

func loginHint(in HardeningInput, ctx SafetyContext) string {
	port := ctx.CurrentPort
	if in.Port != "" {
		port = in.Port
	}
	return fmt.Sprintf("ssh -p %s %s@IP_SERVER", port, ctx.CurrentUser)
}

// InstallSSHPlan memasang OpenSSH server.
func InstallSSHPlan() run.Plan {
	return run.Single(run.Command{
		Title: "Install OpenSSH server", Argv: []string{"apt-get", "install", "-y", "openssh-server"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "openssh-server", Meaning: "program sshd supaya server bisa diakses lewat SSH dari komputer lain"}},
		Effect:  "SSH langsung aktif di port 22 dan login password diizinkan. Setelah memasang key, amankan dari menu ini.",
		Risk:    risk.Caution,
	})
}
