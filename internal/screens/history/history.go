// Package history adalah layar Riwayat: command yang pernah dijalankan ubt, detailnya, dan ekspor
// menjadi script bash.
package history

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/cli"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah daftar riwayat (terbaru di atas).
type Model struct {
	env     shared.Env
	hist    *run.History
	entries []run.Entry // terbaru dulu
	err     error
	loaded  bool
	table   ui.Table
	message string
	path    string // file tujuan ekspor yang sedang dikonfirmasi
}

type loadedMsg struct {
	owner   *Model
	entries []run.Entry
	err     error
}

// New membuat layar Riwayat. hist nil memakai lokasi bawaan.
func New(env shared.Env, hist *run.History) *Model {
	if hist == nil {
		h, err := run.DefaultHistory()
		if err == nil {
			hist = h
		}
	}
	return &Model{env: env, hist: hist, table: ui.Table{Columns: []ui.Column{
		{Title: "Waktu", Width: 16},
		{Title: "Status", Width: 6},
		{Title: "Aksi", Width: 20, Flex: 1},
		{Title: "Command", Width: 24, Flex: 2},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Riwayat perintah" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("enter", "detail"), b("e", "ekspor jadi script")}
}

func (m *Model) HelpText() string {
	return "Setiap command yang dijalankan lewat ubt dicatat beserta hasilnya, supaya kamu bisa melihat apa yang pernah diubah di server ini. " +
		"Ekspor mengubah riwayat menjadi script bash untuk dokumentasi atau untuk mengulang setup di server lain (baca dulu sebelum menjalankannya). " +
		"Isi rahasia (mis. password) tidak pernah disimpan. Command setara: ubt history, ubt history export --last 20"
}

func (m *Model) Init() tea.Cmd {
	h := m.hist
	return func() tea.Msg {
		if h == nil {
			return loadedMsg{owner: m, err: errors.New("lokasi riwayat tidak diketahui ($HOME tidak diset)")}
		}
		entries, err := h.Read(0)
		return loadedMsg{owner: m, entries: cli.SortNewestFirst(entries), err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		if msg.owner == m {
			m.loaded, m.entries, m.err = true, msg.entries, msg.err
			m.fill()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled || r.ID != "export" {
				return m, nil
			}
			return m.export(r.Answers)
		case run.Outcome:
			if r.Approved && r.OK() && m.path != "" {
				m.message = "✓ Script ditulis ke " + m.path + ". Baca dulu sebelum menjalankannya: bash " + m.path
			} else if r.Approved {
				m.message = "✗ Gagal menulis script."
			}
			m.path = ""
			return m, m.Init()
		}
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "enter":
			if m.table.Cursor < len(m.entries) {
				return m, nav.Push(newDetail(m.entries[m.table.Cursor]))
			}
		case "e":
			if len(m.entries) == 0 {
				m.message = "Riwayat masih kosong, belum ada yang bisa diekspor."
				return m, nil
			}
			return m, nav.Push(ask.New(ExportForm(len(m.entries), m.env.Now())))
		}
	}
	return m, nil
}

func (m *Model) fill() {
	rows := make([][]string, len(m.entries))
	for i, e := range m.entries {
		status := "ok"
		if cli.EntryStatus(e) != "ok" {
			status = "gagal"
		}
		title := e.Title
		if e.Check {
			title += " (validasi)"
		}
		rows[i] = []string{e.Time.Local().Format("2006-01-02 15:04"), status, title, cli.EntryCommand(e)}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		if c == 1 && r < len(m.entries) && cli.EntryStatus(m.entries[r]) != "ok" {
			return ui.Current.Danger
		}
		return lipgloss.NewStyle()
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca riwayat…")
	}
	path := ""
	if m.hist != nil {
		path = m.hist.Path
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Riwayat (%d command)", len(m.entries))) + "   " + t.Muted.Render("disimpan di "+path)}
	if m.err != nil {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err.Error()), width, " "))
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.entries) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada command yang dijalankan", "", "setiap aksi yang kamu setujui di modul mana pun akan tercatat di sini", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ExportForm menanyakan cakupan dan lokasi ekspor.
func ExportForm(total int, now time.Time) ask.Form {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "ubt-riwayat-"+now.Format("20060102-1504")+".sh")
	var opts []ask.Option
	for _, n := range []int{10, 25, 50} {
		if n < total {
			opts = append(opts, ask.Option{Value: strconv.Itoa(n), Label: fmt.Sprintf("%d command terakhir", n)})
		}
	}
	opts = append(opts, ask.Option{Value: "0", Label: fmt.Sprintf("Semua (%d command)", total), Recommended: true})
	return ask.Form{ID: "export", Title: "Ekspor riwayat", Questions: []ask.Question{
		{ID: "last", Header: "Cakupan", Kind: ask.Single, Prompt: "Command mana yang diekspor?", Options: opts},
		{ID: "failed", Header: "Gagal", Kind: ask.Confirm, Prompt: "Ikutkan command yang gagal (sebagai komentar)?", Default: []string{ask.ValueNo}, Options: []ask.Option{
			{Label: "Ya", Description: "Berguna sebagai catatan apa saja yang pernah dicoba."},
			{Label: "Tidak", Description: "Script hanya berisi command yang berhasil.", Recommended: true},
		}},
		{ID: "path", Header: "File", Kind: ask.Text, Prompt: "Simpan script ke mana?", Default: []string{def},
			Help: "File dibuat dengan izin 0700 (hanya kamu yang bisa membaca) karena bisa berisi path dan isi file konfigurasi server.",
			Validate: func(s string) error {
				s = strings.TrimSpace(s)
				if !filepath.IsAbs(s) {
					return errors.New("tulis path lengkap, mis. /home/kamu/setup.sh")
				}
				if st, err := os.Stat(filepath.Dir(s)); err != nil || !st.IsDir() {
					return errors.New("folder tujuan tidak ada")
				}
				if _, err := os.Stat(s); err == nil {
					return ask.Warn("file sudah ada dan akan ditimpa")
				}
				return nil
			}},
	}}
}

func (m *Model) export(a ask.Answers) (nav.Screen, tea.Cmd) {
	last, _ := strconv.Atoi(a["last"].Value())
	entries, err := m.hist.Read(last)
	if err != nil {
		m.message = "✗ " + err.Error()
		return m, nil
	}
	path := strings.TrimSpace(a["path"].Value())
	script := cli.ExportScript(entries, a["failed"].Yes(), m.env.Now())
	m.path = path
	return m, nav.Push(runflow.Confirm(WritePlan(path, script), m.env.Deps))
}

// WritePlan menulis script ekspor (tanpa root).
func WritePlan(path, script string) run.Plan {
	c := run.Command{
		Title: "Tulis script " + filepath.Base(path), Argv: []string{"install", "-m", "0700", "/dev/stdin", path},
		Stdin: script, StdinLabel: fmt.Sprintf("(script, %d baris)", strings.Count(script, "\n")),
		Explain: []run.Line{
			{Token: "install -m 0700", Meaning: "tulis file yang hanya bisa dibaca & dijalankan olehmu"},
			{Token: "/dev/stdin", Meaning: "isi file diambil dari teks di bawah"},
			{Token: path, Meaning: "lokasi file"},
		},
		Effect: "Script hanya dibuat, tidak dijalankan.",
		Risk:   risk.Safe,
	}
	if _, err := os.Stat(path); err == nil {
		c.Risk = risk.Caution
		c.Effect = "File yang sudah ada di lokasi ini ditimpa. Script hanya dibuat, tidak dijalankan."
	}
	return run.Single(c)
}

func duration(ms int64) string {
	switch d := time.Duration(ms) * time.Millisecond; {
	case d < time.Second:
		return fmt.Sprintf("%d ms", ms)
	case d < time.Minute:
		return fmt.Sprintf("%.1f detik", d.Seconds())
	default:
		return shared.Duration(d)
	}
}

// detailModel menampilkan satu entri lengkap.
type detailModel struct {
	e      run.Entry
	viewer ui.Viewer
}

func newDetail(e run.Entry) *detailModel {
	d := &detailModel{e: e}
	script := cli.ExportScript([]run.Entry{e}, true, e.Time)
	// Buang header script; cukup blok command-nya.
	lines := strings.Split(strings.TrimRight(script, "\n"), "\n")
	for len(lines) > 0 && !strings.HasPrefix(lines[0], "# "+e.Title) {
		lines = lines[1:]
	}
	d.viewer.Lines = lines
	return d
}

func (d *detailModel) Title() string { return d.e.Title }
func (d *detailModel) Init() tea.Cmd { return nil }
func (d *detailModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "gulir"))}
}

func (d *detailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		d.viewer.HandleKey(k.String())
	}
	return d, nil
}

func (d *detailModel) View(width, height int) string {
	t := ui.Current
	e := d.e
	status := t.Success.Render("berhasil")
	if cli.EntryStatus(e) != "ok" {
		status = t.Danger.Render(cli.EntryStatus(e))
		if e.Error != "" {
			status += t.Muted.Render(" — " + e.Error)
		}
	}
	top := "\n" + ui.Detail([]ui.Pair{
		{Key: "Waktu", Value: e.Time.Local().Format("2006-01-02 15:04:05")},
		{Key: "Hasil", Value: status},
		{Key: "Durasi", Value: duration(e.DurationMs)},
	}, width) + "\n"
	return top + "\n" + d.viewer.View(width, height-lipgloss.Height(top)-1, false)
}
