// Package docker membaca kondisi Docker (ketersediaan, container, image, pemakaian disk) lewat
// `docker ... --format '{{json .}}'` dan membangun command untuk mengelolanya. Murni pengumpul data.
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Availability adalah hasil pengecekan apakah Docker bisa dipakai.
type Availability int

const (
	Ready        Availability = iota
	NotInstalled              // binary docker tidak ada
	DaemonDown                // binary ada, daemon tidak berjalan
	NoPermission              // daemon berjalan, user tidak boleh mengakses socket
	Unknown                   // gagal karena sebab lain
)

// Container adalah satu baris `docker ps -a`.
type Container struct {
	ID         string `json:"ID"`
	Names      string `json:"Names"`
	Image      string `json:"Image"`
	Command    string `json:"Command"`
	State      string `json:"State"`
	Status     string `json:"Status"`
	Ports      string `json:"Ports"`
	RunningFor string `json:"RunningFor"`
	Size       string `json:"Size"`
	Labels     string `json:"Labels"`
	Mounts     string `json:"Mounts"`
	Networks   string `json:"Networks"`
}

// Running melaporkan apakah container sedang berjalan (termasuk restarting/paused).
func (c Container) Running() bool {
	return c.State == "running" || c.State == "restarting" || c.State == "paused"
}

// Label mengembalikan nilai satu label.
func (c Container) Label(name string) string {
	for _, kv := range strings.Split(c.Labels, ",") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// ComposeProject adalah nama project compose yang mengelola container ini (kosong bila tidak ada).
func (c Container) ComposeProject() string { return c.Label("com.docker.compose.project") }

// Name adalah nama pertama container.
func (c Container) Name() string {
	name, _, _ := strings.Cut(c.Names, ",")
	return name
}

// Image adalah satu baris `docker images`.
type Image struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Size         string `json:"Size"`
	CreatedSince string `json:"CreatedSince"`
	Containers   string `json:"Containers"`
}

// Ref adalah nama image yang bisa dipakai di command: repo:tag, atau ID bila tanpa nama.
func (i Image) Ref() string {
	if i.Repository == "" || i.Repository == "<none>" {
		return i.ID
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

// Dangling melaporkan image tanpa nama (sisa build lama).
func (i Image) Dangling() bool { return i.Repository == "<none>" }

// DiskUsage adalah satu baris `docker system df`.
type DiskUsage struct {
	Type        string `json:"Type"`
	TotalCount  string `json:"TotalCount"`
	Active      string `json:"Active"`
	Size        string `json:"Size"`
	Reclaimable string `json:"Reclaimable"`
}

// Status adalah kondisi Docker secara keseluruhan.
type Status struct {
	Avail      Availability
	Bin        string
	Version    string
	Error      string // pesan asli docker bila gagal
	Compose    bool   // plugin `docker compose` tersedia
	Containers []Container
	Images     []Image
	Disk       []DiskUsage
}

func parseLines[T any](out string) []T {
	var items []T
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v T
		if json.Unmarshal([]byte(line), &v) == nil {
			items = append(items, v)
		}
	}
	return items
}

// ParseContainers membaca output `docker ps -a --format '{{json .}}'`. Baris rusak dilewati.
// Urutan: yang berjalan dulu, lalu per nama.
func ParseContainers(out string) []Container {
	cs := parseLines[Container](out)
	sort.SliceStable(cs, func(a, b int) bool {
		if cs[a].Running() != cs[b].Running() {
			return cs[a].Running()
		}
		return cs[a].Names < cs[b].Names
	})
	return cs
}

// ParseImages membaca output `docker images --format '{{json .}}'`.
func ParseImages(out string) []Image { return parseLines[Image](out) }

// ParseDiskUsage membaca output `docker system df --format '{{json .}}'`.
func ParseDiskUsage(out string) []DiskUsage { return parseLines[DiskUsage](out) }

// Classify menentukan jenis kegagalan dari pesan error docker.
func Classify(stderr string) Availability {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "permission denied"):
		return NoPermission
	case strings.Contains(s, "cannot connect to the docker daemon"), strings.Contains(s, "is the docker daemon running"),
		strings.Contains(s, "no such file or directory") && strings.Contains(s, "docker.sock"):
		return DaemonDown
	}
	return Unknown
}

