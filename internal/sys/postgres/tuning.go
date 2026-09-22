package postgres

import (
	"fmt"
	"strconv"
	"strings"
)

// Jenis beban kerja yang dipakai kalkulator penyetelan.
const (
	WorkloadWeb   = "web"   // banyak koneksi, query pendek (aplikasi web/API)
	WorkloadMixed = "mixed" // campuran: aplikasi + laporan
	WorkloadDW    = "dw"    // laporan & analitik: query besar, koneksi sedikit
)

// Server adalah kondisi mesin yang dipakai kalkulator.
type Server struct {
	RAMBytes int64
	CPUs     int
	SSD      bool
	Workload string
	MaxConn  int // 0 = pakai saran ubt
}

// Tuned adalah satu parameter hasil perhitungan.
type Tuned struct {
	Name         string
	Value        string
	Current      string // nilai yang sedang berlaku (diisi pemanggil)
	Why          string
	NeedsRestart bool
}

// Changed melaporkan apakah nilai baru berbeda dari yang sedang berlaku.
func (t Tuned) Changed() bool {
	return t.Current != "" && !sameSetting(t.Current, t.Value)
}

// sameSetting membandingkan dua nilai parameter dalam satuan yang sama.
func sameSetting(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) || toKB(a) == toKB(b) && toKB(a) > 0
}

