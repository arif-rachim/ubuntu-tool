package demo

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// RunModel adalah layar tersembunyi `ubt --demo-run` untuk mencoba alur konfirmasi & eksekusi
// dengan aksi nyata yang aman.
type RunModel struct {
	deps    runflow.Deps
	last    *run.Outcome
	started bool
}

// NewRun membuat layar demo eksekusi.
func NewRun(d runflow.Deps) *RunModel { return &RunModel{deps: d} }

func (m *RunModel) Title() string { return "Demo eksekusi" }

func (m *RunModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "pilih aksi lain"))}
}

func (m *RunModel) Init() tea.Cmd {
	if m.started {
		return nil
	}
	m.started = true
	return nav.Push(ask.New(actionForm()))
}

func (m *RunModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nav.Pop(nil)
			}
			if p, ok := demoPlans()[r.Answers["aksi"].Value()]; ok {
				return m, nav.Push(runflow.Confirm(p, m.deps))
			}
		case run.Outcome:
			m.last = &r
		}
	case tea.KeyPressMsg:
		if msg.String() == "enter" {
			return m, nav.Push(ask.New(actionForm()))
		}
	}
	return m, nil
}

func (m *RunModel) View(width, _ int) string {
	t := ui.Current
	if m.last == nil {
		return ""
	}
	o := m.last
	var b strings.Builder
	b.WriteString("\n " + t.Title.Render("Hasil: "+o.Plan.Title) + "\n\n")
	switch {
	case !o.Approved:
		b.WriteString(" " + t.Subtle.Render("Dibatalkan di layar konfirmasi. Tidak ada yang dijalankan.") + "\n")
	case o.OK():
		b.WriteString(" " + t.Success.Render("✓ Semua langkah berhasil.") + "\n")
	default:
		b.WriteString(" " + t.Danger.Render(fmt.Sprintf("✗ Tidak semua langkah berhasil (%d dijalankan).", len(o.Executed()))) + "\n")
	}
	if o.Approved {
		b.WriteString("\n " + t.Subtle.Render("Tercatat di riwayat: ~/.config/ubt/history.log") + "\n")
	}
	b.WriteString("\n " + t.Muted.Render("Tekan enter untuk mencoba aksi lain.") + "\n")
	return b.String()
}

func actionForm() ask.Form {
	return ask.Form{
		Title: "Pilih aksi demo",
		Questions: []ask.Question{{
			ID:     "aksi",
			Prompt: "Aksi mana yang ingin dicoba?",
			Kind:   ask.Single,
			Options: []ask.Option{
				{Value: "version", Label: "Cek versi systemd", Recommended: true, Description: "Satu command aman, output ditangkap: systemctl --version"},
				{Value: "plan", Label: "Plan 2 langkah + validasi", Risk: risk.Caution, Description: "Buat /tmp/ubt-demo, validasi, lalu tulis file lewat stdin"},
				{Value: "fail", Label: "Plan yang gagal di tengah", Description: "Langkah 2 gagal, langkah 3 tidak dijalankan"},
				{Value: "stream", Label: "Output bertahap (bisa dihentikan)", Description: "ping 127.0.0.1 sepuluh kali; tekan ctrl+c untuk menghentikan"},
				{Value: "sudo", Label: "Butuh sudo", Description: "id -un sebagai root; sudo meminta password bila belum tersimpan"},
				{Value: "interactive", Label: "Command interaktif", Description: "Layar ubt diganti terminal biasa untuk menjawab pertanyaan"},
				{Value: "danger", Label: "Aksi berbahaya (simulasi)", Risk: risk.Dangerous, Description: "Menghapus /tmp/ubt-demo; butuh y dua kali"},
			},
		}},
	}
}

func demoPlans() map[string]run.Plan {
	return map[string]run.Plan{
		"version": run.Single(run.Command{
			Title: "Cek versi systemd",
			Argv:  []string{"systemctl", "--version"},
			Explain: []run.Line{
				{Token: "systemctl", Meaning: "alat untuk mengelola service di systemd"},
				{Token: "--version", Meaning: "tampilkan versi systemd dan fitur yang aktif"},
			},
			Effect: "Hanya membaca, tidak mengubah apa pun.",
		}),
		"plan": {
			Title: "Buat file demo di /tmp",
			Steps: []run.Command{
				{
					Title:   "Buat folder demo",
					Argv:    []string{"mkdir", "-p", "/tmp/ubt-demo"},
					Explain: []run.Line{{Token: "-p", Meaning: "buat juga folder induk; tidak error bila sudah ada"}},
				},
				{
					Title:      "Tulis file lewat stdin",
					Argv:       []string{"tee", "/tmp/ubt-demo/halo.txt"},
					Stdin:      "Halo dari ubt!\nFile ini ditulis lewat stdin.\n",
					StdinLabel: "(teks, 2 baris)",
					Risk:       risk.Caution,
					Explain:    []run.Line{{Token: "tee", Meaning: "tulis stdin ke file sekaligus tampilkan di layar"}},
					Effect:     "File /tmp/ubt-demo/halo.txt dibuat atau ditimpa.",
				},
			},
			Check: &run.Command{
				Title:   "Pastikan folder ada",
				Argv:    []string{"test", "-d", "/tmp/ubt-demo"},
				Explain: []run.Line{{Token: "test -d", Meaning: "berhasil (exit 0) bila path adalah folder"}},
			},
		},
		"fail": {
			Title: "Plan yang gagal di tengah",
			Steps: []run.Command{
				{Title: "Langkah yang berhasil", Argv: []string{"true"}},
				{Title: "Langkah yang gagal", Argv: []string{"ls", "/tidak-ada-ubt-demo"}},
				{Title: "Langkah yang tidak akan dijalankan", Argv: []string{"echo", "seharusnya tidak tampil"}},
			},
		},
		"stream": run.Single(run.Command{
			Title: "Ping localhost",
			Argv:  []string{"ping", "-c", "10", "-i", "0.5", "127.0.0.1"},
			Explain: []run.Line{
				{Token: "-c 10", Meaning: "kirim 10 paket lalu berhenti"},
				{Token: "-i 0.5", Meaning: "jeda setengah detik antar paket"},
			},
		}),
		"sudo": run.Single(run.Command{
			Title:     "Tampilkan user efektif sebagai root",
			Argv:      []string{"id", "-un"},
			NeedsRoot: true,
			Explain:   []run.Line{{Token: "id -un", Meaning: "nama user yang menjalankan command"}},
			Effect:    "Menampilkan \"root\" bila sudo berhasil.",
		}),
		"interactive": run.Single(run.Command{
			Title:       "Tanya nama",
			Argv:        []string{"bash", "-c", `read -r -p "Siapa namamu? " n; echo "Halo, $n!"`},
			Interactive: true,
		}),
		"danger": run.Single(run.Command{
			Title:   "Hapus folder demo",
			Argv:    []string{"rm", "-rf", "/tmp/ubt-demo"},
			Risk:    risk.Dangerous,
			Explain: []run.Line{{Token: "-rf", Meaning: "hapus isi folder secara rekursif tanpa bertanya"}},
			Effect:  "Folder /tmp/ubt-demo dan isinya hilang permanen.",
			Safer:   "periksa isinya dulu dengan ls -la /tmp/ubt-demo",
		}),
	}
}
