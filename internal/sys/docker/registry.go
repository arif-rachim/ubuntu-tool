package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Registry adalah profil satu registry image (mis. Nexus di kantor). Password TIDAK PERNAH disimpan
// ubt: yang menyimpan token login adalah docker sendiri di ~/.docker/config.json setelah `docker login`.
type Registry struct {
	Name     string `json:"name"`               // label pendek untuk dipilih di menu, mis. "nexus"
	Host     string `json:"host"`               // host[:port] tanpa skema, mis. nexus.contoh.com:8082
	User     string `json:"user,omitempty"`     // username login
	Repo     string `json:"repo,omitempty"`     // awalan repository, mis. "tim-a" → host/tim-a/app:1.0
	Insecure bool   `json:"insecure,omitempty"` // registry dilayani HTTP polos, bukan HTTPS
}

// Registries adalah isi file profil registry.
type Registries struct {
	Items []Registry `json:"registries"`
}

// Find mencari profil berdasarkan nama.
func (rs Registries) Find(name string) (Registry, bool) {
	for _, r := range rs.Items {
		if r.Name == name {
			return r, true
		}
	}
	return Registry{}, false
}

// Upsert menambah atau mengganti profil dengan nama yang sama, lalu mengurutkan per nama.
func (rs Registries) Upsert(r Registry) Registries {
	out := make([]Registry, 0, len(rs.Items)+1)
	for _, x := range rs.Items {
		if x.Name != r.Name {
			out = append(out, x)
		}
	}
	out = append(out, r)
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return Registries{Items: out}
}

// Remove menghapus profil bernama name.
func (rs Registries) Remove(name string) Registries {
	out := make([]Registry, 0, len(rs.Items))
	for _, x := range rs.Items {
		if x.Name != name {
			out = append(out, x)
		}
	}
	return Registries{Items: out}
}

// Prefix adalah awalan nama image di registry ini: host, atau host/repo.
func (r Registry) Prefix() string {
	if r.Repo == "" {
		return r.Host
	}
	return r.Host + "/" + strings.Trim(r.Repo, "/")
}

// Ref menggabungkan awalan registry dengan nama image lokal. Nama yang sudah memuat awalan
// dibiarkan apa adanya, supaya tidak jadi nexus.../nexus.../app.
func (r Registry) Ref(image string) string {
	image = strings.TrimSpace(image)
	if r.Host == "" || strings.HasPrefix(image, r.Host+"/") {
		return image
	}
	return r.Prefix() + "/" + image
}

// URL adalah alamat antarmuka web registry, untuk ditampilkan sebagai petunjuk.
func (r Registry) URL() string {
	if r.Insecure {
		return "http://" + r.Host
	}
	return "https://" + r.Host
}