// Client menjalankan command docker, opsional lewat sudo (saat user belum masuk grup docker).
type Client struct {
	Bin  string
	Sudo bool
}

// NewClient mencari binary docker di PATH.
func NewClient() Client {
	bin, err := exec.LookPath("docker")
	if err != nil {
		return Client{}
	}
	return Client{Bin: bin}
}

// Command membuat command docker dengan argumen tertentu.
func (c Client) Command(args ...string) run.Command {
	return run.Command{Argv: append([]string{"docker"}, args...), NeedsRoot: c.Sudo}
}

// Read membaca status, container, image, dan pemakaian disk.
func (c Client) Read(ctx context.Context, r run.Runner) Status {
	st := Status{Bin: c.Bin}
	if c.Bin == "" {
		st.Avail = NotInstalled
		return st
	}
	if _, err := os.Stat(c.Bin); err != nil {
		st.Avail = NotInstalled
		return st
	}
	out, stderr, err := r.Capture(ctx, c.Command("info", "--format", "{{json .ServerVersion}}"))
	if err != nil {
		st.Avail = Classify(stderr + err.Error())
		if c.Sudo && strings.Contains(stderr, "sudo") {
			st.Avail = NoPermission
		}
		st.Error = strings.TrimSpace(firstLine(stderr))
		return st
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &st.Version)
	if out, _, err := r.Capture(ctx, c.Command("ps", "-a", "--no-trunc", "--format", "{{json .}}")); err == nil {
		st.Containers = ParseContainers(out)
	}
	if out, _, err := r.Capture(ctx, c.Command("images", "--format", "{{json .}}")); err == nil {
		st.Images = ParseImages(out)
	}
	if out, _, err := r.Capture(ctx, c.Command("system", "df", "--format", "{{json .}}")); err == nil {
		st.Disk = ParseDiskUsage(out)
	}
	_, _, err = r.Capture(ctx, c.Command("compose", "version"))
	st.Compose = err == nil
	return st
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// Logs membaca log container (baris terakhir, dengan timestamp).
func (c Client) Logs(ctx context.Context, r run.Runner, id string, tail int) (string, error) {
	cmd := c.Command("logs", "--tail", fmt.Sprint(tail), "--timestamps", id)
	out, stderr, err := r.Capture(ctx, cmd)
	// docker logs meneruskan stderr container ke stderr; gabungkan keduanya.
	combined := strings.TrimRight(out, "\n")
	if s := strings.TrimRight(stderr, "\n"); s != "" && err == nil {
		lines := append(strings.Split(combined, "\n"), strings.Split(s, "\n")...)
		sort.SliceStable(lines, func(a, b int) bool { return lines[a] < lines[b] })
		combined = strings.TrimLeft(strings.Join(lines, "\n"), "\n")
	}
	if err != nil {
		return combined, errors.New(strings.TrimSpace(stderr + " " + err.Error()))
	}
	return combined, nil
}

var (
	nameRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	imageRe  = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(:[0-9]+)?(/[a-z0-9]+([._-]+[a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(@sha256:[a-f0-9]{64})?$`)
	moduleRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

// ValidImage memeriksa nama image seperti `nginx`, `python:3.12-slim`, `ghcr.io/org/app:1.0`.
func ValidImage(s string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return errors.New("nama image wajib diisi")
	case strings.ContainsAny(s, " \t"):
		return errors.New("nama image tidak boleh berisi spasi")
	case !imageRe.MatchString(s):
		return errors.New("format image tidak valid; contoh: nginx:alpine atau ghcr.io/org/app:1.0 (huruf kecil)")
	}
	return nil
}

// ValidName memeriksa nama container/service.
func ValidName(s string) error {
	if !nameRe.MatchString(strings.TrimSpace(s)) {
		return errors.New("gunakan huruf, angka, titik, garis bawah, atau strip; diawali huruf/angka")
	}
	return nil
}

// ValidModule memeriksa nama modul Python seperti `app` atau `paket.main`.
func ValidModule(s string) error {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".py") || strings.Contains(s, "/") {
		return errors.New("tulis nama modul, bukan path file: app/main.py → app.main")
	}
	if !moduleRe.MatchString(s) {
		return errors.New("nama modul Python tidak valid; contoh: app atau paket.main (tanpa .py)")
	}
	return nil
}

// --- Plan -----------------------------------------------------------------------------------------

func (c Client) cmd(title string, r risk.Level, effect string, explain []run.Line, args ...string) run.Command {
	cmd := c.Command(args...)
	cmd.Title, cmd.Risk, cmd.Effect, cmd.Explain = title, r, effect, explain
	return cmd
}

// InstallPlan memasang Docker dari repository Ubuntu.
func InstallPlan(user string) run.Plan {
	return run.Plan{Title: "Install Docker", Steps: []run.Command{
		{Title: "Install docker.io dan plugin compose", Argv: []string{"apt-get", "install", "-y", "docker.io", "docker-compose-v2"}, NeedsRoot: true, Risk: risk.Caution,
			Explain: []run.Line{
				{Token: "docker.io", Meaning: "Docker Engine dari repository resmi Ubuntu"},
				{Token: "docker-compose-v2", Meaning: "perintah `docker compose` untuk menjalankan beberapa container dari satu file"},
			},
			Effect: "Daemon docker langsung berjalan dan aktif otomatis saat boot."},
		AddGroupCommand(user),
	}}
}

// AddGroupCommand memasukkan user ke grup docker.
func AddGroupCommand(user string) run.Command {
	return run.Command{Title: "Izinkan " + user + " memakai docker tanpa sudo", Argv: []string{"usermod", "-aG", "docker", user}, NeedsRoot: true, Risk: risk.Caution,
		Explain: []run.Line{
			{Token: "usermod -aG docker", Meaning: "tambahkan (-a) ke grup (-G) docker tanpa menghapus grup lain"},
			{Token: user, Meaning: "user yang diberi akses"},
		},
		Effect: "Berlaku setelah logout lalu login lagi (atau jalankan `newgrp docker` di terminal). Catatan: anggota grup docker praktis setara root di server ini.",
	}
}

// StartDaemonPlan menyalakan daemon docker.
func StartDaemonPlan() run.Plan {
	return run.Single(run.Command{Title: "Nyalakan daemon Docker", Argv: []string{"systemctl", "enable", "--now", "docker"}, NeedsRoot: true, Risk: risk.Caution,
		Explain: []run.Line{{Token: "enable --now", Meaning: "jalankan sekarang dan otomatis saat boot"}},
		Effect:  "Bila gagal, lihat alasannya dengan: journalctl -u docker -n 50"})
}

// StartPlan, StopPlan, RestartPlan untuk satu container.
func (c Client) StartPlan(ct Container) run.Plan {
	return run.Single(c.cmd("Jalankan container "+ct.Name(), risk.Caution, "Container berjalan lagi dengan konfigurasi yang sama.",
		[]run.Line{{Token: "docker start", Meaning: "jalankan container yang sudah ada"}}, "start", ct.Name()))
}

func (c Client) StopPlan(ct Container) run.Plan {
	p := run.Single(c.cmd("Hentikan container "+ct.Name(), risk.Caution, "Layanan di container berhenti; data di dalamnya tetap ada dan bisa dijalankan lagi (docker start).",
		[]run.Line{{Token: "docker stop", Meaning: "kirim SIGTERM, tunggu 10 detik, lalu SIGKILL bila belum berhenti"}}, "stop", ct.Name()))
	if proj := ct.ComposeProject(); proj != "" {
		p.Steps[0].Safer = "Container ini bagian project compose \"" + proj + "\"; untuk menghentikan semuanya pakai `docker compose stop` di direktori project."
	}
	return p
}

func (c Client) RestartPlan(ct Container) run.Plan {
	return run.Single(c.cmd("Restart container "+ct.Name(), risk.Caution, "Layanan terputus beberapa detik.",
		[]run.Line{{Token: "docker restart", Meaning: "stop lalu start lagi"}}, "restart", ct.Name()))
}

// RemovePlan menghapus container. Container berjalan dihentikan dulu.
func (c Client) RemovePlan(ct Container) run.Plan {
	args := []string{"rm", ct.Name()}
	explain := []run.Line{{Token: "docker rm", Meaning: "hapus container beserta isi filesystem-nya (volume tidak ikut terhapus)"}}
	lvl := risk.Caution
	if ct.Running() {
		args = []string{"rm", "-f", ct.Name()}
		explain = append(explain, run.Line{Token: "-f", Meaning: "paksa: hentikan dulu container yang sedang berjalan"})
		lvl = risk.Dangerous
	}
	cmd := c.cmd("Hapus container "+ct.Name(), lvl, "File yang dibuat di dalam container (di luar volume) hilang permanen. Image tidak ikut terhapus.", explain, args...)
	cmd.Safer = "Simpan dulu isinya jadi image dengan commit (tombol m), atau cukup hentikan (tombol s)."
	return run.Single(cmd)
}

// CommitPlan menyimpan kondisi container menjadi image baru.
func (c Client) CommitPlan(ct Container, image string) run.Plan {
	return run.Single(c.cmd("Simpan container "+ct.Name()+" jadi image "+image, risk.Safe,
		"Image baru berisi semua perubahan file di container (tanpa isi volume). Jalankan dengan: docker run -it "+image,
		[]run.Line{
			{Token: "docker commit", Meaning: "potret filesystem container menjadi image"},
			{Token: ct.Name(), Meaning: "container sumber"},
			{Token: image, Meaning: "nama image baru"},
		}, "commit", ct.Name(), image))
}

// shellScript memilih bash bila ada, bila tidak sh.
const shellScript = `if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi`

// ExecShellPlan membuka shell interaktif di container yang berjalan.
func (c Client) ExecShellPlan(ct Container) run.Plan {
	cmd := c.cmd("Shell di container "+ct.Name(), risk.Caution, "Kamu masuk ke dalam container. Ketik `exit` untuk kembali ke ubt. Perubahan file hilang bila container dihapus, kecuali disimpan dengan commit.",
		[]run.Line{
			{Token: "docker exec -it", Meaning: "jalankan program di container yang berjalan dengan terminal interaktif"},
			{Token: "sh -c '…'", Meaning: "pakai bash bila ada di image, bila tidak pakai sh"},
		}, "exec", "-it", ct.Name(), "sh", "-c", shellScript)
	cmd.Interactive = true
	return run.Single(cmd)
}

// RunShellPlan menjalankan container sementara dari image dan membuka shell di dalamnya.
func (c Client) RunShellPlan(image string, keep bool, name string) run.Plan {
	args := []string{"run", "-it"}
	explain := []run.Line{{Token: "docker run -it", Meaning: "buat container baru dengan terminal interaktif"}}
	effect := "Container dihapus otomatis saat kamu keluar (exit), jadi perubahan tidak tersimpan."
	if keep {
		args = append(args, "--name", name)
		explain = append(explain, run.Line{Token: "--name " + name, Meaning: "container disimpan setelah keluar; bisa di-commit jadi image"})
		effect = "Setelah exit, container " + name + " tetap ada (berhenti). Simpan perubahannya dengan commit di daftar container."
	} else {
		args = append(args, "--rm")
		explain = append(explain, run.Line{Token: "--rm", Meaning: "hapus container otomatis setelah keluar"})
	}
	args = append(args, image, "sh", "-c", shellScript)
	explain = append(explain, run.Line{Token: "sh -c '…'", Meaning: "pakai bash bila ada di image, bila tidak pakai sh"})
	cmd := c.cmd("Shell di image "+image, risk.Caution, effect+" Bila image belum ada, docker mengunduhnya dulu.", explain, args...)
	cmd.Interactive = true
	return run.Single(cmd)
}

// PullPlan mengunduh image.
func (c Client) PullPlan(image string) run.Plan {
	return run.Single(c.cmd("Unduh image "+image, risk.Safe, "Image tersimpan lokal dan memakai ruang disk.",
		[]run.Line{{Token: "docker pull", Meaning: "unduh image dari registry (default Docker Hub)"}}, "pull", image))
}

// RemoveImagePlan menghapus image.
func (c Client) RemoveImagePlan(img Image) run.Plan {
	cmd := c.cmd("Hapus image "+img.Ref(), risk.Caution, "Ruang disk kembali. Image bisa diunduh lagi dengan docker pull, kecuali image buatan sendiri (hasil build/commit).",
		[]run.Line{{Token: "docker rmi", Meaning: "hapus image; ditolak bila masih dipakai container"}}, "rmi", img.Ref())
	return run.Single(cmd)
}

// PrunePlan membersihkan sumber daya tak terpakai.
func (c Client) PrunePlan(all, volumes bool) run.Plan {
	args := []string{"system", "prune", "-f"}
	explain := []run.Line{
		{Token: "docker system prune", Meaning: "hapus container yang berhenti, network tak terpakai, image tanpa nama, dan build cache"},
		{Token: "-f", Meaning: "tanpa bertanya lagi (sudah dikonfirmasi di ubt)"},
	}
	lvl := risk.Caution
	if all {
		args = append(args, "-a")
		explain = append(explain, run.Line{Token: "-a", Meaning: "juga hapus SEMUA image yang tidak dipakai container, bukan hanya yang tanpa nama"})
	}
	if volumes {
		args = append(args, "--volumes")
		explain = append(explain, run.Line{Token: "--volumes", Meaning: "juga hapus volume yang tidak dipakai — data database di volume ikut hilang"})
		lvl = risk.Dangerous
	}
	cmd := c.cmd("Bersihkan Docker", lvl, "Container yang berhenti ikut terhapus. Container yang sedang berjalan dan image-nya aman.", explain, args...)
	cmd.Safer = "Lihat dulu apa yang memakan tempat: docker system df -v"
	return run.Single(cmd)
}

// PythonRunPlan menjalankan modul Python dari direktori host di dalam container.
// uid/gid dipakai supaya file yang ditulis program di direktori proyek tidak menjadi milik root.
func (c Client) PythonRunPlan(image, dir, module string, uid, gid int) run.Plan {
	user := fmt.Sprintf("%d:%d", uid, gid)
	cmd := c.cmd("Jalankan python -m "+module+" di container", risk.Caution,
		"Direktori "+dir+" dipasang ke /app, jadi file yang ditulis program ikut tersimpan di host. Container dihapus setelah selesai.",
		[]run.Line{
			{Token: "--rm -it", Meaning: "container sementara dengan terminal interaktif"},
			{Token: "-v " + dir + ":/app", Meaning: "pasang direktori host ke /app di container"},
			{Token: "-w /app", Meaning: "jadikan /app direktori kerja"},
			{Token: "--user " + user, Meaning: "jalankan sebagai user kamu, supaya file yang dibuat bukan milik root"},
			{Token: "-e HOME=/tmp", Meaning: "direktori home yang bisa ditulis oleh user tersebut di dalam container"},
			{Token: image, Meaning: "image Python yang dipakai"},
			{Token: "python -m " + module, Meaning: "jalankan modul (" + strings.ReplaceAll(module, ".", "/") + ".py atau paket dengan __main__.py)"},
		}, "run", "--rm", "-it", "-v", dir+":/app", "-w", "/app", "--user", user, "-e", "HOME=/tmp", image, "python", "-m", module)
	cmd.Interactive = true
	cmd.Safer = "Bila butuh paket dari requirements.txt, buat Dockerfile lewat wizard (tombol n) agar tidak install ulang setiap kali."
	return run.Single(cmd)
}

// ComposeUpPlan menjalankan docker compose up -d di direktori.
func (c Client) ComposeUpPlan(file string) run.Plan {
	return run.Single(c.cmd("Jalankan compose "+file, risk.Caution, "Semua service di file dijalankan di latar belakang. Lihat statusnya di daftar container.",
		[]run.Line{
			{Token: "docker compose -f " + file, Meaning: "pakai file compose ini"},
			{Token: "up -d", Meaning: "buat & jalankan service di latar belakang (detached)"},
		}, "compose", "-f", file, "up", "-d"))
}
