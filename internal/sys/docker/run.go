package docker

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// RunSpec adalah "resep" satu container: semua yang dijawab user di wizard `docker run`.
// Resep disimpan supaya container yang sama bisa dibuat ulang setelah image diperbarui.
type RunSpec struct {
	Name        string    `json:"name"`
	Image       string    `json:"image"`
	Ports       []string  `json:"ports,omitempty"`       // "127.0.0.1:8080:80"
	Volumes     []string  `json:"volumes,omitempty"`     // "dbdata:/var/lib/postgresql/data"
	Env         []string  `json:"env,omitempty"`         // "KEY=nilai"
	EnvFile     string    `json:"env_file,omitempty"`    // berkas berisi KEY=nilai, untuk rahasia
	Workdir     string    `json:"workdir,omitempty"`     // -w
	Command     []string  `json:"command,omitempty"`     // command pengganti CMD image
	User        string    `json:"user,omitempty"`        // "1000:1000"
	Network     string    `json:"network,omitempty"`     // nama network docker
	Restart     string    `json:"restart,omitempty"`     // no | on-failure | unless-stopped | always
	Memory      string    `json:"memory,omitempty"`      // "512m"
	CPUs        string    `json:"cpus,omitempty"`        // "1.5"
	HealthCmd   string    `json:"health_cmd,omitempty"`  // command cek sehat di dalam container
	Detach      bool      `json:"detach"`                // jalan di latar belakang (-d)
	AutoRemove  bool      `json:"auto_remove,omitempty"` // --rm
	Interactive bool      `json:"interactive,omitempty"` // -it
	Updated     time.Time `json:"updated,omitempty"`
}

// Recipes adalah isi file resep container.
type Recipes struct {
	Items []RunSpec `json:"containers"`
}

// Find mencari resep berdasarkan nama container.
func (rs Recipes) Find(name string) (RunSpec, bool) {
	for _, r := range rs.Items {
		if r.Name == name {
			return r, true
		}
	}
	return RunSpec{}, false
}

// Upsert menambah atau mengganti resep dengan nama container yang sama.
func (rs Recipes) Upsert(s RunSpec) Recipes {
	out := make([]RunSpec, 0, len(rs.Items)+1)
	for _, x := range rs.Items {
		if x.Name != s.Name {
			out = append(out, x)
		}
	}
	out = append(out, s)
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return Recipes{Items: out}
}

// Remove menghapus resep bernama name.
func (rs Recipes) Remove(name string) Recipes {
	out := make([]RunSpec, 0, len(rs.Items))
	for _, x := range rs.Items {
		if x.Name != name {
			out = append(out, x)
		}
	}
	return Recipes{Items: out}
}

// DefaultRecipePath adalah lokasi file resep: $XDG_CONFIG_HOME/ubt/containers.json.
func DefaultRecipePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ubt", "containers.json"), nil
}

// LoadRecipes membaca file resep. File yang belum ada bukan error.
func LoadRecipes(path string) (Recipes, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Recipes{}, nil
	}
	if err != nil {
		return Recipes{}, err
	}
	var rs Recipes
	if err := json.Unmarshal(b, &rs); err != nil {
		return Recipes{}, fmt.Errorf("%s rusak: %w", path, err)
	}
	return rs, nil
}

// RecipesJSON merender isi file resep.
func RecipesJSON(rs Recipes) string {
	if rs.Items == nil {
		rs.Items = []RunSpec{}
	}
	b, _ := json.MarshalIndent(rs, "", "  ")
	return string(b) + "\n"
}

// SaveRecipesCommand menulis file resep. Ditandai sensitif bila ada environment inline, supaya
// isinya tidak ikut tercatat di riwayat perintah.
func SaveRecipesCommand(path string, rs Recipes) run.Command {
	secret := false
	for _, s := range rs.Items {
		if len(s.Env) > 0 {
			secret = true
		}
	}
	return run.Command{
		Title: "Catat resep container di " + path, Argv: []string{"install", "-D", "-m", "0600", "/dev/stdin", path},
		Stdin: RecipesJSON(rs), StdinLabel: fmt.Sprintf("(%d resep container)", len(rs.Items)), Sensitive: secret,
		Explain: []run.Line{
			{Token: "install -D -m 0600", Meaning: "tulis file (buat direktorinya bila perlu) hanya bisa dibaca kamu"},
			{Token: path, Meaning: "catatan ubt berisi setting container, supaya bisa dibuat ulang saat image diperbarui"},
		},
		Effect: "Tidak mengubah container apa pun; hanya catatan di direktori konfigurasimu.",
		Risk:   risk.Safe,
	}
}

// --- validasi -------------------------------------------------------------------------------------

