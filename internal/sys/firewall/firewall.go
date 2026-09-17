// Package firewall membaca status ufw dan membangun command untuk mengelolanya dengan pengaman
// supaya sesi SSH tidak terputus. Murni pengumpul data.
package firewall

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Rule adalah satu aturan dari `ufw status numbered`.
type Rule struct {
	Num     int
	To      string // "22/tcp", "80,443/tcp", "Nginx Full", "8080"
	Action  string // ALLOW, DENY, REJECT, LIMIT
	Dir     string // IN, OUT, FWD
	From    string // "Anywhere", "203.0.113.0/24"
	V6      bool
	Comment string
}

// Status adalah kondisi ufw.
type Status struct {
	Installed bool
	Readable  bool // aturan terbaca (butuh root)
	Active    bool
	Defaults  string // "deny (incoming), allow (outgoing), disabled (routed)"
	Logging   string
	Rules     []Rule
	Apps      []string
}

var ruleRe = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s{2,}(ALLOW|DENY|REJECT|LIMIT)(?:\s+(IN|OUT|FWD))?\s+(.+?)\s*$`)

// ParseNumbered mem-parse `ufw status numbered`.
func ParseNumbered(out string) (active bool, rules []Rule) {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Status:") {
			active = strings.TrimSpace(strings.TrimPrefix(line, "Status:")) == "active"
			continue
		}
		m := ruleRe.FindStringSubmatch(strings.TrimRight(line, " "))
		if m == nil {
			continue
		}
		r := Rule{To: strings.TrimSpace(m[2]), Action: m[3], Dir: m[4], From: strings.TrimSpace(m[5])}
		r.Num, _ = strconv.Atoi(m[1])
		if r.Dir == "" {
			r.Dir = "IN"
		}
		if i := strings.Index(r.From, "#"); i >= 0 {
			r.Comment = strings.TrimSpace(r.From[i+1:])
			r.From = strings.TrimSpace(r.From[:i])
		}
		if strings.HasSuffix(r.To, "(v6)") {
			r.V6 = true
			r.To = strings.TrimSpace(strings.TrimSuffix(r.To, "(v6)"))
			r.From = strings.TrimSpace(strings.TrimSuffix(r.From, "(v6)"))
		}
		rules = append(rules, r)
	}
	return active, rules
}

// ParseVerbose mengambil default policy dan logging dari `ufw status verbose`.
func ParseVerbose(out string) (defaults, logging string) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "Default:"):
			defaults = strings.TrimSpace(strings.TrimPrefix(line, "Default:"))
		case strings.HasPrefix(line, "Logging:"):
			logging = strings.TrimSpace(strings.TrimPrefix(line, "Logging:"))
		}
	}
	return defaults, logging
}

// ParseApps mem-parse `ufw app list`.
func ParseApps(out string) []string {
	var apps []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			apps = append(apps, strings.TrimSpace(line))
		}
	}
	return apps
}

// appPorts adalah port untuk profil aplikasi ufw yang umum.
var appPorts = map[string][]int{
	"OpenSSH": {22}, "Nginx Full": {80, 443}, "Nginx HTTP": {80}, "Nginx HTTPS": {443},
	"Apache Full": {80, 443}, "Apache": {80}, "Apache Secure": {443}, "Postfix": {25},
}

// Allows melaporkan apakah aturan mengizinkan port TCP masuk dari mana saja.
func (r Rule) Allows(port int) bool {
	if (r.Action != "ALLOW" && r.Action != "LIMIT") || r.Dir != "IN" {
		return false
	}
	if !strings.HasPrefix(r.From, "Anywhere") {
		return false
	}
	if ports, ok := appPorts[r.To]; ok {
		for _, p := range ports {
			if p == port {
				return true
			}
		}
		return false
	}
	spec, proto, _ := strings.Cut(r.To, "/")
	if proto != "" && proto != "tcp" {
		return false
	}
	for _, part := range strings.Split(spec, ",") {
		if lo, hi, ok := strings.Cut(part, ":"); ok {
			a, _ := strconv.Atoi(lo)
			b, _ := strconv.Atoi(hi)
			if port >= a && port <= b {
				return true
			}
			continue
		}
		if n, err := strconv.Atoi(part); err == nil && n == port {
			return true
		}
	}
	return false
}

// Paths adalah lokasi yang dibaca (diganti saat test).
type Paths struct {
	Binary string
	Conf   string
}

// DefaultPaths untuk sistem sungguhan.
var DefaultPaths = Paths{Binary: "/usr/sbin/ufw", Conf: "/etc/ufw/ufw.conf"}

// Read membaca status ufw. Aturan hanya terbaca bila sudo tersedia tanpa password (atau root).
func Read(ctx context.Context, r run.Runner, p Paths) Status {
	var s Status
	if _, err := os.Stat(p.Binary); err != nil {
		return s
	}
	s.Installed = true
	if data, err := os.ReadFile(p.Conf); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == "ENABLED" {
				s.Active = strings.Trim(v, `"' `) == "yes"
			}
		}
	}
	out, _, err := r.Capture(ctx, run.Command{Argv: []string{"ufw", "status", "numbered"}, NeedsRoot: true})
	if err != nil {
		return s
	}
	s.Readable = true
	s.Active, s.Rules = ParseNumbered(out)
	if v, _, err := r.Capture(ctx, run.Command{Argv: []string{"ufw", "status", "verbose"}, NeedsRoot: true}); err == nil {
		s.Defaults, s.Logging = ParseVerbose(v)
	}
	if a, _, err := r.Capture(ctx, run.Command{Argv: []string{"ufw", "app", "list"}, NeedsRoot: true}); err == nil {
		s.Apps = ParseApps(a)
	}
	return s
}

