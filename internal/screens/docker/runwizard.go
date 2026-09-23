package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// listValidator menerapkan validator satu nilai ke daftar yang dipisah koma/baris baru.
func listValidator(v func(string) error) func(string) error {
	return func(s string) error {
		for _, item := range sysdocker.SplitList(s) {
			if err := v(item); err != nil {
				return fmt.Errorf("%q: %w", item, err)
			}
		}
		return nil
	}
}

// imageOptions memuat daftar image lokal sebagai pilihan.
func imageOptions(c sysdocker.Client, r run.Runner) func(context.Context) ([]ask.Option, error) {
	return func(ctx context.Context) ([]ask.Option, error) {
		out, _, err := r.Capture(ctx, c.Command("images", "--format", "{{json .}}"))
		if err != nil {
			return nil, err
		}
		var opts []ask.Option
		for _, img := range sysdocker.ParseImages(out) {
			if img.Dangling() {
				continue
			}
			opts = append(opts, ask.Option{Value: img.Ref(), Label: img.Ref(),
				Description: "sudah ada di server ini, dibuat " + img.CreatedSince, Meta: img.Size})
		}
		if len(opts) == 0 {
			return nil, nil
		}
		return opts, nil
	}
}

// networkOptions memuat daftar network sebagai pilihan.
func networkOptions(c sysdocker.Client, r run.Runner) func(context.Context) ([]ask.Option, error) {
	return func(ctx context.Context) ([]ask.Option, error) {
		opts := []ask.Option{{Value: "", Label: "Bawaan (bridge)", Description: "Container bisa keluar ke internet, tetapi tidak bisa memanggil container lain lewat nama.", Recommended: true}}
		for _, n := range c.ReadNetworks(ctx, r) {
			if n.Builtin() {
				continue
			}
			opts = append(opts, ask.Option{Value: n.Name, Label: n.Name,
				Description: "network " + n.Driver + "; container di dalamnya bisa saling memanggil lewat nama"})
		}
		return opts, nil
	}
}

// Nilai jawaban pertanyaan "cara menjalankan".
const (
	modeDetach = "detach"
	modeShell  = "shell"
	modeOnce   = "once"
)

