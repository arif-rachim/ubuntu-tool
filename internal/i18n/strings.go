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

// Layar konfirmasi & eksekusi
const (
	RiskSafe      = "AMAN"
	RiskCaution   = "BERISIKO"
	RiskDangerous = "BAHAYA"

	ConfirmTitle        = "Konfirmasi"
	ConfirmCommand      = "Perintah yang akan dijalankan:"
	ConfirmSteps        = "Langkah-langkah yang akan dijalankan (%d):"
	ConfirmCheck        = "validasi — bila gagal, langkah berikutnya tidak dijalankan"
	ConfirmMeaning      = "Artinya:"
	ConfirmStdin        = "Isi yang dikirim ke stdin"
	ConfirmStdinHidden  = "Isi disembunyikan karena sensitif"
	ConfirmStdinMore    = "… %d baris lagi"
	ConfirmEffect       = "Efek:"
	ConfirmSafer        = "Lebih aman:"
	ConfirmInteractive  = "Perintah ini interaktif: layar ubt akan diganti terminal biasa sampai perintah selesai."
	ConfirmStopOnFail   = "Bila satu langkah gagal, langkah berikutnya tidak dijalankan."
	ConfirmSudoChecking = "Memeriksa sudo…"
	ConfirmSudoPasswd   = "sudo akan meminta password kamu (sekali, sebelum langkah pertama yang butuh root)."
	ConfirmSudoCached   = "sudo tidak akan meminta password (izin sudo masih tersimpan)."
	ConfirmSudoRoot     = "Kamu root: perintah dijalankan tanpa sudo."
	ConfirmSudoMissing  = "sudo tidak terinstall. Jalankan ubt sebagai root untuk aksi ini."
	ConfirmArmed        = "Aksi ini BERBAHAYA. Tekan y sekali lagi untuk benar-benar menjalankan, tombol lain untuk batal."
	ConfirmCopied       = "Command disalin ke clipboard (butuh terminal yang mendukung OSC 52)."
	ConfirmKeyRun       = "jalankan"
	ConfirmKeyCancel    = "batal"
	ConfirmKeyCopy      = "salin command"
	ConfirmKeyScroll    = "gulir"

	ExecTitle          = "Menjalankan"
	ExecOutputOf       = "Output langkah %d"
	ExecNoOutput       = "(tidak ada output)"
	ExecInteractiveOut = "(output tampil langsung di terminal saat perintah berjalan)"
	ExecSkipped        = "dilewati"
	ExecCancelled      = "dihentikan"
	ExecAllOK          = "Semua langkah berhasil."
	ExecFailedAt       = "Gagal di langkah %d (kode keluar %d)."
	ExecFailedErr      = "Gagal di langkah %d: %v"
	ExecCancelledAt    = "Dihentikan di langkah %d."
	ExecAlreadyRan     = "Langkah %s sudah terlanjur dijalankan dan tidak dibatalkan otomatis."
	ExecSudoFailed     = "izin sudo tidak didapat (password salah atau dibatalkan)"
	ExecHistoryFailed  = "Riwayat tidak tersimpan: %v"
	ExecPressEnter     = "Selesai (kode keluar %s). Tekan enter untuk kembali ke ubt…"
	ExecKeyStop        = "hentikan"
	ExecKeyDone        = "selesai"
	ExecKeyPickStep    = "pilih langkah"
)