var (
	memRe      = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[bkmgBKMG]$`)
	userSpecRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]*(:[a-z_][a-z0-9_-]*)?$|^[0-9]+(:[0-9]+)?$`)
)

// ValidWorkdir memeriksa direktori kerja di dalam container (harus absolut).
func ValidWorkdir(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "/") {
		return errors.New("direktori kerja di dalam container harus diawali /, mis. /app")
	}
	return nil
}

// ValidMemory memeriksa batas memori seperti 512m atau 2g.
func ValidMemory(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !memRe.MatchString(s) {
		return errors.New("format: angka + satuan b/k/m/g, mis. 512m atau 2g")
	}
	return nil
}

// ValidCPUs memeriksa batas CPU seperti 0.5 atau 2.
func ValidCPUs(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 || n > 1024 {
		return errors.New("tulis jumlah inti CPU, mis. 0.5 atau 2")
	}
	return nil
}

// ValidUserSpec memeriksa --user: "1000", "1000:1000", atau "app:app".
func ValidUserSpec(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !userSpecRe.MatchString(s) {
		return errors.New("tulis UID atau UID:GID (mis. 1000:1000), atau nama user di dalam container")
	}
	return nil
}

// ValidEnvFile memeriksa berkas environment.
func ValidEnvFile(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	fi, err := os.Stat(s)
	if err != nil || fi.IsDir() {
		return errors.New("file tidak ditemukan: " + s)
	}
	return nil
}

// ErrUnterminatedQuote dikembalikan SplitCommand bila tanda kutip tidak ditutup.
var ErrUnterminatedQuote = errors.New("tanda kutip tidak ditutup")

