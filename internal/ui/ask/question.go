// Package ask menyediakan pertanyaan interaktif bergaya prompt pilihan Claude Code:
// setiap opsi punya penjelasan, ada rekomendasi, bisa dipilih dengan angka, dan selalu
// ada jalan keluar "Lainnya…". Semua wizard di ubt memakai paket ini.
package ask

import (
	"context"
	"errors"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

// Kind adalah jenis pertanyaan.
type Kind int

const (
	Single   Kind = iota // pilih satu
	Multi                // pilih satu atau lebih
	Text                 // satu baris teks
	TextArea             // teks banyak baris (mis. paste public key)
	Confirm              // ya / tidak
)

// Nilai Answer.Values untuk pertanyaan Confirm.
const (
	ValueYes = "yes"
	ValueNo  = "no"
)

// Option adalah satu pilihan pada pertanyaan Single atau Multi.
type Option struct {
	Label       string
	Value       string
	Description string
	Meta        string     // info ringkas rata kanan, mis. "~1,2 GB"
	Recommended bool       // dipindah ke urutan pertama + label "(Disarankan)"
	Preview     string     // isi file/command yang dihasilkan bila opsi ini dipilih
	Risk        risk.Level // opsi berisiko diberi penanda, bukan hanya warna
	Disabled    string     // bila tidak kosong: alasan opsi tidak bisa dipilih
}

// Question adalah satu pertanyaan di Form.
type Question struct {
	ID          string
	Header      string // chip pendek, ≤12 karakter
	Prompt      string // kalimat tanya lengkap
	Help        string // penjelasan konsep untuk pemula, tampil di layar bantuan (?)
	Kind        Kind
	Options     []Option
	Load        func(ctx context.Context) ([]Option, error) // opsi dinamis; menggantikan Options
	Other       bool                                        // tambahkan opsi "Lainnya…"
	Default     []string                                    // nilai awal
	Placeholder string
	Optional    bool                           // Text/TextArea boleh kosong; Multi boleh tanpa pilihan
	Validate    func(string) error             // Text/TextArea/Other; kembalikan Warn(...) untuk peringatan yang tidak memblokir
	Min, Max    int                            // Multi: batas jumlah pilihan; Min 0 berarti 1 (atau 0 bila Optional), Max 0 = tanpa batas
	When        func(Answers) bool             // tampilkan hanya bila true
	Summary     func(selected []Option) string // Multi: ringkasan di bawah daftar, mis. total ukuran
}

// Form adalah sekumpulan pertanyaan yang dijawab berurutan.
type Form struct {
	ID         string // penanda untuk pemanggil yang membuka beberapa form (dikembalikan di Result.ID)
	Title      string
	Questions  []Question
	SkipReview bool // lewati layar ringkasan (ringkasan hanya muncul bila ada >1 pertanyaan)
}

// Answer adalah jawaban satu pertanyaan.
type Answer struct {
	Values []string // Single: 1 elemen; Multi: 0..n; Confirm: ValueYes/ValueNo
	Other  string   // teks bila "Lainnya…" dipilih
	Text   string   // Text/TextArea
}

// Value mengembalikan nilai tunggal: teks, teks "Lainnya…", atau nilai pertama.
func (a Answer) Value() string {
	switch {
	case a.Text != "":
		return a.Text
	case a.Other != "":
		return a.Other
	case len(a.Values) > 0:
		return a.Values[0]
	}
	return ""
}

// Has melaporkan apakah nilai v dipilih.
func (a Answer) Has(v string) bool {
	for _, x := range a.Values {
		if x == v {
			return true
		}
	}
	return false
}

// Yes melaporkan apakah jawaban Confirm adalah ya.
func (a Answer) Yes() bool { return a.Has(ValueYes) }

// Answers memetakan Question.ID ke jawabannya.
type Answers map[string]Answer

// Result dikirim lewat nav.PopMsg saat form selesai atau dibatalkan.
type Result struct {
	ID        string
	Form      string
	Answers   Answers
	Cancelled bool
}

// Warning adalah hasil validasi yang ditampilkan tetapi tidak memblokir.
type Warning struct{ Msg string }

func (w *Warning) Error() string { return w.Msg }

// Warn membuat peringatan yang tidak memblokir.
func Warn(msg string) error { return &Warning{Msg: msg} }

// isWarning melaporkan apakah err hanya peringatan.
func isWarning(err error) bool {
	var w *Warning
	return errors.As(err, &w)
}

// otherValue adalah nilai internal untuk baris "Lainnya…".
const otherValue = "\x00other"

// sortRecommended memindahkan opsi Recommended ke depan dengan urutan tetap stabil.
func sortRecommended(opts []Option) []Option {
	out := make([]Option, 0, len(opts))
	for _, o := range opts {
		if o.Recommended {
			out = append(out, o)
		}
	}
	for _, o := range opts {
		if !o.Recommended {
			out = append(out, o)
		}
	}
	return out
}

func matchesFilter(o Option, filter string) bool {
	if filter == "" {
		return true
	}
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(o.Label), f) ||
		strings.Contains(strings.ToLower(o.Description), f) ||
		strings.Contains(strings.ToLower(o.Value), f)
}
