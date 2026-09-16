package disk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdisk "github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// cleanupForm menanyakan kandidat mana yang dibersihkan.
func cleanupForm(cands []sysdisk.Candidate) ask.Form {
	sizes := map[string]int64{}
	var opts []ask.Option
	for _, c := range cands {
		o := ask.Option{Value: c.ID, Label: c.Label, Description: c.Detail}
		switch {
		case c.Size > 0:
			o.Meta = "~" + shared.Bytes(c.Size)
		case c.SizeLabel != "":
			o.Meta = c.SizeLabel
		}
		if !c.Available {
			o.Disabled = c.Why
		}
		for _, s := range c.Steps {
			if s.Risk > o.Risk {
				o.Risk = s.Risk
			}
		}
		sizes[c.ID] = c.Size
		opts = append(opts, o)
	}
	return ask.Form{
		ID:    "cleanup",
		Title: "Bersih-bersih disk",
		Questions: []ask.Question{{
			ID:      "pilih",
			Prompt:  "Bersihkan apa saja?",
			Kind:    ask.Multi,
			Options: opts,
			Help:    "Semua kandidat ini hanya menghapus data yang bisa dibuat ulang atau tidak dipakai lagi. Tidak ada file milikmu yang dihapus. Untuk file lain, cari dulu lewat \"apa yang makan tempat\".",
			Summary: func(sel []ask.Option) string {
				var total int64
				for _, o := range sel {
					total += sizes[o.Value]
				}
				return fmt.Sprintf("Perkiraan ruang yang kembali: %s", shared.Bytes(total))
			},
		}},
	}
}

// cleanupPlan menggabungkan langkah kandidat yang dipilih.
func cleanupPlan(cands []sysdisk.Candidate, ids []string) run.Plan {
	chosen := map[string]bool{}
	for _, id := range ids {
		chosen[id] = true
	}
	p := run.Plan{Title: "Bersih-bersih disk"}
	for _, c := range cands {
		if chosen[c.ID] && c.Available {
			p.Steps = append(p.Steps, c.Steps...)
		}
	}
	return p
}

// swapForm menanyakan ukuran dan lokasi swapfile.
func swapForm(d Data) ask.Form {
	rec := sysdisk.RecommendedSwap(d.RAMBytes)
	root, _ := sysdisk.MountFor(d.Mounts, "/")
	existing := ""
	if len(d.Swaps) > 0 {
		var names []string
		for _, s := range d.Swaps {
			names = append(names, fmt.Sprintf("%s (%s)", s.Name, shared.Bytes(s.Size)))
		}
		existing = "Sudah ada swap aktif: " + strings.Join(names, ", ") + ". Swapfile baru akan menambah total swap. "
	}
	var opts []ask.Option
	for _, gib := range []int{1, 2, 4, 8} {
		o := ask.Option{
			Value:       strconv.Itoa(gib),
			Label:       fmt.Sprintf("%d GiB", gib),
			Recommended: gib == rec,
		}
		if gib == rec {
			o.Description = fmt.Sprintf("Sesuai RAM %s.", shared.Bytes(d.RAMBytes))
		}
		need := int64(gib+1) << 30
		if root.Avail > 0 && root.Avail < need {
			o.Disabled = fmt.Sprintf("sisa disk / hanya %s", shared.Bytes(root.Avail))
		}
		opts = append(opts, o)
	}
	return ask.Form{
		ID:    "swap",
		Title: "Buat swapfile",
		Questions: []ask.Question{
			{
				ID:      "size",
				Header:  "Ukuran",
				Prompt:  "Berapa ukuran swapfile?",
				Kind:    ask.Single,
				Options: opts,
				Help: existing + "Swap adalah ruang disk yang dipakai saat RAM penuh. Lebih lambat dari RAM, tetapi mencegah aplikasi dimatikan paksa (OOM) saat pemakaian memori melonjak. " +
					"Aturan umum: RAM ≤ 2 GiB → 2 GiB, RAM 2–8 GiB → sama dengan RAM, RAM > 8 GiB → 4 GiB.",
			},
			{
				ID:          "path",
				Header:      "Lokasi",
				Prompt:      "Di mana swapfile disimpan?",
				Kind:        ask.Text,
				Default:     []string{"/swapfile"},
				Placeholder: "/swapfile",
				Help:        "Biasanya /swapfile di partisi root. Ubuntu 24.04 bawaan memakai /swap.img.",
				Validate:    validateSwapPath,
			},
		},
	}
}

func validateSwapPath(s string) error {
	s = strings.TrimSpace(s)
	if !filepath.IsAbs(s) || strings.ContainsAny(s, " \t\n") {
		return errors.New("harus path absolut tanpa spasi, mis. /swapfile")
	}
	if _, err := os.Stat(s); err == nil {
		return errors.New("file " + s + " sudah ada; pilih nama lain")
	}
	if _, err := os.Stat(filepath.Dir(s)); err != nil {
		return errors.New("folder " + filepath.Dir(s) + " tidak ada")
	}
	return nil
}

// restartPlan me-restart unit pemilik file terhapus supaya ruang disk kembali.
func restartPlan(unit string, userUnit bool, file sysdisk.DeletedFile) run.Plan {
	argv := []string{"systemctl", "restart", unit}
	root := true
	if userUnit {
		argv = []string{"systemctl", "--user", "restart", unit}
		root = false
	}
	return run.Single(run.Command{
		Title:     "Restart " + unit,
		Argv:      argv,
		NeedsRoot: root,
		Explain: []run.Line{
			{Token: "restart", Meaning: "hentikan lalu jalankan lagi; proses baru tidak lagi memegang file yang sudah dihapus"},
			{Token: unit, Meaning: "unit yang masih membuka " + file.Path},
		},
		Effect: fmt.Sprintf("Layanan terputus sebentar. Ruang %s kembali setelah proses lama berhenti.", shared.Bytes(file.Size)),
		Risk:   risk.Caution,
	})
}
