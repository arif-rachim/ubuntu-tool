// Package packages membaca kondisi paket apt/dpkg/snap dan membangun command untuk mengelolanya.
// Murni pengumpul data.
package packages

import (
	"bufio"
	"context"
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
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
)

// Installed adalah satu paket yang terpasang.
type Installed struct {
	Name    string
	Version string
	SizeKB  int64
	Status  string // ii = terpasang normal; rc = dihapus tapi config tertinggal; iU/iF = rusak
}

// InstalledArgs adalah command daftar paket terpasang.
var InstalledArgs = []string{"dpkg-query", "-W", "-f=${Package}\t${Version}\t${Installed-Size}\t${db:Status-Abbrev}\n"}

// ParseInstalled mem-parse output InstalledArgs, terbesar lebih dulu.
func ParseInstalled(out string) []Installed {
	var pkgs []Installed
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 4 || f[0] == "" {
			continue
		}
		size, _ := strconv.ParseInt(strings.TrimSpace(f[2]), 10, 64)
		pkgs = append(pkgs, Installed{Name: f[0], Version: f[1], SizeKB: size, Status: strings.TrimSpace(f[3])})
	}
	sort.SliceStable(pkgs, func(i, j int) bool { return pkgs[i].SizeKB > pkgs[j].SizeKB })
	return pkgs
}

// Upgradable adalah paket yang punya versi lebih baru.
type Upgradable struct {
	Name     string
	Suites   []string
	New      string
	Old      string
	Security bool
}

// ParseUpgradable mem-parse `apt list --upgradable`.
//
//	nginx/noble-updates,noble-security 1.24.0-2ubuntu7.1 amd64 [upgradable from: 1.24.0-2ubuntu7]
func ParseUpgradable(out string) []Upgradable {
	var ups []Upgradable
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "[upgradable from:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 6 {
			continue
		}
		name, suites, _ := strings.Cut(f[0], "/")
		u := Upgradable{Name: name, Suites: strings.Split(suites, ","), New: f[1], Old: strings.TrimSuffix(f[len(f)-1], "]")}
		for _, s := range u.Suites {
			if strings.HasSuffix(s, "-security") {
				u.Security = true
			}
		}
		ups = append(ups, u)
	}
	sort.SliceStable(ups, func(i, j int) bool {
		if ups[i].Security != ups[j].Security {
			return ups[i].Security
		}
		return ups[i].Name < ups[j].Name
	})
	return ups
}

// SearchResult adalah satu hasil apt-cache search.
type SearchResult struct {
	Name        string
	Description string
}

// ParseSearch mem-parse `apt-cache search` ("nama - deskripsi").
func ParseSearch(out string) []SearchResult {
	var res []SearchResult
	for _, line := range strings.Split(out, "\n") {
		name, desc, ok := strings.Cut(line, " - ")
		if ok && name != "" {
			res = append(res, SearchResult{Name: strings.TrimSpace(name), Description: strings.TrimSpace(desc)})
		}
	}
	return res
}

// Policy adalah hasil apt-cache policy satu paket.
type Policy struct {
	Name      string
	Installed string // kosong bila tidak terpasang
	Candidate string // kosong bila tidak ada di repo
	Sources   []string
}

// ParsePolicy mem-parse `apt-cache policy PAKET`.
func ParsePolicy(out string) Policy {
	var p Policy
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case p.Name == "" && strings.HasSuffix(t, ":") && !strings.Contains(t, " "):
			p.Name = strings.TrimSuffix(t, ":")
		case strings.HasPrefix(t, "Installed:"):
			p.Installed = none(strings.TrimSpace(strings.TrimPrefix(t, "Installed:")))
		case strings.HasPrefix(t, "Candidate:"):
			p.Candidate = none(strings.TrimSpace(strings.TrimPrefix(t, "Candidate:")))
		case strings.Contains(t, "http://") || strings.Contains(t, "https://") || strings.Contains(t, "/var/lib/dpkg/status"):
			f := strings.Fields(t)
			if len(f) >= 3 {
				src := strings.Join(f[1:3], " ")
				if !contains(p.Sources, src) {
					p.Sources = append(p.Sources, src)
				}
			}
		}
	}
	return p
}

