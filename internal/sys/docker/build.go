package docker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// BuildSpec adalah jawaban wizard `docker build`.
type BuildSpec struct {
	Context    string   // direktori berisi kode + Dockerfile
	Dockerfile string   // nama file (kosong = Dockerfile di direktori context)
	Tag        string   // nama:tag image hasil build
	BuildArgs  []string // "KEY=value" untuk ARG di Dockerfile
	NoCache    bool     // abaikan cache layer
	Pull       bool     // ambil image dasar versi terbaru
}

// DockerfilePath mengembalikan path Dockerfile yang dipakai.
func (s BuildSpec) DockerfilePath() string {
	if s.Dockerfile == "" {
		return filepath.Join(s.Context, "Dockerfile")
	}
	if filepath.IsAbs(s.Dockerfile) {
		return s.Dockerfile
	}
	return filepath.Join(s.Context, s.Dockerfile)
}

// ValidBuildContext memeriksa direktori build: harus ada dan berisi Dockerfile.
func ValidBuildContext(s string) error {
	s = strings.TrimSpace(s)
	fi, err := os.Stat(s)
	if err != nil || !fi.IsDir() {
		return errors.New("direktori tidak ditemukan: " + s)
	}
	if _, err := os.Stat(filepath.Join(s, "Dockerfile")); err != nil {
		return errors.New("tidak ada Dockerfile di " + s + "; buat dulu lewat wizard file (tombol n) atau sebut nama filenya di pertanyaan berikutnya")
	}
	return nil
}

// ValidBuildArg memeriksa "KEY=value" untuk --build-arg.
func ValidBuildArg(s string) error { return ValidEnv(s) }

// Args membangun argumen `docker build`.
func (s BuildSpec) Args() []string {
	args := []string{"build", "-t", s.Tag}
	if s.Dockerfile != "" {
		args = append(args, "-f", s.DockerfilePath())
	}
	for _, a := range s.BuildArgs {
		args = append(args, "--build-arg", a)
	}
	if s.NoCache {
		args = append(args, "--no-cache")
	}
	if s.Pull {
		args = append(args, "--pull")
	}
	return append(args, s.Context)
}

// BuildPlan membangun image dari Dockerfile.
func (c Client) BuildPlan(s BuildSpec) run.Plan {
	explain := []run.Line{
		{Token: "docker build", Meaning: "jalankan langkah-langkah di Dockerfile menjadi image baru"},
		{Token: "-t " + s.Tag, Meaning: "nama:tag image hasil build"},
	}
	if s.Dockerfile != "" {
		explain = append(explain, run.Line{Token: "-f " + s.DockerfilePath(), Meaning: "pakai file resep ini, bukan Dockerfile bawaan di direktori build"})
	}
	for _, a := range s.BuildArgs {
		k, _, _ := strings.Cut(a, "=")
		explain = append(explain, run.Line{Token: "--build-arg " + a, Meaning: "isi ARG " + k + " di Dockerfile; nilainya tersimpan di riwayat image, jadi jangan dipakai untuk password"})
	}
	if s.NoCache {
		explain = append(explain, run.Line{Token: "--no-cache", Meaning: "abaikan hasil build sebelumnya; semua langkah diulang (lebih lama, tetapi pasti segar)"})
	}
	if s.Pull {
		explain = append(explain, run.Line{Token: "--pull", Meaning: "ambil versi terbaru image dasar (FROM) dari registry"})
	}
	explain = append(explain, run.Line{Token: s.Context, Meaning: "direktori yang dikirim ke docker sebagai bahan build; file yang tercantum di .dockerignore tidak ikut"})

	cmd := c.cmd("Build image "+s.Tag, risk.Caution,
		"Image baru dibuat di server ini. Tag yang sama akan menunjuk ke image baru; image lama jadi tanpa nama (dangling) dan masih memakan disk sampai dibersihkan.",
		explain, s.Args()...)
	cmd.Safer = "Pastikan .dockerignore memuat .git, .env, dan node_modules supaya rahasia dan file besar tidak ikut masuk image."
	return run.Single(cmd)
}