// RunForm adalah wizard `docker run` bertahap: pertanyaan dasar dulu, setting lanjutan hanya
// ditanyakan bila user memintanya.
func RunForm(c sysdocker.Client, r run.Runner, start sysdocker.RunSpec) ask.Form {
	advanced := func(a ask.Answers) bool { return a["advanced"].Yes() }
	detached := func(a ask.Answers) bool { return a["mode"].Value() == modeDetach }
	envInline := func(a ask.Answers) bool { return a["envkind"].Value() == "inline" }
	envFile := func(a ask.Answers) bool { return a["envkind"].Value() == "file" }

	return ask.Form{ID: "run", Title: "Jalankan container baru", Questions: []ask.Question{
		{ID: "image", Header: "Image", Kind: ask.Single, Prompt: "Container dibuat dari image apa?",
			Help:  "Image adalah cetakan berisi sistem file + program. Kalau belum ada di server, docker mengunduhnya dari Docker Hub (atau registry di menu registry). Contoh: postgres:17, redis:7-alpine, nginx:alpine.",
			Other: true, Validate: sysdocker.ValidImage, Load: imageOptions(c, r), Default: def(start.Image),
			Options: []ask.Option{
				{Value: "nginx:alpine", Label: "nginx:alpine", Description: "Web server kecil untuk menyajikan file statis atau jadi proxy."},
				{Value: "postgres:17", Label: "postgres:17", Description: "Database PostgreSQL; wajib env POSTGRES_PASSWORD dan volume ke /var/lib/postgresql/data."},
				{Value: "redis:7-alpine", Label: "redis:7-alpine", Description: "Cache / antrian; port 6379."},
			}},
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama container?", Placeholder: "app", Validate: sysdocker.ValidName, Default: def(start.Name),
			Help: "Nama ini dipakai di semua command lain (docker logs NAMA, docker stop NAMA) dan menjadi hostname container ini bagi container lain di network yang sama."},
		{ID: "mode", Header: "Cara jalan", Kind: ask.Single, Prompt: "Container ini dijalankan bagaimana?", Options: []ask.Option{
			{Value: modeDetach, Label: "Latar belakang, hidup terus", Description: "Untuk layanan: web, database, antrian. Terminal langsung kembali ke ubt (-d).", Recommended: true},
			{Value: modeShell, Label: "Interaktif, saya ketik di dalamnya", Description: "Terminal masuk ke dalam container (-it). Container tetap ada setelah keluar."},
			{Value: modeOnce, Label: "Sekali jalan lalu hapus", Description: "Untuk mencoba-coba: container dihapus otomatis setelah selesai (--rm -it).", Risk: risk.Caution},
		}},
		{ID: "ports", Header: "Port", Kind: ask.Text, Optional: true, Validate: listValidator(sysdocker.ValidPortMapping),
			Prompt: "Port yang dibuka ke server (HOST:CONTAINER, pisahkan koma)?", Placeholder: "8080:80", Default: def(strings.Join(start.Ports, ", ")),
			Help: "Kiri = port di server ini, kanan = port yang didengarkan program di dalam container. Awali 127.0.0.1: (mis. 127.0.0.1:5432:5432) supaya hanya bisa diakses dari server ini — penting karena docker membuat aturan firewallnya sendiri dan MELEWATI ufw."},
		{ID: "volumes", Header: "Volume", Kind: ask.Text, Optional: true, Validate: listValidator(sysdocker.ValidVolume),
			Prompt: "Data disimpan di mana (SUMBER:/path/di/container)?", Placeholder: "dbdata:/var/lib/postgresql/data", Default: def(strings.Join(start.Volumes, ", ")),
			Help: "Tanpa ini, semua file yang ditulis program hilang begitu container dihapus. Sumber berupa nama (dbdata) = volume dikelola docker; berupa path (/srv/app/data) = direktori di server ini."},
		{ID: "envkind", Header: "Env", Kind: ask.Single, Prompt: "Apakah program butuh environment (mis. password database)?", Options: []ask.Option{
			{Value: "none", Label: "Tidak perlu", Description: "Lewati; kebanyakan image punya nilai bawaan yang cukup untuk mencoba.", Recommended: true},
			{Value: "inline", Label: "Ketik di sini", Description: "Cocok untuk nilai biasa. Nilainya ikut terlihat di layar konfirmasi, riwayat ubt, dan docker inspect."},
			{Value: "file", Label: "Dari berkas .env", Description: "Untuk password: isinya tidak ikut tampil di layar maupun riwayat. Buat berkas berisi baris NAMA=nilai."},
		}},
		{ID: "env", Header: "Env", Kind: ask.Text, When: envInline, Validate: listValidator(sysdocker.ValidEnv),
			Prompt: "Environment (NAMA=nilai, pisahkan koma)?", Placeholder: "POSTGRES_PASSWORD=ganti-ini",
			Help: "Cek halaman image di hub.docker.com untuk daftar environment yang dikenali."},
		{ID: "envfile", Header: "Berkas", Kind: ask.Text, When: envFile, Validate: sysdocker.ValidEnvFile,
			Prompt: "Path berkas environment?", Placeholder: "/srv/app/.env",
			Help: "Satu baris satu isian: NAMA=nilai, tanpa tanda kutip. Amankan dengan: chmod 600 BERKAS"},
		{ID: "restart", Header: "Restart", Kind: ask.Single, When: detached, Prompt: "Kalau container mati atau server reboot?", Options: []ask.Option{
			{Value: "unless-stopped", Label: "Hidupkan lagi otomatis", Description: "Hidup lagi setelah crash dan setelah server reboot, kecuali kamu sendiri yang menghentikannya.", Recommended: true},
			{Value: "on-failure", Label: "Hanya kalau error", Description: "Dijalankan lagi hanya bila program berhenti dengan error."},
			{Value: "no", Label: "Biarkan mati", Description: "Tidak pernah dijalankan ulang otomatis. Cocok untuk percobaan."},
		}},

		{ID: "advanced", Header: "Lanjutan", Kind: ask.Confirm, Default: []string{ask.ValueNo},
			Prompt: "Perlu setting lanjutan (direktori kerja, command, user, network, batas RAM/CPU, healthcheck)?",
			Options: []ask.Option{
				{Label: "Ya, tampilkan", Description: "Enam pertanyaan tambahan; semuanya boleh dikosongkan."},
				{Label: "Tidak, pakai bawaan image", Description: "Image sudah menentukan command dan direktori kerjanya sendiri.", Recommended: true},
			}},
		{ID: "workdir", Header: "Workdir", Kind: ask.Text, Optional: true, When: advanced, Validate: sysdocker.ValidWorkdir,
			Prompt: "Direktori kerja di dalam container?", Placeholder: "/app", Default: def(start.Workdir),
			Help: "Seperti melakukan cd sebelum menjalankan program. Kosongkan untuk memakai bawaan image (WORKDIR di Dockerfile)."},
		{ID: "command", Header: "Command", Kind: ask.Text, Optional: true, When: advanced, Validate: sysdocker.ValidCommand,
			Prompt: "Command yang dijalankan di dalam container?", Placeholder: "kosongkan untuk bawaan image", Default: def(strings.Join(start.Command, " ")),
			Help: "Menggantikan CMD bawaan image. Ditulis seperti di terminal, mis: python -m app. Catatan: ini BUKAN shell, jadi pipa (|) dan $(...) diperlakukan sebagai teks biasa."},
		{ID: "user", Header: "User", Kind: ask.Text, Optional: true, When: advanced, Validate: sysdocker.ValidUserSpec,
			Prompt: "Jalankan sebagai user apa di dalam container?", Placeholder: "kosongkan = bawaan image (sering root)", Default: def(start.User),
			Help: "Isi 1000:1000 (UID:GID milikmu) bila container menulis ke direktori server yang dipasang lewat -v, supaya file yang dibuat tidak jadi milik root."},
		{ID: "network", Header: "Network", Kind: ask.Single, When: advanced, Other: true, Load: networkOptions(c, r), Default: def(start.Network),
			Prompt: "Sambungkan ke network yang mana?",
			Help:   "Container di satu network bisa saling memanggil lewat nama container, mis. aplikasi menyambung ke database dengan host \"db\" tanpa membuka port ke server."},
		{ID: "memory", Header: "RAM", Kind: ask.Text, Optional: true, When: advanced, Validate: sysdocker.ValidMemory,
			Prompt: "Batas RAM?", Placeholder: "kosongkan = tanpa batas", Default: def(start.Memory),
			Help: "Mis. 512m atau 2g. Berguna supaya satu container yang bocor memori tidak menjatuhkan seluruh server. Bila batas terlampaui, program di dalamnya dimatikan kernel (exit code 137)."},
		{ID: "cpus", Header: "CPU", Kind: ask.Text, Optional: true, When: advanced, Validate: sysdocker.ValidCPUs,
			Prompt: "Batas CPU (jumlah inti)?", Placeholder: "kosongkan = tanpa batas", Default: def(start.CPUs),
			Help: "Mis. 0.5 berarti separuh satu inti, 2 berarti dua inti penuh."},
		{ID: "health", Header: "Health", Kind: ask.Text, Optional: true, When: advanced,
			Prompt: "Command pemeriksa sehat di dalam container?", Placeholder: "curl -f http://localhost/ || exit 1", Default: def(start.HealthCmd),
			Help: "Dijalankan docker tiap 30 detik. Bila gagal 3 kali berturut-turut, container ditandai unhealthy di daftar container. Kosongkan bila tidak perlu."},

		{ID: "save", Header: "Simpan", Kind: ask.Confirm, When: detached, Default: []string{ask.ValueYes},
			Prompt: "Simpan setting ini sebagai resep, supaya container bisa dibuat ulang saat image diperbarui?",
			Options: []ask.Option{
				{Label: "Ya, simpan", Description: "Disimpan di ~/.config/ubt/containers.json (izin 0600). Dipakai fitur \"perbarui & buat ulang\".", Recommended: true},
				{Label: "Tidak", Description: "Container tetap dibuat, hanya settingnya tidak dicatat."},
			}},
	}}
}

