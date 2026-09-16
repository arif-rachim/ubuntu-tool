// Package logs adalah layar modul Log: "ada error apa di server ini?".
package logs

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// JournalDir adalah folder journal persisten.
const JournalDir = "/var/log/journal"

// preset adalah satu tampilan log siap pakai.
type preset struct {
	id    string
	label string
	desc  string
	query syslogs.Query
}

func presets() []preset {
	return []preset{
		{"errors", "Error sejak boot ini", "Pesan tingkat error, kritis, dan lebih parah sejak server terakhir dinyalakan.", syslogs.Query{Boot: syslogs.BootPtr(0), Priority: syslogs.PrioErr}},
		{"all", "Semua log boot ini (300 terbaru)", "Semua pesan terbaru dari semua service.", syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1}},
		{"hour", "Satu jam terakhir", "Semua pesan dalam 60 menit terakhir.", syslogs.Query{Priority: -1, Since: "1 hour ago"}},
		{"kernel", "Pesan kernel", "Driver, disk, jaringan, OOM killer, dan masalah perangkat keras (dmesg).", syslogs.Query{Boot: syslogs.BootPtr(0), Kernel: true, Priority: -1}},
		{"prev", "Boot sebelumnya: kenapa server restart?", "Peringatan & error menjelang server mati/restart terakhir kali.", syslogs.Query{Boot: syslogs.BootPtr(-1), Priority: syslogs.PrioWarning, Lines: 200}},
		{"grep", "Cari kata kunci…", "Cari di journal boot ini, mis. \"Failed password\" atau nama aplikasi.", syslogs.Query{}},
		{"unit", "Log satu service…", "Semua pesan dari satu unit systemd, mis. nginx.service.", syslogs.Query{}},
		{"files", "File di /var/log", "auth.log, syslog, log nginx/apache, riwayat apt, dan lainnya.", syslogs.Query{}},
	}
}

// Model adalah menu modul Log.
type Model struct {
	env        shared.Env
	journalDir string
	health     syslogs.Health
	loaded  bool
	cursor  int
	items   []preset
	message string
}

type healthMsg struct {
	owner  *Model
	health syslogs.Health
}

// New membuat menu modul Log.
func New(env shared.Env) *Model { return &Model{env: env, journalDir: JournalDir, items: presets()} }

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Log" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	keys := []key.Binding{b("↑↓", "pilih"), b("enter", "buka")}
	if m.loaded && !m.health.Persistent {
		keys = append(keys, b("p", "simpan log permanen"))
	}
	if m.health.Usage > 1<<30 {
		keys = append(keys, b("v", "rampingkan journal"))
	}
	return keys
}

func (m *Model) HelpText() string {
	return "Hampir semua program di Ubuntu mencatat log ke journal systemd, dibaca dengan journalctl. Tingkat 0–3 adalah error, 4 peringatan, 6 informasi. " +
		"Sebagian aplikasi (nginx, apache) juga menulis file sendiri di /var/log. User di grup adm bisa membaca log sistem tanpa sudo."
}

func (m *Model) Init() tea.Cmd {
	r, dir := m.env.Runner, m.journalDir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return healthMsg{owner: m, health: syslogs.ReadHealth(ctx, r, dir)}
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case healthMsg:
		if msg.owner == m {
			m.health, m.loaded = msg.health, true
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			switch r.ID {
			case "grep":
				word := r.Answers["kata"].Value()
				return m, nav.Push(NewEntries(m.env, "Cari: "+word, syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1, Grep: word}))
			case "unit":
				unit := normalizeUnit(r.Answers["unit"].Value())
				return m, nav.Push(NewEntries(m.env, "Log "+unit, syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1, Unit: unit}))
			}
		case run.Outcome:
			if r.Approved && r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			}
			return m, m.Init()
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up", "k":
			m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
		case "down", "j":
			m.cursor = (m.cursor + 1) % len(m.items)
		case "enter":
			return m, m.open(m.items[m.cursor])
		case "p":
			if m.loaded && !m.health.Persistent {
				return m, nav.Push(runflow.Confirm(PersistentPlan(), m.env.Deps))
			}
		case "v":
			if m.health.Usage > 1<<30 {
				return m, nav.Push(runflow.Confirm(VacuumPlan(m.health.Usage), m.env.Deps))
			}
		}
	}
	return m, nil
}

