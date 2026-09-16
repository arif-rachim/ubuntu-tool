// Package shared berisi ketergantungan dan helper tampilan yang dipakai semua layar modul.
package shared

import (
	"fmt"
	"os"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Env adalah ketergantungan layar modul; diisi palsu saat test.
type Env struct {
	Deps     runflow.Deps // alur konfirmasi & eksekusi
	Runner   run.Runner   // membaca data lewat command (capture)
	ProcRoot string       // biasanya /proc
	IsRoot   bool
	UID      int
	Now      func() time.Time
}

// Default membuat Env untuk sistem sungguhan.
func Default() Env {
	deps := runflow.DefaultDeps()
	return Env{
		Deps:     deps,
		Runner:   run.Real{IsRoot: deps.Env.IsRoot},
		ProcRoot: "/proc",
		IsRoot:   deps.Env.IsRoot,
		UID:      os.Getuid(),
		Now:      time.Now,
	}
}

// Ago menampilkan selisih waktu yang mudah dibaca: "5 menit lalu", "3 hari lalu".
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "baru saja"
	case d < time.Hour:
		return fmt.Sprintf("%d menit lalu", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d jam lalu", int(d.Hours()))
	default:
		return fmt.Sprintf("%d hari lalu", int(d.Hours()/24))
	}
}

// Bytes menampilkan ukuran dalam satuan biner dengan koma desimal: "1,2 GiB".
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	s := fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
	for i := range s {
		if s[i] == '.' {
			return s[:i] + "," + s[i+1:]
		}
	}
	return s
}

// Duration menampilkan lama waktu yang mudah dibaca: "2 hari 3 jam", "15 menit".
func Duration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%d hari %d jam", days, hours)
	case hours > 0:
		return fmt.Sprintf("%d jam %d menit", hours, mins)
	default:
		return fmt.Sprintf("%d menit", mins)
	}
}
