package disk

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Swap adalah satu area swap aktif.
type Swap struct {
	Name string
	Type string // file / partition
	Size int64
	Used int64
}

// SwaponArgs adalah command untuk membaca swap aktif.
var SwaponArgs = []string{"swapon", "--show=NAME,TYPE,SIZE,USED", "--bytes", "--noheadings", "--raw"}

// ParseSwapon mem-parse output SwaponArgs.
func ParseSwapon(out string) []Swap {
	var swaps []Swap
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		s := Swap{Name: f[0], Type: f[1]}
		s.Size, _ = strconv.ParseInt(f[2], 10, 64)
		s.Used, _ = strconv.ParseInt(f[3], 10, 64)
		swaps = append(swaps, s)
	}
	return swaps
}

// RecommendedSwap menyarankan ukuran swap (GiB) berdasarkan RAM.
func RecommendedSwap(ramBytes int64) int {
	gib := float64(ramBytes) / (1 << 30)
	switch {
	case gib <= 2:
		return 2
	case gib <= 8:
		return int(gib + 0.5)
	default:
		return 4
	}
}

// SwapfilePlan membangun Plan pembuatan swapfile permanen.
func SwapfilePlan(path string, sizeGiB int, now time.Time) run.Plan {
	size := fmt.Sprintf("%dG", sizeGiB)
	backup := "/etc/fstab.ubt-bak-" + now.Format("20060102-150405")
	line := path + " none swap sw 0 0\n"
	return run.Plan{
		Title: fmt.Sprintf("Buat swapfile %s sebesar %s", path, size),
		Steps: []run.Command{
			{
				Title: "Siapkan file " + size, Argv: []string{"fallocate", "-l", size, path}, NeedsRoot: true,
				Explain: []run.Line{{Token: "fallocate -l " + size, Meaning: "pesan ruang disk " + size + " sekaligus (cepat, tanpa menulis nol)"}},
				Effect:  "Ruang disk sebesar " + size + " langsung terpakai.",
				Risk:    risk.Caution,
			},
			{
				Title: "Kunci izin file", Argv: []string{"chmod", "600", path}, NeedsRoot: true,
				Explain: []run.Line{{Token: "600", Meaning: "hanya root yang bisa membaca/menulis; isi RAM yang di-swap bisa berisi rahasia"}},
			},
			{
				Title: "Format sebagai swap", Argv: []string{"mkswap", path}, NeedsRoot: true,
				Explain: []run.Line{{Token: "mkswap", Meaning: "tulis penanda swap di awal file"}},
			},
			{
				Title: "Aktifkan swap sekarang", Argv: []string{"swapon", path}, NeedsRoot: true,
				Explain: []run.Line{{Token: "swapon", Meaning: "mulai pakai file sebagai swap (berlaku sampai reboot)"}},
			},
			{
				Title: "Cadangkan /etc/fstab", Argv: []string{"cp", "-a", "/etc/fstab", backup}, NeedsRoot: true,
				Explain: []run.Line{{Token: backup, Meaning: "salinan untuk dikembalikan bila ada yang salah"}},
			},
			{
				Title: "Aktifkan otomatis saat boot", Argv: []string{"tee", "-a", "/etc/fstab"}, NeedsRoot: true,
				Stdin: line, StdinLabel: "(1 baris fstab)",
				Explain: []run.Line{
					{Token: "tee -a", Meaning: "tambahkan (append) stdin ke akhir file"},
					{Token: "/etc/fstab", Meaning: "daftar filesystem & swap yang dipasang saat boot"},
				},
				Effect: "Baris yang salah di /etc/fstab bisa membuat boot gagal; karena itu fstab divalidasi sesudahnya.",
				Risk:   risk.Caution,
			},
			{
				Title: "Tampilkan swap aktif", Argv: []string{"swapon", "--show"},
				Explain: []run.Line{{Token: "--show", Meaning: "daftar swap yang sedang aktif"}},
			},
		},
		Check: &run.Command{
			Title: "Validasi /etc/fstab", Argv: []string{"findmnt", "--verify"},
			Explain: []run.Line{{Token: "findmnt --verify", Meaning: "periksa sintaks /etc/fstab; bila gagal, pulihkan dari " + backup}},
		},
	}
}