func none(s string) string {
	if s == "(none)" {
		return ""
	}
	return s
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// HistoryEntry adalah satu transaksi di /var/log/apt/history.log.
type HistoryEntry struct {
	Start       time.Time
	Commandline string
	RequestedBy string
	Install     []string
	Upgrade     []string
	Remove      []string
	Purge       []string
}

// Summary meringkas perubahan transaksi.
func (h HistoryEntry) Summary() string {
	var parts []string
	for _, x := range []struct {
		label string
		pkgs  []string
	}{{"install", h.Install}, {"upgrade", h.Upgrade}, {"hapus", h.Remove}, {"purge", h.Purge}} {
		if len(x.pkgs) > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", x.label, len(x.pkgs)))
		}
	}
	return strings.Join(parts, ", ")
}

// ParseHistory mem-parse history.log apt, terbaru lebih dulu.
func ParseHistory(s string) []HistoryEntry {
	var entries []HistoryEntry
	var cur *HistoryEntry
	pkgNames := func(v string) []string {
		var names []string
		depth := 0
		start := 0
		for i, r := range v {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					names = append(names, pkgName(v[start:i]))
					start = i + 1
				}
			}
		}
		if strings.TrimSpace(v[start:]) != "" {
			names = append(names, pkgName(v[start:]))
		}
		return names
	}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch k {
		case "Start-Date":
			entries = append(entries, HistoryEntry{})
			cur = &entries[len(entries)-1]
			cur.Start, _ = time.ParseInLocation("2006-01-02  15:04:05", strings.TrimSpace(v), time.Local)
		case "Commandline":
			if cur != nil {
				cur.Commandline = v
			}
		case "Requested-By":
			if cur != nil {
				cur.RequestedBy = v
			}
		case "Install", "Upgrade", "Remove", "Purge":
			if cur == nil {
				continue
			}
			names := pkgNames(v)
			switch k {
			case "Install":
				cur.Install = names
			case "Upgrade":
				cur.Upgrade = names
			case "Remove":
				cur.Remove = names
			case "Purge":
				cur.Purge = names
			}
		}
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

func pkgName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " ("); i >= 0 {
		s = s[:i]
	}
	name, _, _ := strings.Cut(s, ":")
	return name
}

// Source adalah satu sumber repo apt.
type Source struct {
	File    string
	Types   string
	URIs    string
	Suites  string
	Enabled bool
	Format  string // deb822 atau list
}

// ReadSources membaca /etc/apt/sources.list dan sources.list.d (format .sources deb822 dan .list).
func ReadSources(dir string) []Source {
	var out []Source
	files, _ := filepath.Glob(filepath.Join(dir, "sources.list.d", "*"))
	if _, err := os.Stat(filepath.Join(dir, "sources.list")); err == nil {
		files = append([]string{filepath.Join(dir, "sources.list")}, files...)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(f, ".sources"):
			out = append(out, parseDeb822(f, string(data))...)
		case strings.HasSuffix(f, ".list"):
			out = append(out, parseList(f, string(data))...)
		}
	}
	return out
}

func parseDeb822(file, s string) []Source {
	var out []Source
	for _, stanza := range strings.Split(s, "\n\n") {
		src := Source{File: file, Format: "deb822", Enabled: true}
		for _, line := range strings.Split(stanza, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "Types":
				src.Types = v
			case "URIs":
				src.URIs = v
			case "Suites":
				src.Suites = v
			case "Enabled":
				src.Enabled = v != "no"
			}
		}
		if src.URIs != "" {
			out = append(out, src)
		}
	}
	return out
}

