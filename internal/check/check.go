// Package check mendefinisikan pemeriksaan bertahap yang berhenti di langkah pertama yang menemukan
// penyebab masalah. Dipakai wizard konektivitas (Network) dan modul Diagnosa.
package check

import "context"

// Status adalah hasil satu langkah pemeriksaan.
type Status int

const (
	OK   Status = iota
	Warn        // ada catatan, tetapi pemeriksaan tetap lanjut
	Fail        // penyebab ditemukan: pemeriksaan berhenti
	Skip        // tidak relevan untuk target ini
)

// Result adalah hasil satu langkah.
type Result struct {
	Status  Status
	Summary string // satu baris hasil
	Explain string // artinya bagi user & langkah berikutnya
	Next    string // ID modul yang disarankan untuk dibuka (opsional), mis. "firewall"
}

// Step adalah satu langkah pemeriksaan.
type Step struct {
	Title      string
	Equivalent string // command yang bisa dijalankan user sendiri
	Run        func(ctx context.Context) Result
}
