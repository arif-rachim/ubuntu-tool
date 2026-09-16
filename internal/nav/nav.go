// Package nav berisi kontrak layar dan pesan navigasi antar layar.
//
// Paket ini sengaja kecil dan tidak bergantung pada paket internal lain, supaya app, ui, dan
// screens bisa memakainya tanpa import melingkar. Hanya root model (internal/app) yang
// memanipulasi tumpukan layar; layar cukup mengirim pesan di bawah ini.
package nav

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Screen adalah satu layar di tumpukan navigasi.
type Screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (Screen, tea.Cmd)
	// View merender isi body dengan ukuran yang tersedia (tanpa header & footer).
	View(width, height int) string
	// Title dipakai untuk breadcrumb.
	Title() string
	// Keys dipakai untuk footer dan layar bantuan.
	Keys() []key.Binding
}

// Typer diimplementasikan layar yang bisa sedang menerima ketikan (filter, text input).
// Saat Typing() true, root hanya menangani ctrl+c; huruf seperti q, r, dan ? diteruskan ke layar.
type Typer interface {
	Typing() bool
}

// BackHandler diimplementasikan layar yang menangani esc sendiri (mis. mundur antar pertanyaan).
// Bila HandlesBack() true, root meneruskan esc ke layar alih-alih mem-pop.
type BackHandler interface {
	HandlesBack() bool
}

// Helper diimplementasikan layar yang punya penjelasan tambahan untuk layar bantuan (?).
type Helper interface {
	HelpText() string
}

// Busy diimplementasikan layar yang sedang menjalankan proses. Saat Busy() true, root meneruskan
// semua tombol (termasuk q dan ctrl+c) ke layar, supaya layar bisa menghentikan prosesnya dengan
// rapi alih-alih ubt keluar dan meninggalkan proses yatim.
type Busy interface {
	Busy() bool
}

// PushMsg menaruh layar baru di atas tumpukan.
type PushMsg struct{ Screen Screen }

// PopMsg mengeluarkan layar teratas dan mengirim Result ke layar di bawahnya lewat ResumedMsg.
type PopMsg struct{ Result any }

// ReplaceMsg mengganti layar teratas.
type ReplaceMsg struct{ Screen Screen }

// ResumedMsg diterima layar yang kembali menjadi teratas setelah layar di atasnya di-pop.
type ResumedMsg struct{ Result any }

// SizeMsg dikirim ke semua layar di tumpukan saat ukuran body berubah.
type SizeMsg struct{ Width, Height int }

// RefreshMsg dikirim ke layar teratas saat user menekan r.
type RefreshMsg struct{}

// Push mengembalikan command untuk menaruh layar baru.
func Push(s Screen) tea.Cmd { return func() tea.Msg { return PushMsg{Screen: s} } }

// Pop mengembalikan command untuk kembali ke layar sebelumnya dengan hasil.
func Pop(result any) tea.Cmd { return func() tea.Msg { return PopMsg{Result: result} } }

// Replace mengembalikan command untuk mengganti layar teratas.
func Replace(s Screen) tea.Cmd { return func() tea.Msg { return ReplaceMsg{Screen: s} } }