func parseList(file, s string) []Source {
	var out []Source
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		enabled := true
		if strings.HasPrefix(t, "#") {
			t = strings.TrimSpace(strings.TrimPrefix(t, "#"))
			enabled = false
		}
		f := strings.Fields(t)
		if len(f) < 3 || (f[0] != "deb" && f[0] != "deb-src") {
			continue
		}
		i := 1
		if strings.HasPrefix(f[1], "[") {
			for i < len(f) && !strings.HasSuffix(f[i], "]") {
				i++
			}
			i++
		}
		if i+1 >= len(f) {
			continue
		}
		out = append(out, Source{File: file, Types: f[0], URIs: f[i], Suites: f[i+1], Enabled: enabled, Format: "list"})
	}
	return out
}

// AutoUpgrade adalah konfigurasi unattended-upgrades.
type AutoUpgrade struct {
	Configured bool // file 20auto-upgrades ada
	UpdateList bool
	Upgrade    bool
}

// ParseAutoUpgrade mem-parse /etc/apt/apt.conf.d/20auto-upgrades.
func ParseAutoUpgrade(s string) AutoUpgrade {
	a := AutoUpgrade{Configured: true}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		on := strings.Contains(line, `"1"`)
		switch {
		case strings.HasPrefix(line, "APT::Periodic::Update-Package-Lists"):
			a.UpdateList = on
		case strings.HasPrefix(line, "APT::Periodic::Unattended-Upgrade"):
			a.Upgrade = on
		}
	}
	return a
}

// Paths adalah lokasi file yang dibaca (diganti saat test).
type Paths struct {
	RebootRequired string
	RebootPkgs     string
	AutoUpgrades   string
	History        string
	AptDir         string
	ProcRoot       string
}

// DefaultPaths untuk sistem sungguhan.
var DefaultPaths = Paths{
	RebootRequired: "/var/run/reboot-required",
	RebootPkgs:     "/var/run/reboot-required.pkgs",
	AutoUpgrades:   "/etc/apt/apt.conf.d/20auto-upgrades",
	History:        "/var/log/apt/history.log",
	AptDir:         "/etc/apt",
	ProcRoot:       "/proc",
}

// Status adalah ringkasan kondisi paket.
type Status struct {
	Upgradable   []Upgradable
	ListAge      time.Duration // umur daftar paket (sejak apt update terakhir); 0 bila tidak diketahui
	RebootNeeded bool
	RebootPkgs   []string
	Auto         AutoUpgrade
	Broken       string // output dpkg --audit (kosong = sehat)
	LockHolder   *procs.Process
	Snaps        int
}

// ReadStatus membaca kondisi paket.
func ReadStatus(ctx context.Context, r run.Runner, p Paths, now time.Time) Status {
	var s Status
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"apt", "list", "--upgradable"}}); err == nil {
		s.Upgradable = ParseUpgradable(out)
	}
	if st, err := os.Stat("/var/lib/apt/lists"); err == nil {
		s.ListAge = now.Sub(st.ModTime())
	}
	if _, err := os.Stat(p.RebootRequired); err == nil {
		s.RebootNeeded = true
		if data, err := os.ReadFile(p.RebootPkgs); err == nil {
			s.RebootPkgs = strings.Fields(string(data))
		}
	}
	if data, err := os.ReadFile(p.AutoUpgrades); err == nil {
		s.Auto = ParseAutoUpgrade(string(data))
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"dpkg", "--audit"}}); err == nil || out != "" {
		s.Broken = strings.TrimSpace(out)
	}
	s.LockHolder = FindAptProcess(p.ProcRoot)
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"snap", "list"}}); err == nil {
		s.Snaps = max(len(strings.Split(strings.TrimSpace(out), "\n"))-1, 0)
	}
	return s
}

