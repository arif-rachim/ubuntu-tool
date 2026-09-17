package run

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// IsRoot melaporkan apakah ubt berjalan sebagai root.
func IsRoot() bool { return os.Geteuid() == 0 }

// SudoState menjelaskan kesiapan sudo untuk menjalankan langkah yang butuh root.
type SudoState int

const (
	SudoNotNeeded   SudoState = iota // tidak ada langkah root, atau ubt sudah root
	SudoCached                       // kredensial masih tersimpan, tidak akan minta password
	SudoNeedsPasswd                  // sudo akan meminta password
	SudoMissing                      // program sudo tidak ada
)

// Env adalah informasi lingkungan yang memengaruhi cara command dijalankan.
// Dibuat lewat DetectEnv; bisa diisi manual saat test.
type Env struct {
	IsRoot bool
	// SudoCheck mengembalikan status sudo untuk user non-root. Dipanggil di goroutine terpisah.
	SudoCheck func(ctx context.Context) SudoState
}

// DetectEnv membaca lingkungan sungguhan.
func DetectEnv() Env {
	return Env{IsRoot: IsRoot(), SudoCheck: CheckSudo}
}

// CheckSudo memeriksa apakah sudo ada dan apakah kredensialnya masih tersimpan (`sudo -n true`).
func CheckSudo(ctx context.Context) SudoState {
	if _, err := exec.LookPath("sudo"); err != nil {
		return SudoMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "-n", "true")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if err := cmd.Run(); err != nil {
		return SudoNeedsPasswd
	}
	return SudoCached
}

// SudoValidate adalah command interaktif untuk meminta password sudo sekali di awal,
// supaya langkah-langkah berikutnya bisa ditangkap outputnya dengan `sudo -n`.
func SudoValidate() Command {
	return Command{
		Argv:        []string{"sudo", "-v"},
		Title:       "Minta izin sudo",
		Interactive: true,
	}
}
