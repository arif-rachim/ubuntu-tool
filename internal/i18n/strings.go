// Package i18n menyimpan semua teks antarmuka di satu tempat.
//
// Saat ini hanya Bahasa Indonesia. Nama command dan flag Linux sengaja tidak diterjemahkan.
package i18n

const (
	AppName = "ubt"

	// Kunci global
	KeyBack    = "kembali"
	KeyQuit    = "keluar"
	KeyHelp    = "bantuan"
	KeyRefresh = "muat ulang"
	KeySelect  = "pilih"
	KeyMove    = "pindah"
	KeyOpen    = "buka"
	KeyClose   = "tutup"

	// Layar bantuan
	HelpTitle      = "Bantuan"
	HelpGlobalKeys = "Tombol umum"
	HelpScreenKeys = "Tombol di layar ini"
	HelpAbout      = "Penjelasan"
	HelpDismiss    = "tekan tombol apa pun untuk menutup"

	// Header
	BadgeRoot    = "root"
	BadgeNonRoot = "non-root"

	// Non-TTY
	NeedsTTY = "ubt butuh terminal interaktif. Untuk perintah yang bisa dipakai di script, jalankan `ubt help`."

	// Home
	HomeTitle       = "Menu utama"
	HomeIntro       = "Pilih yang ingin kamu lakukan. Setiap aksi akan menampilkan command aslinya sebelum dijalankan."
	GroupDiagnose   = "Ada masalah?"
	GroupSystem     = "Sistem"
	GroupNetwork    = "Jaringan"
	GroupAccess     = "Akses"
	GroupContainer  = "Container"
	GroupHistory    = "Riwayat"
	PlaceholderBody = "Modul ini belum tersedia di build ini."
	PlaceholderPlan = "Direncanakan di fase %d — lihat docs/PLAN.md."

	// Status
	Loading = "Memuat…"
	ErrorOf = "Gagal: %v"
	HintOf  = "Saran: %s"

	// Komponen ask
	AskRecommended   = "(Disarankan)"
	AskOther         = "Lainnya…"
	AskOtherDesc     = "Ketik sendiri"
	AskYes           = "Ya"
	AskNo            = "Tidak"
	AskMultiHint     = "(pilih satu atau lebih)"
	AskReview        = "Ringkasan"
	AskReviewSubmit  = "Lanjut"
	AskReviewEditKey = "enter pada jawaban untuk mengubah"
	AskEmptyAnswer   = "—"
	AskMoreAbove     = "↑ %d lagi di atas"
	AskMoreBelow     = "↓ %d lagi di bawah"
	AskFilterPrompt  = "Filter: "
	AskNoMatch       = "Tidak ada opsi yang cocok dengan filter."
	AskLoadFailed    = "Gagal memuat pilihan: %v"
	AskCancelConfirm = "Batalkan dan buang jawaban yang sudah diisi? (y/n)"
	AskLines         = "(%d baris)"

	AskErrDisabled  = "Opsi ini tidak bisa dipilih: %s"
	AskErrRequired  = "Jawaban ini wajib diisi."
	AskErrMinSelect = "Pilih minimal %d."
	AskErrMaxSelect = "Pilih maksimal %d."
	AskErrNoOptions = "Tidak ada pilihan yang tersedia."

	AskKeyNumbers    = "langsung pilih"
	AskKeyToggle     = "centang"
	AskKeyAll        = "semua"
	AskKeyNext       = "lanjut"
	AskKeyPrev       = "sebelumnya"
	AskKeyFilter     = "filter"
	AskKeyScrollPrev = "geser preview"
	AskKeyNewline    = "baris baru"
	AskKeyDone       = "selesai"
	AskKeyCancelEdit = "batal ketik"

	// Demo
	DemoTitle       = "Demo pertanyaan"
	DemoResultTitle = "Hasil jawaban"
	DemoCancelled   = "Dibatalkan oleh user."
)
