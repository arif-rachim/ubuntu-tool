package disk

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// ParseHumanSize mengubah "30.5M", "8.507GB", "1.2G", "850MB", "0B" menjadi byte.
func ParseHumanSize(s string) int64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	v, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToUpper(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s[i:]), "iB"), "B"))
	mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[unit]
	if strings.HasSuffix(strings.TrimSpace(s[i:]), "B") && !strings.Contains(s[i:], "i") && len(unit) == 1 {
		// Docker memakai satuan desimal (kB, MB, GB).
		mult = map[string]float64{"K": 1e3, "M": 1e6, "G": 1e9, "T": 1e12}[unit]
	}
	return int64(v * mult)
}

var journalUsageRe = regexp.MustCompile(`take up ([0-9.]+\s*[KMGT]?)`)

// ParseJournalUsage mengambil ukuran dari "Archived and active journals take up 30.5M in the file system."
func ParseJournalUsage(out string) int64 {
	m := journalUsageRe.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	return ParseHumanSize(m[1])
}

// SnapRevision adalah revisi snap yang nonaktif dan bisa dihapus.
type SnapRevision struct {
	Name     string
	Revision string
	Size     int64
}

// ParseSnapDisabled mengambil baris berstatus "disabled" dari `snap list --all`.
func ParseSnapDisabled(out string, snapsDir string) []SnapRevision {
	var revs []SnapRevision
	for _, line := range strings.Split(out, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 6 || !strings.Contains(f[len(f)-1], "disabled") {
			continue
		}
		r := SnapRevision{Name: f[0], Revision: f[2]}
		if info, err := os.Stat(filepath.Join(snapsDir, r.Name+"_"+r.Revision+".snap")); err == nil {
			r.Size = info.Size()
		}
		revs = append(revs, r)
	}
	return revs
}

// ParseAutoremove menghitung paket yang akan dihapus dari `apt-get --dry-run autoremove` (baris "Remv").
func ParseAutoremove(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[0] == "Remv" {
			pkgs = append(pkgs, f[1])
		}
	}
	return pkgs
}

// ParseDockerReclaimable mengambil total ruang yang bisa diambil kembali dari `docker system df`.
func ParseDockerReclaimable(out string) int64 {
	var total int64
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// TYPE bisa dua kata ("Local Volumes", "Build Cache"): ambil kolom RECLAIMABLE dari kanan.
		if len(f) < 5 || f[0] == "TYPE" {
			continue
		}
		idx := len(f) - 1
		if strings.HasPrefix(f[idx], "(") {
			idx--
		}
		total += ParseHumanSize(f[idx])
	}
	return total
}

// DirFilesSize menjumlahkan ukuran file yang cocok dengan predikat (tanpa rekursi ke mount lain).
func DirFilesSize(root string, match func(name string) bool) (count int, size int64) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !match(d.Name()) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			count++
			size += info.Size()
		}
		return nil
	})
	return count, size
}

// RotatedLog melaporkan apakah nama file adalah log hasil rotasi (syslog.1, auth.log.2.gz).
func RotatedLog(name string) bool {
	if strings.HasSuffix(name, ".gz") {
		return true
	}
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return false
	}
	_, err := strconv.Atoi(name[i+1:])
	return err == nil
}

// Candidate adalah satu kandidat bersih-bersih.
type Candidate struct {
	ID        string
	Label     string
	Detail    string
	Size      int64 // perkiraan ruang yang kembali (0 bila tidak diketahui)
	SizeLabel string
	Available bool
	Why       string // alasan tidak tersedia
	Steps     []run.Command
}

// CleanupSources adalah lokasi yang dipakai saat menghitung kandidat (bisa diganti saat test).
type CleanupSources struct {
	AptArchives string
	SnapsDir    string
	LogDir      string
}

// DefaultCleanupSources untuk sistem sungguhan.
var DefaultCleanupSources = CleanupSources{
	AptArchives: "/var/cache/apt/archives",
	SnapsDir:    "/var/lib/snapd/snaps",
	LogDir:      "/var/log",
}

// JournalKeep adalah ukuran journal yang disisakan saat vacuum.
const JournalKeep = 200 << 20