// FindAptProcess mencari proses apt/dpkg/unattended-upgrade yang sedang berjalan (pemegang lock apt).
func FindAptProcess(procRoot string) *procs.Process {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "comm"))
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(comm)) {
		case "apt", "apt-get", "dpkg", "unattended-upgr", "aptitude":
			p, err := procs.Read(procRoot, pid)
			// unattended-upgrade-shutdown berjalan terus menunggu sinyal shutdown dan tidak memegang lock.
			if err != nil || strings.Contains(p.CommandLine(), "unattended-upgrade-shutdown") {
				continue
			}
			return &p
		}
	}
	return nil
}

// ReadHistory membaca riwayat transaksi apt.
func ReadHistory(path string, limit int) ([]HistoryEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var b strings.Builder
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	h := ParseHistory(b.String())
	if limit > 0 && len(h) > limit {
		h = h[:limit]
	}
	return h, sc.Err()
}

// ValidName memeriksa nama paket Debian (huruf kecil, angka, + - .).
func ValidName(s string) error {
	if len(s) < 2 {
		return errors.New("nama paket minimal 2 karakter")
	}
	for i, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && strings.ContainsRune("+-.", r))
		if !ok {
			return fmt.Errorf("nama paket hanya boleh huruf kecil, angka, dan + - . (karakter %q tidak valid)", r)
		}
	}
	return nil
}

// Plan-plan pengelolaan paket.

// UpdatePlan menyegarkan daftar paket.
func UpdatePlan() run.Plan {
	return run.Single(run.Command{
		Title: "Perbarui daftar paket", Argv: []string{"apt-get", "update"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "apt-get update", Meaning: "unduh daftar versi terbaru dari semua repo; belum memasang apa pun"}},
		Effect:  "Tidak ada paket yang berubah. Setelah ini ubt bisa menampilkan update yang tersedia.",
	})
}

// UpgradePlan memasang semua update. Interaktif karena dpkg bisa bertanya soal file konfigurasi.
func UpgradePlan(count, security int) run.Plan {
	return run.Single(run.Command{
		Title: fmt.Sprintf("Pasang %d update (%d keamanan)", count, security), Argv: []string{"apt-get", "upgrade"}, NeedsRoot: true, Interactive: true,
		Explain: []run.Line{
			{Token: "apt-get upgrade", Meaning: "pasang versi terbaru semua paket yang terpasang, tanpa menghapus paket apa pun"},
		},
		Effect: "apt akan menampilkan daftar & meminta konfirmasi Y/n di terminal. Bila ada pertanyaan soal file konfigurasi, pilihan aman adalah mempertahankan versi yang sedang dipakai (N) kecuali kamu tahu perubahannya.",
		Safer:  "pasang update keamanan saja dengan: sudo unattended-upgrade -d",
		Risk:   risk.Caution,
	})
}

// InstallPlan memasang paket.
func InstallPlan(name string) run.Plan {
	return run.Single(run.Command{
		Title: "Install " + name, Argv: []string{"apt-get", "install", name}, NeedsRoot: true, Interactive: true,
		Explain: []run.Line{{Token: "apt-get install", Meaning: "unduh dan pasang paket beserta dependensinya"}, {Token: name, Meaning: "nama paket"}},
		Effect:  "apt menampilkan daftar paket yang ikut terpasang dan meminta konfirmasi di terminal. Service di dalam paket biasanya langsung berjalan.",
		Risk:    risk.Caution,
	})
}

// RemovePlan menghapus paket; purge ikut menghapus konfigurasi.
func RemovePlan(name string, purge bool) run.Plan {
	verb, title, meaning := "remove", "Hapus "+name, "hapus program, tetapi simpan file konfigurasi (bisa dipasang lagi dengan setelan lama)"
	lvl := risk.Caution
	if purge {
		verb, title, meaning = "purge", "Hapus total "+name, "hapus program BESERTA file konfigurasinya di /etc"
		lvl = risk.Dangerous
	}
	return run.Single(run.Command{
		Title: title, Argv: []string{"apt-get", verb, name}, NeedsRoot: true, Interactive: true,
		Explain: []run.Line{{Token: "apt-get " + verb, Meaning: meaning}},
		Effect:  "apt menampilkan paket lain yang ikut terhapus (dependensinya) dan meminta konfirmasi. Periksa daftarnya baik-baik sebelum menjawab Y.",
		Safer:   map[bool]string{true: "remove (tanpa purge) supaya konfigurasi tersimpan", false: ""}[purge],
		Risk:    lvl,
	})
}