// toKB menerjemahkan "512MB", "2GB", "64kB" menjadi kilobyte; 0 bila bukan ukuran.
func toKB(s string) int64 {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := int64(0)
	switch {
	case strings.HasSuffix(s, "gb"):
		mult, s = 1<<20, strings.TrimSuffix(s, "gb")
	case strings.HasSuffix(s, "mb"):
		mult, s = 1<<10, strings.TrimSuffix(s, "mb")
	case strings.HasSuffix(s, "kb"):
		mult, s = 1, strings.TrimSuffix(s, "kb")
	default:
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n * mult
}

// mb merender ukuran dalam MB/GB seperti yang dipakai postgresql.conf.
func mb(megabytes int64) string {
	if megabytes >= 1024 && megabytes%1024 == 0 {
		return strconv.FormatInt(megabytes/1024, 10) + "GB"
	}
	return strconv.FormatInt(megabytes, 10) + "MB"
}

// SuggestedMaxConn adalah jumlah koneksi yang disarankan untuk jenis beban tertentu.
func SuggestedMaxConn(workload string) int {
	switch workload {
	case WorkloadDW:
		return 40
	case WorkloadMixed:
		return 100
	}
	return 150
}

// Tune menghitung nilai parameter yang masuk akal untuk mesin dan beban tertentu.
// Angkanya mengikuti pedoman umum PostgreSQL: seperempat RAM untuk shared_buffers, dan sisanya
// dibagi menurut jumlah koneksi. Ini titik awal yang waras, bukan hasil pengukuran beban aslimu.
func Tune(s Server) []Tuned {
	ramMB := s.RAMBytes / (1 << 20)
	if ramMB <= 0 {
		return nil
	}
	cpus := s.CPUs
	if cpus < 1 {
		cpus = 1
	}
	maxConn := s.MaxConn
	if maxConn <= 0 {
		maxConn = SuggestedMaxConn(s.Workload)
	}

	shared := ramMB / 4
	if shared > 8192 {
		shared = 8192 // di atas ini, cache sistem operasi biasanya lebih berguna
	}
	if shared < 128 {
		shared = 128
	}
	cache := ramMB * 3 / 4
	maint := ramMB / 16
	switch {
	case maint > 2048:
		maint = 2048
	case maint < 64:
		maint = 64
	}
	// work_mem dipakai PER operasi sort/hash, bisa beberapa kali per query.
	workKB := (ramMB - shared) * 1024 / int64(maxConn) / 3
	if s.Workload == WorkloadDW {
		workKB *= 2
	}
	switch {
	case workKB < 4096:
		workKB = 4096
	case workKB > 256*1024:
		workKB = 256 * 1024
	}

	randomCost, ioConcurrency, statsTarget := "4.0", "2", "100"
	if s.SSD {
		randomCost, ioConcurrency = "1.1", "200"
	}
	if s.Workload == WorkloadDW {
		statsTarget = "500"
	}
	parallel := cpus / 2
	if parallel < 1 {
		parallel = 1
	}
	if parallel > 8 {
		parallel = 8
	}
	maxWAL := int64(4096)
	if s.Workload == WorkloadDW {
		maxWAL = 8192
	}

	return []Tuned{
		{Name: "max_connections", Value: strconv.Itoa(maxConn), NeedsRestart: true,
			Why: "koneksi bersamaan; tiap koneksi memakai memori sendiri, jadi jangan berlebihan — pakai connection pool di aplikasi"},
		{Name: "shared_buffers", Value: mb(shared), NeedsRestart: true,
			Why: "cache data milik PostgreSQL sendiri, ±25% RAM"},
		{Name: "effective_cache_size", Value: mb(cache), Why: "perkiraan total cache (PostgreSQL + sistem operasi); dipakai perencana query, bukan alokasi nyata"},
		{Name: "maintenance_work_mem", Value: mb(maint), Why: "memori untuk VACUUM, CREATE INDEX, dan ALTER TABLE"},
		{Name: "work_mem", Value: kbValue(workKB), Why: fmt.Sprintf("memori per operasi sort/hash; dihitung dari RAM dibagi %d koneksi", maxConn)},
		{Name: "wal_buffers", Value: "16MB", Why: "penyangga catatan transaksi sebelum ditulis ke disk"},
		{Name: "min_wal_size", Value: "1GB", Why: "ruang catatan transaksi yang dijaga tetap ada supaya tidak bolak-balik dibuat"},
		{Name: "max_wal_size", Value: mb(maxWAL), Why: "batas ukuran catatan transaksi sebelum checkpoint dipaksa"},
		{Name: "checkpoint_completion_target", Value: "0.9", Why: "sebar penulisan checkpoint supaya tidak ada lonjakan I/O mendadak"},
		{Name: "random_page_cost", Value: randomCost, Why: diskWhy(s.SSD)},
		{Name: "effective_io_concurrency", Value: ioConcurrency, Why: "berapa banyak pembacaan disk bisa berjalan bersamaan"},
		{Name: "default_statistics_target", Value: statsTarget, Why: "seberapa rinci statistik tabel dikumpulkan; makin tinggi, rencana query makin tepat tetapi ANALYZE makin lama"},
		{Name: "max_worker_processes", Value: strconv.Itoa(cpus), NeedsRestart: true, Why: fmt.Sprintf("mengikuti jumlah inti CPU (%d)", cpus)},
		{Name: "max_parallel_workers", Value: strconv.Itoa(cpus), Why: "batas total proses paralel untuk query"},
		{Name: "max_parallel_workers_per_gather", Value: strconv.Itoa(parallel), Why: "batas proses paralel untuk satu query; terlalu tinggi justru memperlambat beban web"},
	}
}

func kbValue(kb int64) string {
	if kb >= 1024 && kb%1024 == 0 {
		return mb(kb / 1024)
	}
	return strconv.FormatInt(kb, 10) + "kB"
}

func diskWhy(ssd bool) string {
	if ssd {
		return "SSD/NVMe: membaca acak hampir secepat berurutan, jadi perencana query lebih berani memakai index"
	}
	return "hard disk berputar: membaca acak jauh lebih mahal daripada berurutan"
}

// Notes adalah catatan tambahan yang perlu dibaca sebelum menerapkan hasil penyetelan.
func Notes(s Server) []string {
	var out []string
	ramMB := s.RAMBytes / (1 << 20)
	if ramMB < 2048 {
		out = append(out, "RAM server ini kecil (<2 GB). Pastikan ada swap supaya server tidak mati mendadak saat beban naik — lihat modul Disk & Storage.")
	}
	if s.Workload == WorkloadWeb {
		out = append(out, "Untuk aplikasi web, connection pool (PgBouncer atau pool bawaan framework) jauh lebih berpengaruh daripada menaikkan max_connections.")
	}
	out = append(out, "Angka di atas adalah titik awal. Setelah diterapkan, amati lagi lewat monitor koneksi & query lambat, lalu sesuaikan satu parameter dalam satu waktu.")
	return out
}