var (
	regNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	hostRe    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*(:[0-9]{1,5})?$`)
	repoRe    = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)
)

// ValidRegistryName memeriksa label profil.
func ValidRegistryName(s string) error {
	if !regNameRe.MatchString(strings.TrimSpace(s)) {
		return errors.New("gunakan huruf kecil, angka, garis bawah, atau strip (maks. 32), mis. nexus")
	}
	return nil
}

// ValidRegistryHost memeriksa host[:port] tanpa skema.
func ValidRegistryHost(s string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return errors.New("host registry wajib diisi")
	case strings.Contains(s, "://"):
		return errors.New("tulis host saja tanpa https:// atau http://, mis. nexus.contoh.com:8082")
	case strings.Contains(s, "/"):
		return errors.New("tulis host saja; awalan repository diisi di pertanyaan berikutnya")
	case !hostRe.MatchString(strings.ToLower(s)):
		return errors.New("format host tidak valid; contoh: nexus.contoh.com atau nexus.contoh.com:8082")
	}
	return nil
}

// ValidRegistryRepo memeriksa awalan repository (boleh kosong).
func ValidRegistryRepo(s string) error {
	s = strings.Trim(strings.TrimSpace(s), "/")
	if s == "" {
		return nil
	}
	if !repoRe.MatchString(s) {
		return errors.New("gunakan huruf kecil, angka, titik, strip, dan / sebagai pemisah, mis. tim-a/backend")
	}
	return nil
}

// ValidRegistryUser memeriksa username login (boleh kosong bila registry anonim).
func ValidRegistryUser(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !userRe.MatchString(s) {
		return errors.New("username tidak valid")
	}
	return nil
}

// DefaultRegistryPath adalah lokasi file profil: $XDG_CONFIG_HOME/ubt/registries.json.
func DefaultRegistryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ubt", "registries.json"), nil
}

// LoadRegistries membaca file profil. File yang belum ada bukan error.
func LoadRegistries(path string) (Registries, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Registries{}, nil
	}
	if err != nil {
		return Registries{}, err
	}
	var rs Registries
	if err := json.Unmarshal(b, &rs); err != nil {
		return Registries{}, fmt.Errorf("%s rusak: %w", path, err)
	}
	return rs, nil
}

// RegistriesJSON merender isi file profil.
func RegistriesJSON(rs Registries) string {
	if rs.Items == nil {
		rs.Items = []Registry{}
	}
	b, _ := json.MarshalIndent(rs, "", "  ")
	return string(b) + "\n"
}

// SaveRegistriesPlan menulis file profil (0600, hanya bisa dibaca pemiliknya).
func SaveRegistriesPlan(path string, rs Registries, title string) run.Plan {
	return run.Single(run.Command{
		Title: title, Argv: []string{"install", "-D", "-m", "0600", "/dev/stdin", path},
		Stdin: RegistriesJSON(rs), StdinLabel: fmt.Sprintf("(%d profil registry)", len(rs.Items)),
		Explain: []run.Line{
			{Token: "install -D -m 0600", Meaning: "tulis file (buat direktorinya bila perlu) hanya bisa dibaca kamu"},
			{Token: "/dev/stdin", Meaning: "isi file diambil dari teks di bawah"},
			{Token: path, Meaning: "catatan profil registry milik ubt"},
		},
		Effect: "Hanya catatan alamat & username. Password tidak pernah disimpan ubt.",
		Risk:   risk.Safe,
	})
}

// LoginPlan menjalankan `docker login`. Password diketik langsung ke docker (interaktif) dan
// tidak pernah lewat ubt.
func (c Client) LoginPlan(r Registry) run.Plan {
	args := []string{"login", r.Host}
	explain := []run.Line{
		{Token: "docker login", Meaning: "simpan kredensial registry di ~/.docker/config.json milikmu"},
		{Token: r.Host, Meaning: "alamat registry yang dituju"},
	}
	if r.User != "" {
		args = append(args, "-u", r.User)
		explain = append(explain, run.Line{Token: "-u " + r.User, Meaning: "username; password diketik langsung ke docker, ubt tidak melihat maupun menyimpannya"})
	}
	cmd := c.cmd("Login ke registry "+r.Name, risk.Caution,
		"Kredensial tersimpan di ~/.docker/config.json (base64, bukan enkripsi) sampai kamu logout. Setelah ini push & pull tidak perlu login lagi.",
		explain, args...)
	cmd.Interactive = true
	cmd.Safer = "Pakai akun dengan hak seperlunya (mis. deploy user), bukan akun admin Nexus."
	return run.Single(cmd)
}

// LogoutPlan menghapus kredensial registry dari ~/.docker/config.json.
func (c Client) LogoutPlan(host string) run.Plan {
	return run.Single(c.cmd("Logout dari "+host, risk.Safe, "Push & pull ke registry ini butuh login lagi.",
		[]run.Line{{Token: "docker logout", Meaning: "hapus kredensial registry dari ~/.docker/config.json"}}, "logout", host))
}

// PushPlan memberi tag registry pada image lokal lalu mengunggahnya.
func (c Client) PushPlan(local, target string) run.Plan {
	steps := []run.Command{}
	if local != target {
		steps = append(steps, c.cmd("Beri tag "+target, risk.Safe,
			"Image yang sama mendapat nama kedua; tidak ada salinan baru di disk.",
			[]run.Line{
				{Token: "docker tag", Meaning: "beri nama lain pada image yang sama"},
				{Token: local, Meaning: "nama image lokal sekarang"},
				{Token: target, Meaning: "nama lengkap di registry: host/repository/nama:tag"},
			}, "tag", local, target))
	}
	push := c.cmd("Unggah "+target, risk.Caution,
		"Layer yang belum ada di registry diunggah; yang sudah ada dilewati. Tag yang sama di registry akan ditimpa.",
		[]run.Line{
			{Token: "docker push", Meaning: "unggah image ke registry sesuai awalan host di namanya"},
			{Token: target, Meaning: "tujuan unggahan"},
		}, "push", target)
	push.Safer = "Pakai tag versi (mis. :1.4.2) selain :latest, supaya bisa kembali ke versi sebelumnya."
	steps = append(steps, push)
	return run.Plan{Title: "Push " + target, Steps: steps}
}

// CertPath adalah lokasi sertifikat CA per registry yang dibaca docker.
func CertPath(host string) string { return "/etc/docker/certs.d/" + run.FileName(host) + "/ca.crt" }

// InstallCAPlan menyalin sertifikat CA internal supaya docker mempercayai registry HTTPS self-signed.
func InstallCAPlan(host, src string) run.Plan {
	dst := CertPath(host)
	cmd := run.Command{
		Title: "Pasang sertifikat CA untuk " + host, Argv: []string{"install", "-D", "-m", "0644", src, dst}, NeedsRoot: true,
		Explain: []run.Line{
			{Token: "install -D -m 0644", Meaning: "salin file (buat direktorinya bila perlu) dengan izin baca untuk semua"},
			{Token: src, Meaning: "file sertifikat CA dari tim infrastruktur (format PEM, diawali -----BEGIN CERTIFICATE-----)"},
			{Token: dst, Meaning: "docker membaca CA per registry dari direktori ini"},
		},
		Effect: "Docker langsung memakainya untuk registry " + host + " tanpa restart daemon. Ini hanya berlaku untuk docker; curl/browser punya trust store sendiri.",
		Risk:   risk.Caution,
		Safer:  "Minta sertifikat CA (bukan sertifikat server) ke tim infrastruktur, dan pastikan sidik jarinya benar: openssl x509 -in " + src + " -noout -fingerprint -sha256",
	}
	return run.Single(cmd)
}

// DaemonJSONPath adalah konfigurasi daemon docker.
const DaemonJSONPath = "/etc/docker/daemon.json"

// ReadDaemonJSON membaca /etc/docker/daemon.json. File yang belum ada menghasilkan map kosong.
func ReadDaemonJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("%s bukan JSON yang valid: %w", path, err)
	}
	return cfg, nil
}

// WithInsecureRegistry mengembalikan salinan cfg dengan host ditambahkan ke insecure-registries.
func WithInsecureRegistry(cfg map[string]any, host string) map[string]any {
	out := map[string]any{}
	for k, v := range cfg {
		out[k] = v
	}
	var list []any
	if cur, ok := out["insecure-registries"].([]any); ok {
		for _, x := range cur {
			if s, ok := x.(string); ok && s == host {
				return out // sudah ada
			}
			list = append(list, x)
		}
	}
	out["insecure-registries"] = append(list, host)
	return out
}

// DaemonJSON merender daemon.json.
func DaemonJSON(cfg map[string]any) string {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return string(b) + "\n"
}

// InsecureRegistryPlan menambahkan registry HTTP ke daemon.json lalu me-restart docker.
// Restart docker menghentikan semua container sebentar, jadi selalu berisiko tinggi.
func InsecureRegistryPlan(host string, cfg map[string]any, exists bool) run.Plan {
	steps := []run.Command{}
	if exists {
		steps = append(steps, run.Command{
			Title: "Cadangkan daemon.json", Argv: []string{"cp", "-a", DaemonJSONPath, DaemonJSONPath + ".ubt.bak"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "cp -a", Meaning: "salin apa adanya (izin & waktu ikut) sebagai cadangan sebelum diubah"}},
			Effect:  "Bila docker gagal menyala, kembalikan dengan: sudo mv " + DaemonJSONPath + ".ubt.bak " + DaemonJSONPath,
			Risk:    risk.Safe,
		})
	}
	content := DaemonJSON(WithInsecureRegistry(cfg, host))
	steps = append(steps, run.Command{
		Title: "Tulis " + DaemonJSONPath, Argv: []string{"install", "-D", "-m", "0644", "/dev/stdin", DaemonJSONPath}, NeedsRoot: true,
		Stdin: content, StdinLabel: "(daemon.json)",
		Explain: []run.Line{
			{Token: "install -D -m 0644", Meaning: "tulis konfigurasi daemon docker"},
			{Token: "insecure-registries", Meaning: "daftar registry yang boleh diakses tanpa HTTPS; isi file ditampilkan di bawah"},
		},
		Effect: "Isi lama dipertahankan, hanya " + host + " yang ditambahkan.",
		Risk:   risk.Caution,
	}, run.Command{
		Title: "Restart daemon Docker", Argv: []string{"systemctl", "restart", "docker"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "systemctl restart docker", Meaning: "daemon membaca daemon.json hanya saat start"}},
		Effect:  "SEMUA container berhenti sebentar; yang restart-nya always/unless-stopped menyala lagi sendiri, sisanya harus dijalankan manual.",
		Risk:    risk.Dangerous,
		Safer:   "Pakai HTTPS (walau dengan CA internal) alih-alih HTTP: tanpa TLS, password dan isi image lewat jaringan tanpa perlindungan.",
	})
	return run.Plan{Title: "Izinkan registry HTTP " + host, Steps: steps}
}

// RegistryHint menerjemahkan pesan error docker saat push/pull menjadi saran yang bisa dikerjakan.
func RegistryHint(output string) string {
	s := strings.ToLower(output)
	switch {
	case strings.Contains(s, "x509: certificate signed by unknown authority"), strings.Contains(s, "certificate is not trusted"):
		return "Sertifikat registry tidak dipercaya docker. Pasang sertifikat CA internal lewat menu registry (tombol c)."
	case strings.Contains(s, "http: server gave http response to https client"):
		return "Registry ini melayani HTTP polos. Tandai profilnya sebagai HTTP di menu registry supaya ubt menambahkannya ke insecure-registries."
	case strings.Contains(s, "unauthorized"), strings.Contains(s, "authentication required"), strings.Contains(s, "denied: "):
		return "Belum login atau akun tidak punya hak tulis ke repository ini. Jalankan login di menu registry (tombol l)."
	case strings.Contains(s, "no such host"), strings.Contains(s, "no route to host"), strings.Contains(s, "i/o timeout"):
		return "Host registry tidak bisa dihubungi dari server ini. Cek nama host, DNS, dan firewall di modul Network."
	case strings.Contains(s, "manifest unknown"), strings.Contains(s, "not found"):
		return "Nama atau tag image tidak ada di registry. Cek daftar tag di antarmuka web Nexus."
	}
	return ""
}
