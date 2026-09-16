// Package run adalah lapisan eksekusi: definisi command yang akan dijalankan, cara menampilkannya
// persis seperti diketik user, menjalankannya (capture, stream, atau interaktif), dan mencatat riwayat.
//
// Paket ini tidak mengimpor Bubble Tea, supaya bisa dites dan dipakai dari luar TUI.
package run

import (
	"fmt"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

// Line adalah penjelasan satu bagian command: {"-TERM", "sinyal 'berhenti baik-baik'"}.
type Line struct {
	Token   string
	Meaning string
}

// Command adalah satu perintah yang akan dijalankan ubt.
type Command struct {
	Argv        []string   // {"kill","-TERM","1043"} — TANPA sudo
	NeedsRoot   bool       // true dan bukan root → diawali sudo
	Title       string     // "Hentikan proses nginx (PID 1043)"
	Explain     []Line     // penjelasan per bagian command
	Effect      string     // apa yang terjadi setelah dijalankan
	Safer       string     // alternatif yang lebih aman (boleh kosong)
	Risk        risk.Level // tingkat bahaya
	Interactive bool       // butuh terminal penuh: docker exec -it, adduser, editor
	Stdin       string     // isi yang dialirkan ke stdin, mis. menulis file lewat `sudo tee`
	StdinLabel  string     // ringkasan stdin untuk layar konfirmasi: "(public key, 1 baris)"
	Sensitive   bool       // stdin berisi rahasia: jangan tampilkan isinya dan jangan simpan ke riwayat
}

// ExecArgv adalah argv lengkap yang benar-benar dijalankan untuk user dengan status root tertentu.
// nonInteractiveSudo menambahkan `-n` supaya sudo gagal alih-alih meminta password
// (dipakai saat output ditangkap dan kredensial sudo sudah tersimpan).
func (c Command) ExecArgv(isRoot, nonInteractiveSudo bool) []string {
	if !c.NeedsRoot || isRoot {
		return append([]string(nil), c.Argv...)
	}
	prefix := []string{"sudo"}
	if nonInteractiveSudo {
		prefix = append(prefix, "-n")
	}
	return append(prefix, c.Argv...)
}

// DisplayArgv adalah argv seperti yang akan diketik user sendiri (sudo tanpa -n).
func (c Command) DisplayArgv(isRoot bool) []string { return c.ExecArgv(isRoot, false) }

// Preview mengembalikan command persis seperti diketik di shell, dengan quoting yang benar.
func (c Command) Preview(isRoot bool) string { return JoinShell(c.DisplayArgv(isRoot)) }

// Script mengembalikan command sebagai teks shell yang bisa di-copy-paste, termasuk stdin
// sebagai heredoc. Stdin sensitif diganti komentar supaya rahasia tidak ikut tersalin.
func (c Command) Script(isRoot bool) string {
	line := c.Preview(isRoot)
	switch {
	case c.Stdin == "":
		return line
	case c.Sensitive:
		return "# isi stdin disembunyikan karena sensitif: " + c.StdinLabel + "\n" + line + " < FILE"
	}
	delim := "UBT_EOF"
	for i := 2; strings.Contains(c.Stdin, delim); i++ {
		delim = fmt.Sprintf("UBT_EOF_%d", i)
	}
	body := c.Stdin
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return line + " <<'" + delim + "'\n" + body + delim
}

// UsesSudo melaporkan apakah command ini akan diawali sudo.
func (c Command) UsesSudo(isRoot bool) bool { return c.NeedsRoot && !isRoot }

// JoinShell menggabungkan argumen menjadi satu baris shell yang aman di-copy-paste.
func JoinShell(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = QuoteShell(a)
	}
	return strings.Join(quoted, " ")
}

// QuoteShell mengutip satu argumen hanya bila perlu, memakai tanda kutip tunggal.
func QuoteShell(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !isShellSafe(r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_-+=/.,:@%", r)
}