func (m *Model) open(p preset) tea.Cmd {
	switch p.id {
	case "files":
		return nav.Push(NewFiles(m.env, "/var/log"))
	case "grep":
		return nav.Push(ask.New(ask.Form{
			ID: "grep", Title: "Cari di log",
			Questions: []ask.Question{{
				ID: "kata", Prompt: "Kata atau pola yang dicari?", Kind: ask.Text, Placeholder: "Failed password",
				Help:     "Pencarian memakai regex (tidak peka huruf besar/kecil bila semua huruf kecil). Contoh: \"out of memory\", \"nginx|apache\".",
				Validate: func(s string) error { return nonEmpty(s, "kata kunci") },
			}},
		}))
	case "unit":
		return nav.Push(ask.New(ask.Form{
			ID: "unit", Title: "Log satu service",
			Questions: []ask.Question{{
				ID: "unit", Prompt: "Nama unit systemd?", Kind: ask.Text, Placeholder: "nginx.service",
				Help:     "Nama service seperti nginx, ssh, docker, atau cron. Akhiran .service boleh tidak ditulis. Daftar lengkap ada di modul Service.",
				Validate: func(s string) error { return nonEmpty(s, "nama unit") },
			}},
		}))
	case "prev":
		if m.loaded && len(m.health.Boots) < 2 {
			m.message = "Journal hanya menyimpan boot ini, jadi log sebelum restart tidak tersedia. Tekan p supaya ke depannya log tersimpan permanen."
			return nil
		}
	}
	return nav.Push(NewEntries(m.env, p.label, p.query))
}

func nonEmpty(s, what string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New(what + " tidak boleh kosong")
	}
	return nil
}

func normalizeUnit(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, ".") {
		s += ".service"
	}
	return s
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "$USER"
}

// PersistentPlan membuat journal tersimpan di disk supaya bertahan setelah reboot.
func PersistentPlan() run.Plan {
	return run.Plan{
		Title: "Simpan journal secara permanen",
		Steps: []run.Command{
			{Title: "Buat folder journal", Argv: []string{"mkdir", "-p", JournalDir}, NeedsRoot: true,
				Explain: []run.Line{{Token: JournalDir, Meaning: "bila folder ini ada, journald menyimpan log ke disk (Storage=auto)"}}},
			{Title: "Atur izin folder", Argv: []string{"systemd-tmpfiles", "--create", "--prefix", JournalDir}, NeedsRoot: true,
				Explain: []run.Line{{Token: "systemd-tmpfiles --create", Meaning: "beri pemilik & izin yang benar (grup systemd-journal dan adm bisa membaca)"}}},
			{Title: "Muat ulang journald", Argv: []string{"systemctl", "restart", "systemd-journald"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "restart systemd-journald", Meaning: "journald mulai menulis ke folder baru; tidak ada log yang hilang"}},
				Effect:  "Mulai sekarang log bertahan setelah reboot, jadi penyebab restart bisa diselidiki.",
				Risk:    risk.Caution},
		},
	}
}

// VacuumPlan merampingkan journal menjadi 500 MiB.
func VacuumPlan(usage int64) run.Plan {
	return run.Single(run.Command{
		Title: "Rampingkan journal", Argv: []string{"journalctl", "--vacuum-size=500M"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "--vacuum-size=500M", Meaning: "hapus arsip journal tertua sampai total tinggal 500 MiB"}},
		Effect:  fmt.Sprintf("Journal sekarang %s. Log lama hilang, log terbaru tetap ada.", shared.Bytes(usage)),
		Safer:   "atur batas permanen: SystemMaxUse=500M di /etc/systemd/journald.conf",
		Risk:    risk.Caution,
	})
}

// AddToAdmPlan menambahkan user ke grup adm supaya bisa membaca log sistem tanpa sudo.
func AddToAdmPlan(username string) run.Plan {
	return run.Single(run.Command{
		Title: "Izinkan " + username + " membaca log sistem", Argv: []string{"usermod", "-aG", "adm", username}, NeedsRoot: true,
		Explain: []run.Line{
			{Token: "-aG adm", Meaning: "tambahkan (append) ke grup adm tanpa mengeluarkan dari grup lain"},
			{Token: username, Meaning: "user yang sedang login"},
		},
		Effect: "Berlaku setelah logout lalu login lagi (atau jalankan: newgrp adm).",
		Safer:  "tanpa opsi -a, user dikeluarkan dari semua grup lain — jangan lupakan -a",
		Risk:   risk.Caution,
	})
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	var lines []string
	lines = append(lines, "")
	switch {
	case !m.loaded:
		lines = append(lines, " "+t.Subtle.Render("Memeriksa journal…"))
	default:
		h := m.health
		status := t.Success.Render("✓ tersimpan permanen di " + JournalDir)
		if !h.Persistent {
			status = t.Warning.Render("⚠ hanya di memori: log hilang saat reboot") + t.Subtle.Render(" — tekan p untuk menyimpan permanen")
		}
		lines = append(lines, " "+t.Title.Render("Journal")+"   "+status)
		usage := "ukuran " + shared.Bytes(h.Usage)
		if h.Usage > 1<<30 {
			usage = t.Warning.Render(usage + " — tekan v untuk merampingkan")
		} else {
			usage = t.Subtle.Render(usage)
		}
		boots := t.Subtle.Render(fmt.Sprintf(" · %d boot tercatat", len(h.Boots)))
		if len(h.Boots) > 0 {
			boots += t.Subtle.Render(" sejak " + h.Boots[0].First.Format("2006-01-02 15:04"))
		}
		lines = append(lines, "   "+usage+boots+"   "+t.Muted.Render("command setara: journalctl --disk-usage · journalctl --list-boots"))
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", ui.Section("Mau lihat log apa?"))
	for i, p := range m.items {
		label := p.label
		if i == m.cursor {
			lines = append(lines, " "+t.Selected.Render("❯ "+label))
			lines = append(lines, ui.Wrap(t.Subtle.Render(p.desc), width, "     "))
			if p.query.Boot != nil || p.query.Since != "" {
				lines = append(lines, "     "+t.Muted.Render("$ "+run.JoinShell(p.query.Args())))
			}
		} else {
			lines = append(lines, "   "+label)
		}
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}

// FilesModel mendaftar file di /var/log.
type FilesModel struct {
	env    shared.Env
	dir    string
	files  []syslogs.LogFile
	err    error
	table  ui.Table
	loaded bool
}

type filesMsg struct {
	owner *FilesModel
	files []syslogs.LogFile
	err   error
}

// NewFiles membuat daftar file log.
func NewFiles(env shared.Env, dir string) *FilesModel {
	return &FilesModel{env: env, dir: dir, table: ui.Table{Columns: []ui.Column{
		{Title: "File", Width: 20, Flex: 3},
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "Diubah", Width: 16},
		{Title: "Bisa dibaca", Width: 11},
	}}}
}

func (m *FilesModel) Title() string { return m.dir }
func (m *FilesModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "lihat isi"))}
}

