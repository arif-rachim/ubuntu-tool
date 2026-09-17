package resource

import (
	"fmt"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

// Insight adalah temuan yang dijelaskan dalam bahasa manusia.
type Insight struct {
	Level  risk.Level
	Title  string
	Detail string
	Next   string // langkah yang disarankan (boleh kosong)
}

// Analyze menerjemahkan snapshot menjadi temuan, yang paling penting lebih dulu.
func Analyze(s Snapshot) []Insight {
	var out []Insight
	m := s.Memory

	// Load dibanding jumlah core.
	perCore := s.Load.One / float64(max(s.Cores, 1))
	switch {
	case perCore >= 2:
		out = append(out, Insight{
			Level:  risk.Dangerous,
			Title:  fmt.Sprintf("Server kewalahan: load %.2f di %d core", s.Load.One, s.Cores),
			Detail: fmt.Sprintf("Load average adalah rata-rata jumlah tugas yang sedang berjalan atau mengantre. Load %.2f di %d core berarti antrean %.1f× kapasitas CPU.", s.Load.One, s.Cores, perCore),
			Next:   "lihat proses teratas di bawah; bila tekanan IO tinggi, penyebabnya bisa disk yang lambat, bukan CPU",
		})
	case perCore >= 1:
		out = append(out, Insight{
			Level:  risk.Caution,
			Title:  fmt.Sprintf("CPU penuh: load %.2f di %d core", s.Load.One, s.Cores),
			Detail: "Load setara atau di atas jumlah core berarti tugas mulai mengantre. Wajar sesaat, tapi bila terus-menerus server terasa lambat.",
		})
	}

	// RAM: pakai MemAvailable, bukan MemFree.
	if m.TotalKB > 0 {
		avail := 100 * float64(m.AvailableKB) / float64(m.TotalKB)
		swapUsed := 0.0
		if m.SwapTotalKB > 0 {
			swapUsed = 100 * float64(m.SwapUsedKB()) / float64(m.SwapTotalKB)
		}
		switch {
		case avail < 10:
			out = append(out, Insight{
				Level:  risk.Dangerous,
				Title:  fmt.Sprintf("RAM hampir habis: tersisa %.0f%%", avail),
				Detail: "Bila RAM benar-benar habis, kernel mematikan proses terbesar (OOM killer).",
				Next:   "hentikan proses besar yang tidak perlu, tambah swap, atau tambah RAM",
			})
		case avail < 20:
			out = append(out, Insight{Level: risk.Caution, Title: fmt.Sprintf("RAM menipis: tersisa %.0f%%", avail), Detail: "Perhatikan proses yang memakan memori terbesar."})
		}
		if s.PSI.Memory.Available && s.PSI.Memory.Avg60 >= 10 {
			out = append(out, Insight{
				Level:  risk.Dangerous,
				Title:  fmt.Sprintf("Kekurangan RAM nyata: tugas tertahan menunggu memori %.0f%% waktu", s.PSI.Memory.Avg60),
				Detail: "Tekanan memori (PSI) mengukur berapa lama proses tertahan karena kernel sibuk mencari memori kosong. Ini tanda paling jujur bahwa RAM kurang.",
				Next:   "kurangi proses yang berjalan atau tambah RAM",
			})
		} else if swapUsed > 50 {
			out = append(out, Insight{
				Level:  risk.Caution,
				Title:  fmt.Sprintf("Swap terpakai %.0f%%", swapUsed),
				Detail: "Swap terpakai sendiri belum tentu masalah (data yang jarang dipakai dipindah ke disk). Menjadi masalah bila tekanan memori juga tinggi.",
			})
		}
		if m.SwapTotalKB == 0 && m.TotalKB < 4*1024*1024 {
			out = append(out, Insight{
				Level:  risk.Caution,
				Title:  "Tidak ada swap",
				Detail: "Dengan RAM di bawah 4 GiB dan tanpa swap, lonjakan pemakaian memori langsung memicu OOM killer.",
				Next:   "buat swapfile lewat modul Disk & Storage",
			})
		}
	}

	if s.PSI.IO.Available && s.PSI.IO.Avg60 >= 20 {
		out = append(out, Insight{
			Level:  risk.Caution,
			Title:  fmt.Sprintf("Disk jadi hambatan: tugas menunggu IO %.0f%% waktu", s.PSI.IO.Avg60),
			Detail: "Proses sering tertahan menunggu baca/tulis disk. Server terasa lambat walau CPU tidak penuh.",
			Next:   "cek disk penuh atau proses yang banyak menulis di modul Disk & Storage",
		})
	}
	if s.PSI.CPU.Available && s.PSI.CPU.Avg60 >= 30 && perCore < 1 {
		out = append(out, Insight{
			Level:  risk.Caution,
			Title:  fmt.Sprintf("Proses sering mengantre CPU (%.0f%% waktu)", s.PSI.CPU.Avg60),
			Detail: "Walau load terlihat rendah, ada proses yang sering menunggu giliran CPU. Bisa karena batas CPU container/VM.",
		})
	}

	if len(out) == 0 {
		out = append(out, Insight{Level: risk.Safe, Title: "Sumber daya dalam kondisi baik", Detail: "Load, RAM, dan tekanan disk masih jauh dari batas."})
	}
	if m.TotalKB > 0 && m.CacheKB() > m.TotalKB/4 {
		out = append(out, Insight{
			Level:  risk.Safe,
			Title:  "RAM \"terpakai\" untuk cache itu normal",
			Detail: "Linux memakai RAM kosong sebagai cache file supaya disk terasa cepat, dan melepasnya otomatis saat aplikasi butuh. Karena itu yang diukur adalah RAM tersedia, bukan RAM bebas (free).",
		})
	}
	return out
}

// Bar menggambar bar persen teks, mis. "████████░░░░".
func Bar(percent float64, width int) string {
	width = max(width, 1)
	filled := int(percent/100*float64(width) + 0.5)
	filled = min(max(filled, 0), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