// SSHContext adalah fakta tentang SSH yang dipakai pengaman.
type SSHContext struct {
	Port       int  // port sshd (dari konfigurasi), 0 bila tidak diketahui
	InSession  bool // ubt dijalankan dari sesi SSH
	SessionIP  string
	SSHRunning bool // ada yang listening di port SSH
}

// Lockout menjelaskan risiko terkunci bila firewall diaktifkan/aturan dihapus.
func Lockout(s Status, ssh SSHContext, removing *Rule) string {
	if ssh.Port == 0 || (!ssh.InSession && !ssh.SSHRunning) {
		return ""
	}
	for _, r := range s.Rules {
		if removing != nil && r.Num == removing.Num {
			continue
		}
		if r.Allows(ssh.Port) {
			return ""
		}
	}
	who := "SSH ke server ini"
	if ssh.InSession {
		who = "sesi SSH kamu saat ini"
	}
	return fmt.Sprintf("Tidak ada aturan lain yang mengizinkan port SSH %d. Setelah ini %s bisa terputus dan kamu tidak bisa login lagi kecuali lewat konsol penyedia server.", ssh.Port, who)
}

// Target adalah tujuan aturan allow/deny.
type Target struct {
	App   string // profil aplikasi, mis. "Nginx Full"
	Port  string // "8080", "6000:6007"
	Proto string // tcp, udp, atau "" (keduanya)
	From  string // IP/CIDR sumber, "" = mana saja
}

// ValidatePort memeriksa port tunggal atau rentang a:b.
func ValidatePort(s string) error {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) > 2 {
		return errors.New("format port: 8080 atau rentang 6000:6007")
	}
	prev := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("port harus angka 1-65535")
		}
		if n <= prev {
			return errors.New("rentang harus dari kecil ke besar")
		}
		prev = n
	}
	return nil
}

// ValidateSource memeriksa IP atau CIDR sumber.
func ValidateSource(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return nil
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return nil
	}
	return errors.New("isi IP (203.0.113.9) atau jaringan CIDR (10.0.0.0/8)")
}

func (t Target) args() []string {
	if t.App != "" {
		if t.From == "" {
			return []string{t.App}
		}
		return []string{"from", t.From, "to", "any", "app", t.App}
	}
	if t.From == "" {
		spec := t.Port
		if t.Proto != "" {
			spec += "/" + t.Proto
		} else if strings.Contains(t.Port, ":") {
			spec += "/tcp" // ufw mewajibkan protokol untuk rentang port
		}
		return []string{spec}
	}
	args := []string{"from", t.From, "to", "any", "port", t.Port}
	if t.Proto != "" {
		args = append(args, "proto", t.Proto)
	}
	return args
}

func (t Target) describe() string {
	what := "port " + t.Port
	if t.Proto != "" {
		what += "/" + t.Proto
	}
	if t.App != "" {
		what = "aplikasi " + t.App
	}
	from := "dari mana saja"
	if t.From != "" {
		from = "hanya dari " + t.From
	}
	return what + " " + from
}

// RulePlan membangun aturan allow/deny/limit.
func RulePlan(action string, t Target, comment string) run.Plan {
	argv := append([]string{"ufw", action}, t.args()...)
	if comment != "" {
		argv = append(argv, "comment", comment)
	}
	meaning := map[string]string{
		"allow": "izinkan koneksi masuk",
		"deny":  "tolak diam-diam (pengirim tidak diberi tahu)",
		"limit": "izinkan, tetapi blok sementara IP yang membuka ≥6 koneksi dalam 30 detik (cocok untuk SSH)",
	}[action]
	lvl := risk.Caution
	title := map[string]string{"allow": "Izinkan ", "deny": "Tolak ", "limit": "Batasi "}[action] + t.describe()
	effect := "Aturan berlaku langsung bila ufw aktif; bila belum aktif, tersimpan dan berlaku saat diaktifkan."
	if action == "allow" && t.From == "" && t.App == "" {
		effect += " Port ini terbuka untuk seluruh internet: pastikan aplikasinya memang untuk publik."
	}
	return run.Single(run.Command{
		Title: title, Argv: argv, NeedsRoot: true,
		Explain: []run.Line{{Token: "ufw " + action, Meaning: meaning}, {Token: strings.Join(t.args(), " "), Meaning: t.describe()}},
		Effect:  effect, Risk: lvl,
	})
}