func (m *FilesModel) Init() tea.Cmd {
	dir := m.dir
	return func() tea.Msg {
		files, err := syslogs.ListFiles(dir)
		return filesMsg{owner: m, files: files, err: err}
	}
}

func (m *FilesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case filesMsg:
		if msg.owner != m {
			return m, nil
		}
		m.loaded, m.files, m.err = true, msg.files, msg.err
		rows := make([][]string, len(m.files))
		for i, f := range m.files {
			readable := "ya"
			if !f.Readable {
				readable = "butuh izin"
			}
			rows[i] = []string{strings.TrimPrefix(f.Path, m.dir+"/"), shared.Bytes(f.Size), f.ModTime.Format("01-02 15:04"), readable}
		}
		m.table.SetRows(rows)
		m.table.CellStyle = func(r, c int) lipgloss.Style {
			if r < len(m.files) && (!m.files[r].Readable || m.files[r].Rotated) {
				return ui.Current.Muted
			}
			return lipgloss.NewStyle()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m, nil
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "enter" && m.table.Cursor < len(m.files) {
			f := m.files[m.table.Cursor]
			if !f.Readable && !m.env.IsRoot {
				return m, nav.Push(runflow.Confirm(AddToAdmPlan(currentUser()), m.env.Deps))
			}
			return m, nav.Push(newTail(m.env, f.Path))
		}
	}
	return m, nil
}

func (m *FilesModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca /var/log…")
	}
	if m.err != nil && len(m.files) == 0 {
		return ui.ErrorState(m.err, "", width)
	}
	top := "\n " + t.Muted.Render("command setara: ls -lt /var/log · File abu-abu: hasil rotasi atau butuh izin (enter untuk meminta izin grup adm)") + "\n"
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)-1), width, height)
}

// TailModel menampilkan baris terakhir sebuah file log.
type TailModel struct {
	env    shared.Env
	path   string
	viewer ui.Viewer
	err    error
	loaded bool
}

type tailMsg struct {
	owner *TailModel
	lines []string
	err   error
}

func newTail(env shared.Env, path string) *TailModel {
	return &TailModel{env: env, path: path, viewer: ui.Viewer{Follow: true}}
}

func (m *TailModel) Title() string { return m.path }
func (m *TailModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓ pgup/pgdn", "gulir")), key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "muat ulang"))}
}

func (m *TailModel) Init() tea.Cmd {
	path := m.path
	return func() tea.Msg {
		lines, err := syslogs.Tail(path, 1000)
		return tailMsg{owner: m, lines: lines, err: err}
	}
}

func (m *TailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tailMsg:
		if msg.owner == m {
			m.loaded, m.err = true, msg.err
			m.viewer.SetLines(msg.lines)
		}
	case nav.RefreshMsg:
		m.viewer.Follow = true
		return m, m.Init()
	case tea.KeyPressMsg:
		m.viewer.HandleKey(msg.String())
	}
	return m, nil
}

func (m *TailModel) View(width, height int) string {
	t := ui.Current
	cmd := "tail -n 1000 " + run.QuoteShell(m.path)
	if strings.HasSuffix(m.path, ".gz") {
		cmd = "zcat " + run.QuoteShell(m.path) + " | tail -n 1000"
	}
	top := "\n " + t.Muted.Render("command setara: ") + t.Code.Render(" "+cmd+" ") + "\n"
	switch {
	case !m.loaded:
		return top + "\n " + t.Subtle.Render("Membaca…")
	case m.err != nil:
		return top + ui.ErrorState(m.err, "sudo usermod -aG adm $USER lalu login ulang", width)
	case len(m.viewer.Lines) == 0:
		return top + ui.EmptyState("File kosong", "", "", width)
	}
	return top + "\n" + m.viewer.View(width, height-lipgloss.Height(top)-1, false)
}