// FindCandidates menghitung kandidat bersih-bersih beserta perkiraan ukurannya.
func FindCandidates(ctx context.Context, r run.Runner, src CleanupSources) []Candidate {
	var out []Candidate

	n, size := DirFilesSize(src.AptArchives, func(name string) bool { return strings.HasSuffix(name, ".deb") })
	out = append(out, Candidate{
		ID: "apt-clean", Label: "Cache paket apt", Size: size, Available: n > 0, Why: "cache apt sudah kosong",
		Detail: "File .deb yang sudah terpasang. Aman dihapus; apt mengunduh ulang bila diperlukan.",
		Steps: []run.Command{{
			Title: "Kosongkan cache apt", Argv: []string{"apt-get", "clean"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "apt-get clean", Meaning: "hapus semua file .deb di /var/cache/apt/archives"}},
		}},
	})

	dry, _, err := r.Capture(ctx, run.Command{Argv: []string{"apt-get", "--dry-run", "autoremove"}})
	pkgs := ParseAutoremove(dry)
	c := Candidate{
		ID: "autoremove", Label: "Paket yang tidak dipakai lagi", Available: err == nil && len(pkgs) > 0,
		Detail: "Dependensi yang tertinggal setelah paket lain di-uninstall.",
		Why:    "tidak ada paket yatim",
		Steps: []run.Command{{
			Title: "Hapus paket yatim", Argv: []string{"apt-get", "autoremove", "--purge", "-y"}, NeedsRoot: true,
			Explain: []run.Line{
				{Token: "autoremove", Meaning: "hapus paket yang dulu terpasang sebagai dependensi dan kini tidak dibutuhkan"},
				{Token: "--purge", Meaning: "ikut hapus file konfigurasinya"},
				{Token: "-y", Meaning: "jawab \"ya\" otomatis (persetujuan sudah diberikan di layar ini)"},
			},
			Risk: 1,
		}},
	}
	if len(pkgs) > 0 {
		c.SizeLabel = strconv.Itoa(len(pkgs)) + " paket"
		c.Detail += " Paket: " + strings.Join(firstN(pkgs, 6), ", ")
		if len(pkgs) > 6 {
			c.Detail += ", …"
		}
	}
	if err != nil {
		c.Why = "apt-get tidak tersedia"
	}
	out = append(out, c)

	usage, _, err := r.Capture(ctx, run.Command{Argv: []string{"journalctl", "--disk-usage"}})
	jsize := ParseJournalUsage(usage)
	out = append(out, Candidate{
		ID: "journal", Label: "Journal lama (sisakan 200 MiB)", Size: max(jsize-JournalKeep, 0),
		Available: err == nil && jsize > JournalKeep, Why: "journal masih di bawah 200 MiB",
		Detail: "Log sistem lama. Log terbaru tetap tersimpan.",
		Steps: []run.Command{{
			Title: "Rampingkan journal", Argv: []string{"journalctl", "--vacuum-size=200M"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "--vacuum-size=200M", Meaning: "hapus arsip journal tertua sampai total tinggal 200 MiB"}},
		}},
	})

	snapOut, _, err := r.Capture(ctx, run.Command{Argv: []string{"snap", "list", "--all"}})
	revs := ParseSnapDisabled(snapOut, src.SnapsDir)
	sc := Candidate{
		ID: "snap", Label: "Revisi snap nonaktif", Available: err == nil && len(revs) > 0,
		Detail: "Snap menyimpan revisi lama untuk rollback. Revisi yang nonaktif aman dihapus.",
		Why:    "tidak ada revisi snap nonaktif",
	}
	if err != nil {
		sc.Why = "snap tidak terinstall"
	}
	for _, rv := range revs {
		sc.Size += rv.Size
		sc.Steps = append(sc.Steps, run.Command{
			Title: "Hapus " + rv.Name + " revisi " + rv.Revision, Argv: []string{"snap", "remove", rv.Name, "--revision=" + rv.Revision}, NeedsRoot: true,
			Explain: []run.Line{{Token: "--revision=" + rv.Revision, Meaning: "hanya revisi lama ini; versi yang aktif tidak disentuh"}},
		})
	}
	out = append(out, sc)

	dockerDF, _, err := r.Capture(ctx, run.Command{Argv: []string{"docker", "system", "df"}})
	reclaim := ParseDockerReclaimable(dockerDF)
	out = append(out, Candidate{
		ID: "docker", Label: "Data Docker tak terpakai", Size: reclaim, Available: err == nil && reclaim > 0,
		Why:    map[bool]string{true: "tidak ada yang bisa diambil kembali", false: "docker tidak tersedia atau tidak ada akses"}[err == nil],
		Detail: "Container yang sudah berhenti, image tanpa container, network tak terpakai, dan build cache ikut terhapus. Volume tidak disentuh.",
		Steps: []run.Command{{
			Title: "Bersihkan data Docker", Argv: []string{"docker", "system", "prune", "-a", "-f"},
			Explain: []run.Line{
				{Token: "system prune", Meaning: "hapus container berhenti, network & build cache tak terpakai"},
				{Token: "-a", Meaning: "termasuk image yang tidak dipakai container mana pun"},
				{Token: "-f", Meaning: "tanpa bertanya lagi (persetujuan sudah diberikan di layar ini)"},
			},
			Effect: "Image yang dihapus harus di-pull ulang bila nanti dibutuhkan.",
			Risk:   1,
		}},
	})

	ln, lsize := DirFilesSize(src.LogDir, RotatedLog)
	out = append(out, Candidate{
		ID: "rotated-logs", Label: "Log hasil rotasi di /var/log", Size: lsize, Available: ln > 0, Why: "tidak ada log lama",
		Detail: "File seperti syslog.1 dan auth.log.2.gz. Log yang sedang aktif tidak dihapus.",
		Steps: []run.Command{{
			Title: "Hapus log lama", NeedsRoot: true,
			Argv: []string{"find", src.LogDir, "-type", "f", "(", "-name", "*.gz", "-o", "-regex", `.*\.[0-9]+`, ")", "-delete"},
			Explain: []run.Line{
				{Token: "-type f", Meaning: "hanya file"},
				{Token: "-name '*.gz' -o -regex '.*\\.[0-9]+'", Meaning: "log terkompresi atau berakhiran angka hasil rotasi"},
				{Token: "-delete", Meaning: "hapus file yang cocok"},
			},
			Risk: 1,
		}},
	})
	return out
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}