// FixBrokenPlan memperbaiki instalasi dpkg yang terputus.
func FixBrokenPlan() run.Plan {
	return run.Plan{
		Title: "Perbaiki paket yang rusak",
		Steps: []run.Command{
			{Title: "Selesaikan konfigurasi yang tertunda", Argv: []string{"dpkg", "--configure", "-a"}, NeedsRoot: true, Interactive: true,
				Explain: []run.Line{{Token: "dpkg --configure -a", Meaning: "lanjutkan instalasi yang terputus (mis. karena listrik mati atau terminal ditutup)"}},
				Risk:    risk.Caution},
			{Title: "Lengkapi dependensi yang hilang", Argv: []string{"apt-get", "--fix-broken", "install"}, NeedsRoot: true, Interactive: true,
				Explain: []run.Line{{Token: "--fix-broken install", Meaning: "pasang atau hapus paket supaya semua dependensi kembali konsisten"}},
				Risk:    risk.Caution},
		},
	}
}

// EnableAutoUpgradePlan mengaktifkan update keamanan otomatis.
func EnableAutoUpgradePlan() run.Plan {
	return run.Plan{
		Title: "Aktifkan update keamanan otomatis",
		Steps: []run.Command{
			{Title: "Pastikan unattended-upgrades terpasang", Argv: []string{"apt-get", "install", "-y", "unattended-upgrades"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "unattended-upgrades", Meaning: "paket yang memasang update keamanan secara otomatis setiap hari"}}},
			{Title: "Nyalakan update otomatis", Argv: []string{"dpkg-reconfigure", "-plow", "unattended-upgrades"}, NeedsRoot: true, Interactive: true,
				Explain: []run.Line{{Token: "dpkg-reconfigure -plow", Meaning: "tampilkan pertanyaan konfigurasi; pilih Yes untuk mengaktifkan"}},
				Effect:  "Membuat /etc/apt/apt.conf.d/20auto-upgrades. Server memasang update keamanan setiap hari tanpa perlu login."},
		},
	}
}

// AddPPAPlan menambah repo PPA.
func AddPPAPlan(ppa string) run.Plan {
	return run.Plan{
		Title: "Tambah repo " + ppa,
		Steps: []run.Command{
			{Title: "Tambah " + ppa, Argv: []string{"add-apt-repository", "-y", ppa}, NeedsRoot: true,
				Explain: []run.Line{
					{Token: "add-apt-repository", Meaning: "tambah repo & kunci penandatangannya ke /etc/apt/sources.list.d"},
					{Token: ppa, Meaning: "PPA: repo pribadi di Launchpad yang dikelola orang/tim di luar Ubuntu"},
				},
				Effect: "Paket dari PPA bisa menggantikan paket resmi Ubuntu dan pemilik PPA bisa memasang apa pun di server ini lewat update. Pakai hanya PPA yang kamu percaya.",
				Risk:   risk.Dangerous},
		},
	}
}

// RebootPlan me-restart server.
func RebootPlan() run.Plan {
	return run.Single(run.Command{
		Title: "Restart server", Argv: []string{"systemctl", "reboot"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "systemctl reboot", Meaning: "matikan semua service dengan rapi lalu nyalakan ulang server"}},
		Effect:  "Semua koneksi (termasuk SSH ini) terputus 1–3 menit. Pastikan tidak ada pekerjaan penting yang sedang berjalan.",
		Risk:    risk.Dangerous,
	})
}
