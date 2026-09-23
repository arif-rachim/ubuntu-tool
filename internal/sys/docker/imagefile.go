package docker

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// TarFile adalah satu berkas image (hasil `docker save`) yang ditemukan di sebuah direktori.
type TarFile struct {
	Path string
	Name string
	Size int64
}

// tarExts adalah akhiran nama file yang dianggap arsip image.
var tarExts = []string{".tar", ".tar.gz", ".tgz", ".tar.zst", ".tar.xz"}

// IsImageArchive melaporkan apakah nama file terlihat seperti arsip image.
func IsImageArchive(name string) bool {
	l := strings.ToLower(name)
	for _, ext := range tarExts {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return false
}

// FindImageArchives mencari arsip image di dir (tanpa menelusuri subdirektori), terbesar dulu.
func FindImageArchives(dir string) []TarFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []TarFile
	for _, e := range entries {
		if e.IsDir() || !IsImageArchive(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, TarFile{Path: filepath.Join(dir, e.Name()), Name: e.Name(), Size: info.Size()})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Size > out[b].Size })
	return out
}

// ValidArchivePath memeriksa file arsip yang akan dimuat: harus ada dan bukan direktori.
func ValidArchivePath(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("path file wajib diisi")
	}
	fi, err := os.Stat(s)
	switch {
	case err != nil:
		return errors.New("file tidak ditemukan: " + s)
	case fi.IsDir():
		return errors.New(s + " adalah direktori, bukan file arsip image")
	}
	return nil
}

// ValidNewFilePath memeriksa tujuan `docker save`: direktorinya harus ada.
func ValidNewFilePath(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("path file wajib diisi")
	}
	if !filepath.IsAbs(s) && !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "~") {
		return errors.New("tulis path lengkap, mis. /home/kamu/image.tar atau ./image.tar")
	}
	dir := filepath.Dir(s)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return errors.New("direktori tujuan tidak ada: " + dir)
	}
	return nil
}

// LoadImagePlan memuat image dari arsip tar hasil `docker save`.
func (c Client) LoadImagePlan(path string) run.Plan {
	cmd := c.cmd("Muat image dari "+filepath.Base(path), risk.Caution,
		"Image di dalam arsip ditambahkan ke daftar image lokal beserta nama & tag aslinya. Image lokal dengan nama & tag sama akan digantikan.",
		[]run.Line{
			{Token: "docker load", Meaning: "baca image (beserta semua layer-nya) dari arsip, bukan dari registry"},
			{Token: "-i " + path, Meaning: "arsip sumber; formatnya hasil `docker save`, bukan `docker export`"},
		}, "load", "-i", path)
	cmd.Safer = "Muat hanya arsip dari sumber yang kamu percaya: isinya bisa berupa program apa pun yang akan berjalan di server ini."
	return run.Single(cmd)
}

// SaveImagePlan menyimpan satu atau beberapa image ke arsip tar.
func (c Client) SaveImagePlan(refs []string, path string) run.Plan {
	args := append([]string{"save", "-o", path}, refs...)
	cmd := c.cmd("Simpan "+strings.Join(refs, ", ")+" ke "+filepath.Base(path), risk.Caution,
		"Arsip berisi seluruh layer image, jadi ukurannya kira-kira sebesar image itu sendiri. File yang sudah ada di path tujuan akan ditimpa.",
		[]run.Line{
			{Token: "docker save", Meaning: "tulis image lengkap ke satu arsip yang bisa dipindah ke server lain"},
			{Token: "-o " + path, Meaning: "file tujuan"},
			{Token: strings.Join(refs, " "), Meaning: "image yang disimpan (pakai nama:tag, bukan ID, supaya namanya ikut terbawa)"},
		}, args...)
	cmd.Safer = "Untuk memperkecil file: docker save " + strings.Join(refs, " ") + " | gzip > " + path + ".gz  (muat lagi dengan: gunzip -c " + path + ".gz | docker load)"
	return run.Single(cmd)
}
