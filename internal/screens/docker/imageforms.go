package docker

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// BuildForm adalah wizard `docker build`.
func BuildForm(dir string) ask.Form {
	return ask.Form{ID: "build", Title: "Build image dari Dockerfile", Questions: []ask.Question{
		{ID: "context", Header: "Direktori", Kind: ask.Text, Prompt: "Direktori berisi kode dan Dockerfile?", Default: []string{dir}, Validate: sysdocker.ValidBuildContext,
			Help: "Seluruh isi direktori ini dikirim ke docker sebagai bahan build (kecuali yang tercantum di .dockerignore). Belum punya Dockerfile? Buat dulu lewat wizard file di layar Docker (tombol n)."},
		{ID: "tag", Header: "Nama", Kind: ask.Text, Prompt: "Nama & tag image hasil build?", Placeholder: "app:1.0", Validate: sysdocker.ValidImage,
			Help: "Pakai tag versi (app:1.0) supaya versi lama masih bisa dipakai bila yang baru bermasalah. Untuk langsung dipush ke registry, boleh juga ditulis lengkap: nexus.contoh.com:8082/tim-a/app:1.0"},
		{ID: "args", Header: "Build-arg", Kind: ask.Text, Optional: true, Prompt: "Nilai untuk ARG di Dockerfile (NAMA=nilai, pisahkan koma)?",
			Placeholder: "kosongkan bila tidak ada", Validate: listValidator(sysdocker.ValidBuildArg),
			Help: "Hanya untuk nilai yang aman dilihat orang lain: build-arg tersimpan di riwayat image dan bisa dibaca siapa pun yang punya image-nya. Jangan dipakai untuk password atau token."},
		{ID: "fresh", Header: "Cache", Kind: ask.Single, Prompt: "Pakai hasil build sebelumnya (cache)?", Options: []ask.Option{
			{Value: "cache", Label: "Ya, pakai cache", Description: "Jauh lebih cepat: langkah yang bahannya tidak berubah dilewati.", Recommended: true},
			{Value: "pull", Label: "Pakai cache, tetapi segarkan image dasar", Description: "Mengambil versi terbaru image di baris FROM (--pull), sisanya tetap memakai cache."},
			{Value: "nocache", Label: "Tidak, ulangi semua langkah", Description: "Untuk memastikan hasil build benar-benar segar (--no-cache --pull). Paling lama."},
		}},
	}}
}

// BuildSpecFromAnswers membaca jawaban wizard build.
func BuildSpecFromAnswers(a ask.Answers) sysdocker.BuildSpec {
	s := sysdocker.BuildSpec{
		Context:   expandDir(a["context"].Value()),
		Tag:       strings.TrimSpace(a["tag"].Value()),
		BuildArgs: sysdocker.SplitList(a["args"].Value()),
	}
	switch a["fresh"].Value() {
	case "pull":
		s.Pull = true
	case "nocache":
		s.NoCache, s.Pull = true, true
	}
	return s
}

// LoadImageForm menanyakan arsip image yang akan dimuat.
func LoadImageForm(dir string) ask.Form {
	return ask.Form{ID: "img-load", Title: "Muat image dari berkas", SkipReview: true, Questions: []ask.Question{
		{ID: "file", Header: "Berkas", Kind: ask.Single, Prompt: "Berkas arsip image mana yang dimuat?", Other: true,
			Load: archiveOptions(dir), Validate: sysdocker.ValidArchivePath,
			Help: "Arsip hasil `docker save` di server lain (biasanya berakhiran .tar). Daftar di bawah diambil dari " + dir +
				"; pilih \"Lainnya…\" untuk mengetik path berkas di direktori lain."},
	}}
}

// archiveOptions memuat daftar arsip image di sebuah direktori.
func archiveOptions(dir string) func(context.Context) ([]ask.Option, error) {
	return func(context.Context) ([]ask.Option, error) {
		var opts []ask.Option
		for _, f := range sysdocker.FindImageArchives(dir) {
			opts = append(opts, ask.Option{Value: f.Path, Label: f.Name, Meta: shared.Bytes(f.Size),
				Description: "arsip di " + dir})
		}
		return opts, nil
	}
}

// SaveImageForm menanyakan tujuan `docker save`.
func SaveImageForm(img sysdocker.Image, dir string) ask.Form {
	name := strings.NewReplacer("/", "-", ":", "-").Replace(img.Ref())
	return ask.Form{ID: "img-save", Title: "Simpan " + img.Ref() + " ke berkas", SkipReview: true, Questions: []ask.Question{
		{ID: "file", Header: "Berkas", Kind: ask.Text, Prompt: "Simpan sebagai berkas apa?",
			Default: []string{filepath.Join(dir, name+".tar")}, Validate: sysdocker.ValidNewFilePath,
			Help: "Ukurannya kira-kira sebesar image (" + img.Size + "). Pindahkan ke server tujuan dengan scp, lalu muat di sana lewat menu image → muat dari berkas."},
	}}
}

// PushImageForm menanyakan ke registry mana image diunggah.
func PushImageForm(img sysdocker.Image) ask.Form {
	short := img.Ref()
	if i := strings.LastIndex(short, "/"); i >= 0 {
		short = short[i+1:]
	}
	return ask.Form{ID: "img-push", Title: "Unggah " + img.Ref(), Questions: []ask.Question{
		{ID: "registry", Header: "Registry", Kind: ask.Single, Prompt: "Diunggah ke registry mana?", Other: true, Optional: true,
			Load: registryOptions(), Validate: sysdocker.ValidRegistryHost,
			Help: "Daftar ini berasal dari profil registry yang kamu simpan di layar Docker → registry (tombol g). Pilih \"Lainnya…\" untuk mengetik host lain, atau kosongkan bila nama image sudah lengkap."},
		{ID: "target", Header: "Nama", Kind: ask.Text, Prompt: "Nama & tag di registry?", Default: []string{short}, Validate: sysdocker.ValidImage,
			Help: "Awalan registry ditambahkan otomatis dari pilihan di atas. Hindari :latest saja — tag versi memudahkan kembali ke versi sebelumnya."},
	}}
}

// registryOptions memuat profil registry yang tersimpan.
func registryOptions() func(context.Context) ([]ask.Option, error) {
	return func(context.Context) ([]ask.Option, error) {
		path, err := sysdocker.DefaultRegistryPath()
		if err != nil {
			return nil, err
		}
		regs, err := sysdocker.LoadRegistries(path)
		if err != nil {
			return nil, err
		}
		var opts []ask.Option
		for _, r := range regs.Items {
			desc := "profil tersimpan; login lewat layar registry bila push ditolak"
			lvl := risk.Safe
			if r.Insecure {
				desc, lvl = "HTTP polos — isi image dan kredensial lewat jaringan tanpa perlindungan", risk.Caution
			}
			opts = append(opts, ask.Option{Value: r.Prefix(), Label: r.Name + " (" + r.Prefix() + ")", Description: desc, Risk: lvl})
		}
		return opts, nil
	}
}
