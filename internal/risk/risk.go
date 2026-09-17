// Package risk mendefinisikan tingkat bahaya sebuah aksi.
//
// Dipakai bersama oleh lapisan eksekusi (run) dan tampilan (ui) tanpa membuat keduanya saling import.
package risk

// Level adalah tingkat bahaya sebuah aksi atau opsi.
type Level int

const (
	Safe      Level = iota // tidak mengubah apa pun, atau mudah dibatalkan
	Caution                // mengubah sistem, bisa dipulihkan
	Dangerous              // bisa menghapus data atau memutus akses
)
