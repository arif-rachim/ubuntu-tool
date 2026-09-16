package run

import "github.com/arif-rachim/ubuntu-tool/internal/risk"

// Plan adalah beberapa command yang dijalankan berurutan dengan satu persetujuan.
// Eksekusi berhenti di langkah pertama yang gagal.
type Plan struct {
	Title string
	Steps []Command
	// Check opsional: validasi yang dijalankan tepat sebelum langkah terakhir
	// (sshd -t, nginx -t, visudo -cf). Bila gagal, langkah terakhir tidak dijalankan.
	Check *Command
}

// Single membungkus satu command menjadi Plan.
func Single(c Command) Plan { return Plan{Title: c.Title, Steps: []Command{c}} }

// Step adalah satu langkah eksekusi setelah Check disisipkan.
type Step struct {
	Command Command
	IsCheck bool
}

// Sequence mengembalikan urutan langkah sebenarnya, dengan Check disisipkan sebelum langkah terakhir.
func (p Plan) Sequence() []Step {
	out := make([]Step, 0, len(p.Steps)+1)
	for i, c := range p.Steps {
		if p.Check != nil && i == len(p.Steps)-1 {
			out = append(out, Step{Command: *p.Check, IsCheck: true})
		}
		out = append(out, Step{Command: c})
	}
	if p.Check != nil && len(p.Steps) == 0 {
		out = append(out, Step{Command: *p.Check, IsCheck: true})
	}
	return out
}

// Risk adalah tingkat bahaya tertinggi dari semua langkah.
func (p Plan) Risk() risk.Level {
	var max risk.Level
	for _, s := range p.Sequence() {
		if s.Command.Risk > max {
			max = s.Command.Risk
		}
	}
	return max
}

// NeedsRoot melaporkan apakah ada langkah yang butuh root.
func (p Plan) NeedsRoot() bool {
	for _, s := range p.Sequence() {
		if s.Command.NeedsRoot {
			return true
		}
	}
	return false
}

// StepStatus adalah status satu langkah.
type StepStatus int

const (
	StatusPending   StepStatus = iota
	StatusRunning              // sedang berjalan
	StatusOK                   // exit 0
	StatusFailed               // exit bukan 0 atau gagal dijalankan
	StatusSkipped              // tidak dijalankan karena langkah sebelumnya gagal
	StatusCancelled            // dihentikan user
)

// StepResult adalah hasil satu langkah.
type StepResult struct {
	Step     Step
	Status   StepStatus
	ExitCode int
	Err      error
	Output   []string // gabungan stdout+stderr (dibatasi), kosong untuk command interaktif
}

// Outcome dikirim ke layar pemanggil setelah alur konfirmasi & eksekusi selesai.
type Outcome struct {
	Plan     Plan
	Approved bool // false: user membatalkan di layar konfirmasi, tidak ada yang dijalankan
	Results  []StepResult
}

// OK melaporkan apakah semua langkah berhasil.
func (o Outcome) OK() bool {
	if !o.Approved || len(o.Results) == 0 {
		return false
	}
	for _, r := range o.Results {
		if r.Status != StatusOK {
			return false
		}
	}
	return true
}

// Executed mengembalikan langkah yang benar-benar sempat dijalankan.
func (o Outcome) Executed() []StepResult {
	var out []StepResult
	for _, r := range o.Results {
		if r.Status == StatusOK || r.Status == StatusFailed || r.Status == StatusCancelled {
			out = append(out, r)
		}
	}
	return out
}