// EnablePlan mengaktifkan ufw; bila SSH berisiko terputus, izinkan SSH lebih dulu.
func EnablePlan(s Status, ssh SSHContext) run.Plan {
	p := run.Plan{Title: "Aktifkan firewall ufw"}
	if warn := Lockout(s, ssh, nil); warn != "" {
		spec := strconv.Itoa(ssh.Port) + "/tcp"
		p.Steps = append(p.Steps, run.Command{
			Title: "Izinkan SSH dulu supaya tidak terkunci", Argv: []string{"ufw", "limit", spec, "comment", "SSH (ditambahkan ubt)"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "ufw limit " + spec, Meaning: "izinkan SSH dan blok sementara IP yang mencoba terlalu sering"}},
			Effect:  "Ditambahkan otomatis karena: " + warn,
			Risk:    risk.Caution,
		})
	}
	p.Steps = append(p.Steps, run.Command{
		Title: "Aktifkan ufw", Argv: []string{"ufw", "--force", "enable"}, NeedsRoot: true,
		Explain: []run.Line{
			{Token: "enable", Meaning: "nyalakan firewall sekarang dan setiap boot"},
			{Token: "--force", Meaning: "lewati pertanyaan y/n ufw (persetujuan sudah diberikan di layar ini)"},
		},
		Effect: "Semua koneksi masuk yang tidak diizinkan aturan akan ditolak (default deny incoming). JANGAN TUTUP SESI INI sampai kamu mengetes login SSH dari terminal baru.",
		Risk:   risk.Dangerous,
	})
	return p
}

// DisablePlan mematikan ufw.
func DisablePlan() run.Plan {
	return run.Single(run.Command{
		Title: "Matikan firewall ufw", Argv: []string{"ufw", "disable"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "ufw disable", Meaning: "matikan firewall sekarang dan saat boot; aturan tetap tersimpan"}},
		Effect:  "Semua port yang listening bisa diakses dari luar (kecuali diblok firewall penyedia cloud).",
		Risk:    risk.Dangerous,
	})
}

// DeletePlan menghapus aturan bernomor.
func DeletePlan(r Rule, warn string) run.Plan {
	lvl := risk.Caution
	effect := "Nomor aturan lain bisa bergeser setelah ini."
	if warn != "" {
		lvl = risk.Dangerous
		effect = "PERINGATAN: " + warn + " " + effect
	}
	v6 := ""
	if r.V6 {
		v6 = " (v6)"
	}
	return run.Single(run.Command{
		Title: fmt.Sprintf("Hapus aturan %d: %s %s %s%s", r.Num, r.Action, r.To, r.From, v6),
		Argv:  []string{"ufw", "--force", "delete", strconv.Itoa(r.Num)}, NeedsRoot: true,
		Explain: []run.Line{
			{Token: "delete " + strconv.Itoa(r.Num), Meaning: "hapus aturan nomor ini (lihat: sudo ufw status numbered)"},
			{Token: "--force", Meaning: "lewati pertanyaan y/n ufw"},
		},
		Effect: effect, Risk: lvl,
	})
}

// ResetPlan mengembalikan ufw ke kondisi awal.
func ResetPlan() run.Plan {
	return run.Single(run.Command{
		Title: "Reset firewall", Argv: []string{"ufw", "--force", "reset"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "ufw --force reset", Meaning: "matikan ufw dan hapus SEMUA aturan (salinan lama disimpan di /etc/ufw/*.rules.<tanggal>)"}},
		Effect:  "Semua aturan hilang. Tambahkan lagi aturan SSH sebelum mengaktifkan ufw.",
		Risk:    risk.Dangerous,
	})
}

// InstallPlan memasang ufw.
func InstallPlan() run.Plan {
	return run.Single(run.Command{
		Title: "Install ufw", Argv: []string{"apt-get", "install", "-y", "ufw"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "ufw", Meaning: "Uncomplicated Firewall: pengelola iptables/nftables yang ramah pemula"}},
		Effect:  "ufw terpasang dalam keadaan mati. Tambahkan aturan SSH dulu, baru aktifkan.",
	})
}
