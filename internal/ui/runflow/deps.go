// Package runflow adalah alur baku setiap aksi yang mengubah sistem:
// layar konfirmasi (command persis + penjelasan) → layar eksekusi (progres & output) → hasil.
//
// Modul cukup memanggil nav.Push(runflow.Confirm(plan, deps)) lalu menerima
// nav.ResumedMsg{Result: run.Outcome}.
package runflow

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Deps adalah ketergantungan alur eksekusi; diganti saat test.
type Deps struct {
	Env run.Env
	// Start menjalankan command non-interaktif dengan output bertahap.
	Start run.Starter
	// Interactive menyerahkan terminal ke command (tea.ExecProcess).
	Interactive func(c *exec.Cmd, fn tea.ExecCallback) tea.Cmd
	// History boleh nil (riwayat tidak dicatat).
	History *run.History
}

// DefaultDeps membuat Deps untuk sistem sungguhan.
func DefaultDeps() Deps {
	h, _ := run.DefaultHistory()
	return Deps{
		Env:         run.DetectEnv(),
		Start:       run.StartStream,
		Interactive: tea.ExecProcess,
		History:     h,
	}
}

// Confirm membuat layar konfirmasi untuk sebuah Plan. Hasil akhirnya selalu run.Outcome.
func Confirm(p run.Plan, d Deps) *ConfirmModel { return newConfirm(p, d) }