// SplitCommand memecah satu baris command menjadi argumen seperti yang dilakukan shell terhadap
// spasi dan tanda kutip — TANPA menjalankan shell, jadi $(...) dan | tetap menjadi teks biasa.
func SplitCommand(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inWord, quote := false, byte(0)
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case quote != 0 && ch == quote:
			quote = 0
		case quote != 0:
			cur.WriteByte(ch)
		case ch == '\'' || ch == '"':
			quote, inWord = ch, true
		case ch == ' ' || ch == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			inWord = true
			cur.WriteByte(ch)
		}
	}
	if quote != 0 {
		return nil, ErrUnterminatedQuote
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

// ValidCommand memeriksa command pengganti CMD image.
func ValidCommand(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	_, err := SplitCommand(s)
	return err
}

// Validate memeriksa kombinasi setting yang tidak mungkin dijalankan docker.
func (s RunSpec) Validate() error {
	if err := ValidName(s.Name); err != nil {
		return fmt.Errorf("nama container: %w", err)
	}
	if err := ValidImage(s.Image); err != nil {
		return err
	}
	if s.AutoRemove && s.Restart != "" && s.Restart != "no" {
		return errors.New("--rm tidak bisa digabung dengan restart otomatis: pilih salah satu")
	}
	if s.Detach && s.Interactive {
		return errors.New("container latar belakang tidak bisa sekaligus interaktif")
	}
	for _, p := range s.Ports {
		if err := ValidPortMapping(p); err != nil {
			return fmt.Errorf("port %q: %w", p, err)
		}
	}
	for _, v := range s.Volumes {
		if err := ValidVolume(v); err != nil {
			return fmt.Errorf("volume %q: %w", v, err)
		}
	}
	for _, e := range s.Env {
		if err := ValidEnv(e); err != nil {
			return fmt.Errorf("environment %q: %w", e, err)
		}
	}
	return nil
}

// Args membangun argumen `docker run` sesuai resep.
func (s RunSpec) Args() []string {
	args := []string{"run"}
	switch {
	case s.Detach:
		args = append(args, "-d")
	case s.Interactive:
		args = append(args, "-it")
	}
	if s.AutoRemove {
		args = append(args, "--rm")
	}
	args = append(args, "--name", s.Name)
	for _, p := range s.Ports {
		args = append(args, "-p", p)
	}
	for _, v := range s.Volumes {
		args = append(args, "-v", v)
	}
	if s.EnvFile != "" {
		args = append(args, "--env-file", s.EnvFile)
	}
	for _, e := range s.Env {
		args = append(args, "-e", e)
	}
	if s.Workdir != "" {
		args = append(args, "-w", s.Workdir)
	}
	if s.User != "" {
		args = append(args, "--user", s.User)
	}
	if s.Network != "" {
		args = append(args, "--network", s.Network)
	}
	if s.Restart != "" && s.Restart != "no" {
		args = append(args, "--restart", s.Restart)
	}
	if s.Memory != "" {
		args = append(args, "-m", s.Memory)
	}
	if s.CPUs != "" {
		args = append(args, "--cpus", s.CPUs)
	}
	if s.HealthCmd != "" {
		args = append(args, "--health-cmd", s.HealthCmd, "--health-interval", "30s", "--health-retries", "3")
	}
	args = append(args, s.Image)
	return append(args, s.Command...)
}

// explainRun menjelaskan tiap bagian `docker run` yang dipakai resep ini.
func (s RunSpec) explain() []run.Line {
	lines := []run.Line{{Token: "docker run", Meaning: "buat container baru dari image lalu jalankan"}}
	switch {
	case s.Detach:
		lines = append(lines, run.Line{Token: "-d", Meaning: "jalan di latar belakang; terminal langsung kembali ke ubt"})
	case s.Interactive:
		lines = append(lines, run.Line{Token: "-it", Meaning: "terminal interaktif: kamu bisa mengetik di dalam container"})
	}
	if s.AutoRemove {
		lines = append(lines, run.Line{Token: "--rm", Meaning: "container dihapus otomatis begitu programnya berhenti"})
	}
	lines = append(lines, run.Line{Token: "--name " + s.Name, Meaning: "nama container; dipakai di command lain dan sebagai hostname antar container di network yang sama"})
	for _, p := range s.Ports {
		meaning := "port di server ini diteruskan ke port di dalam container"
		if strings.HasPrefix(p, "127.0.0.1:") {
			meaning += "; hanya bisa diakses dari server ini"
		} else {
			meaning += "; TERBUKA ke jaringan — docker membuat aturannya sendiri dan melewati ufw"
		}
		lines = append(lines, run.Line{Token: "-p " + p, Meaning: meaning})
	}
	for _, v := range s.Volumes {
		src, _, _ := strings.Cut(v, ":")
		meaning := "volume bernama " + src + " dikelola docker; datanya tetap ada walau container dihapus"
		if strings.HasPrefix(src, "/") || strings.HasPrefix(src, ".") || strings.HasPrefix(src, "~") {
			meaning = "direktori " + src + " di server dipasang ke dalam container; file yang ditulis container langsung terlihat di server"
		}
		lines = append(lines, run.Line{Token: "-v " + v, Meaning: meaning})
	}
	if s.EnvFile != "" {
		lines = append(lines, run.Line{Token: "--env-file " + s.EnvFile, Meaning: "baca daftar NAMA=nilai dari berkas; isinya tidak ikut tampil di layar maupun riwayat"})
	}
	for _, e := range s.Env {
		k, _, _ := strings.Cut(e, "=")
		lines = append(lines, run.Line{Token: "-e " + e, Meaning: "environment " + k + " di dalam container"})
	}
	if s.Workdir != "" {
		lines = append(lines, run.Line{Token: "-w " + s.Workdir, Meaning: "direktori kerja saat command dijalankan (seperti cd sebelum menjalankan program)"})
	}
	if s.User != "" {
		lines = append(lines, run.Line{Token: "--user " + s.User, Meaning: "jalankan sebagai user ini, bukan root di dalam container"})
	}
	if s.Network != "" {
		lines = append(lines, run.Line{Token: "--network " + s.Network, Meaning: "sambungkan ke network " + s.Network + "; container lain di network yang sama bisa memanggilnya lewat nama"})
	}
	if s.Restart != "" && s.Restart != "no" {
		lines = append(lines, run.Line{Token: "--restart " + s.Restart, Meaning: restartMeaning(s.Restart)})
	}
	if s.Memory != "" {
		lines = append(lines, run.Line{Token: "-m " + s.Memory, Meaning: "batas RAM; bila terlampaui, program di dalam container dimatikan kernel (OOM)"})
	}
	if s.CPUs != "" {
		lines = append(lines, run.Line{Token: "--cpus " + s.CPUs, Meaning: "batas pemakaian CPU dalam satuan inti"})
	}
	if s.HealthCmd != "" {
		lines = append(lines, run.Line{Token: "--health-cmd …", Meaning: "docker menjalankan command ini tiap 30 detik untuk menandai container sehat/tidak"})
	}
	lines = append(lines, run.Line{Token: s.Image, Meaning: "image yang dipakai; diunduh otomatis bila belum ada di server"})
	if len(s.Command) > 0 {
		lines = append(lines, run.Line{Token: run.JoinShell(s.Command), Meaning: "command yang dijalankan, menggantikan CMD bawaan image"})
	}
	return lines
}

func restartMeaning(policy string) string {
	switch policy {
	case "always":
		return "selalu dijalankan lagi setelah crash maupun reboot server"
	case "unless-stopped":
		return "dijalankan lagi setelah crash dan reboot, kecuali kamu menghentikannya sendiri"
	case "on-failure":
		return "dijalankan lagi hanya bila programnya berhenti dengan error"
	}
	return "kebijakan restart container"
}

// Effect merangkum apa yang terjadi setelah container dijalankan.
func (s RunSpec) Effect() string {
	var b strings.Builder
	if s.Detach {
		b.WriteString("Container berjalan di latar belakang. Lihat lognya dengan: docker logs -f " + s.Name + ".")
	} else {
		b.WriteString("Container berjalan di terminal ini sampai kamu keluar.")
	}
	if s.AutoRemove {
		b.WriteString(" Setelah berhenti, container langsung dihapus — data di luar volume hilang.")
	}
	if len(s.Volumes) == 0 {
		b.WriteString(" Tanpa volume, semua file yang ditulis program hilang saat container dihapus.")
	}
	return b.String()
}

// RunPlan menjalankan container sesuai resep. Bila recipePath tidak kosong, resep dicatat setelah
// container berhasil dijalankan.
func (c Client) RunPlan(s RunSpec, recipePath string, recipes Recipes) run.Plan {
	cmd := c.cmd("Jalankan container "+s.Name, risk.Caution, s.Effect(), s.explain(), s.Args()...)
	cmd.Interactive = s.Interactive
	if len(s.Env) > 0 {
		cmd.Safer = "Nilai environment di atas ikut terlihat di `docker inspect` dan riwayat ubt. Untuk password, pakai --env-file yang izinnya 0600."
	}
	steps := []run.Command{cmd}
	if recipePath != "" && s.Detach {
		s.Updated = time.Now()
		steps = append(steps, SaveRecipesCommand(recipePath, recipes.Upsert(s)))
	}
	return run.Plan{Title: "Jalankan container " + s.Name, Steps: steps}
}

// RecreatePlan membuat ulang container dari resep: unduh image terbaru, hentikan & hapus yang lama,
// lalu jalankan lagi dengan setting yang sama.
func (c Client) RecreatePlan(s RunSpec, pull bool, exists bool) run.Plan {
	var steps []run.Command
	if pull {
		steps = append(steps, c.cmd("Unduh image "+s.Image+" versi terbaru", risk.Safe,
			"Image lama tetap ada di disk sampai dibersihkan; container yang berjalan belum terpengaruh.",
			[]run.Line{{Token: "docker pull", Meaning: "ambil versi terbaru dari tag ini di registry"}}, "pull", s.Image))
	}
	if exists {
		steps = append(steps,
			c.cmd("Hentikan container "+s.Name, risk.Caution, "Layanan terputus mulai dari sini sampai container baru berjalan.",
				[]run.Line{{Token: "docker stop", Meaning: "kirim SIGTERM, tunggu 10 detik, lalu SIGKILL bila belum berhenti"}}, "stop", s.Name),
			c.cmd("Hapus container lama "+s.Name, risk.Dangerous,
				"File di dalam container lama (di luar volume) hilang permanen. Volume dan datanya tetap ada dan dipakai container baru.",
				[]run.Line{{Token: "docker rm", Meaning: "hapus container lama supaya namanya bisa dipakai container baru"}}, "rm", s.Name))
	}
	newCmd := c.cmd("Jalankan container baru "+s.Name, risk.Caution, s.Effect(), s.explain(), s.Args()...)
	newCmd.Safer = "Pastikan datanya ada di volume (bukan di dalam container) sebelum melanjutkan; cek dengan docker inspect " + s.Name
	steps = append(steps, newCmd)
	return run.Plan{Title: "Buat ulang container " + s.Name, Steps: steps}
}

// FromContainer membangun resep awal dari container yang sudah berjalan, sebagai titik mulai wizard.
// Hanya nama, image, dan port yang bisa dibaca dari `docker ps`; sisanya diisi user.
func FromContainer(ct Container) RunSpec {
	s := RunSpec{Name: ct.Name(), Image: ct.Image, Detach: true, Restart: "unless-stopped"}
	for _, p := range strings.Split(ct.Ports, ", ") {
		host, ctr, ok := strings.Cut(p, "->")
		if !ok {
			continue
		}
		proto := ""
		if i := strings.Index(ctr, "/"); i >= 0 {
			ctr = ctr[:i]
		}
		if strings.HasPrefix(host, "[::]") || strings.HasPrefix(host, "[::1]") {
			continue // duplikat dari baris IPv4
		}
		addr, port, found := strings.Cut(host, ":")
		if !found {
			continue
		}
		mapping := port + ":" + ctr + proto
		if addr != "0.0.0.0" {
			mapping = addr + ":" + mapping
		}
		s.Ports = append(s.Ports, mapping)
	}
	return s
}