// def membungkus nilai awal untuk Question.Default.
func def(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// SpecFromAnswers membaca jawaban wizard menjadi resep container.
func SpecFromAnswers(a ask.Answers) (sysdocker.RunSpec, error) {
	s := sysdocker.RunSpec{
		Name:    strings.TrimSpace(a["name"].Value()),
		Image:   strings.TrimSpace(a["image"].Value()),
		Ports:   sysdocker.SplitList(a["ports"].Value()),
		Volumes: sysdocker.SplitList(a["volumes"].Value()),
		Workdir: strings.TrimSpace(a["workdir"].Value()),
		User:    strings.TrimSpace(a["user"].Value()),
		Network: strings.TrimSpace(a["network"].Value()),
		Memory:  strings.TrimSpace(a["memory"].Value()),
		CPUs:    strings.TrimSpace(a["cpus"].Value()),
	}
	if h := strings.TrimSpace(a["health"].Value()); h != "" {
		s.HealthCmd = h
	}
	switch a["envkind"].Value() {
	case "inline":
		s.Env = sysdocker.SplitList(a["env"].Value())
	case "file":
		s.EnvFile = strings.TrimSpace(a["envfile"].Value())
	}
	switch a["mode"].Value() {
	case modeShell:
		s.Interactive = true
	case modeOnce:
		s.Interactive, s.AutoRemove = true, true
	default:
		s.Detach = true
		s.Restart = a["restart"].Value()
	}
	if cmdline := strings.TrimSpace(a["command"].Value()); cmdline != "" {
		parts, err := sysdocker.SplitCommand(cmdline)
		if err != nil {
			return s, err
		}
		s.Command = parts
	}
	return s, s.Validate()
}
