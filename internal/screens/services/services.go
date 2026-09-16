// Package services adalah layar modul Service (systemd): start, stop, restart, enable service, dan lihat lognya.
package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	screenlogs "github.com/arif-rachim/ubuntu-tool/internal/screens/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/systemd"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Mode tampilan daftar.
const (
	viewAll = iota
	viewActive
	viewFailed
)

var viewNames = []string{"semua", "yang berjalan", "yang gagal"}

// ListModel adalah daftar service.
type ListModel struct {
	env    shared.Env
	user   bool
	units  []systemd.Unit
	rows   []systemd.Unit
	loaded bool
	err    error
	table  ui.Table
	filter textinput.Model
	typing bool
	mode   int
}

type listMsg struct {
	owner *ListModel
	units []systemd.Unit
	err   error
}

// New membuat daftar service sistem.
func New(env shared.Env) *ListModel {
	fi := textinput.New()
	fi.Prompt = "Filter: "
	fi.Placeholder = "nama atau deskripsi service…"
	return &ListModel{env: env, filter: fi, table: ui.Table{Columns: []ui.Column{
		{Title: "Service", Width: 18, Flex: 2},
		{Title: "Status", Width: 18},
		{Title: "Saat boot", Width: 9},
		{Title: "Deskripsi", Width: 20, Flex: 3},
	}}}
}

var _ nav.Screen = (*ListModel)(nil)

func (m *ListModel) Title() string {
	if m.user {
		return "Service (milik user)"
	}
	return "Service (systemd)"
}
func (m *ListModel) Typing() bool      { return m.typing }
func (m *ListModel) HandlesBack() bool { return m.typing || m.filter.Value() != "" }

func (m *ListModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	user := "unit milik user"
	if m.user {
		user = "unit sistem"
	}
	return []key.Binding{b("↑↓", "pilih"), b("enter", "detail & aksi"), b("/", "filter"), b("v", "tampilkan "+viewNames[(m.mode+1)%3]), b("u", user), b("r", "muat ulang")}
}

func (m *ListModel) HelpText() string {
	return "Service adalah program yang dijalankan systemd di latar belakang (web server, database, SSH). " +
		"\"Status\" menunjukkan kondisinya sekarang; \"Saat boot\" menunjukkan apakah ia menyala otomatis saat server dinyalakan (enabled). " +
		"static berarti tidak bisa di-enable sendiri karena dijalankan oleh unit lain. Command setara: systemctl list-units --type=service --all, systemctl --failed"
}

func (m *ListModel) Init() tea.Cmd {
	r, user := m.env.Runner, m.user
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		units, err := systemd.List(ctx, r, user)
		return listMsg{owner: m, units: units, err: err}
	}
}

func (m *ListModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		if msg.owner == m {
			m.loaded, m.units, m.err = true, msg.units, msg.err
			m.apply()
		}
	case nav.RefreshMsg, nav.ResumedMsg:
		return m, m.Init()
	case tea.PasteMsg:
		if m.typing {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.apply()
			return m, cmd
		}
	case tea.KeyPressMsg:
		k := msg.String()
		if m.typing {
			switch k {
			case "esc":
				m.typing = false
				m.filter.SetValue("")
				m.filter.Blur()
			case "enter":
				m.typing = false
				m.filter.Blur()
			case "up", "down":
				m.table.HandleKey(k)
				return m, nil
			default:
				var cmd tea.Cmd
				m.filter, cmd = m.filter.Update(msg)
				m.apply()
				return m, cmd
			}
			m.apply()
			return m, nil
		}
		if m.table.HandleKey(k) {
			return m, nil
		}
		switch k {
		case "/":
			m.typing = true
			return m, m.filter.Focus()
		case "esc":
			m.filter.SetValue("")
			m.apply()
		case "v":
			m.mode = (m.mode + 1) % 3
			m.apply()
		case "u":
			m.user = !m.user
			m.loaded = false
			return m, m.Init()
		case "enter", "right", "l":
			if m.table.Cursor < len(m.rows) {
				return m, nav.Push(NewDetail(m.env, m.rows[m.table.Cursor].Name, m.user))
			}
		}
	}
	return m, nil
}

func (m *ListModel) apply() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.rows = m.rows[:0]
	var cells [][]string
	for _, u := range m.units {
		switch {
		case m.mode == viewActive && u.Active != "active":
			continue
		case m.mode == viewFailed && !u.Failed():
			continue
		case q != "" && !strings.Contains(strings.ToLower(u.Name+" "+u.Description), q):
			continue
		}
		m.rows = append(m.rows, u)
		boot := u.FileState
		if boot == "" {
			boot = "—"
		}
		cells = append(cells, []string{strings.TrimSuffix(u.Name, ".service"), u.Active + " (" + u.Sub + ")", boot, u.Description})
	}
	m.table.SetRows(cells)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if r >= len(m.rows) {
			return lipgloss.NewStyle()
		}
		u := m.rows[r]
		switch c {
		case 1:
			return StatusStyle(u.Active, u.Sub)
		case 2:
			if u.Enabled() {
				return t.Success
			}
			return t.Muted
		case 3:
			return t.Subtle
		}
		return lipgloss.NewStyle()
	}
}

// StatusStyle memberi warna sesuai status unit.
func StatusStyle(active, sub string) lipgloss.Style {
	t := ui.Current
	switch {
	case active == "failed" || sub == "failed" || sub == "auto-restart":
		return t.Danger
	case active == "active" && sub == "running":
		return t.Success
	case active == "activating" || active == "deactivating" || active == "reloading":
		return t.Warning
	}
	return t.Muted
}

func (m *ListModel) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca daftar service…")
	case errors.Is(m.err, systemd.ErrNoSystemd):
		return ui.EmptyState("systemd tidak tersedia", m.err.Error()+". Service di sini dikelola dengan cara lain (mis. supervisor, s6, atau langsung sebagai proses).", "gunakan modul Ports & Proses untuk melihat program yang berjalan", width)
	case m.err != nil:
		return ui.ErrorState(m.err, "", width)
	}
	running, failed := 0, 0
	for _, u := range m.units {
		if u.Active == "active" && u.Sub == "running" {
			running++
		}
		if u.Failed() {
			failed++
		}
	}
	summary := fmt.Sprintf("%d service · %d berjalan", len(m.units), running)
	var top []string
	top = append(top, "", " "+t.Title.Render(summary)+failedText(failed)+"   "+t.Muted.Render("menampilkan "+viewNames[m.mode]+" · command setara: systemctl list-units --type=service --all"))
	if m.typing || m.filter.Value() != "" {
		top = append(top, " "+m.filter.View())
	}
	top = append(top, "")
	topStr := strings.Join(top, "\n")
	if len(m.rows) == 0 {
		return topStr + "\n" + ui.EmptyState("Tidak ada service yang cocok", "", "tekan v untuk mengganti tampilan atau esc untuk menghapus filter", width)
	}
	return ui.FitHeight(topStr+"\n"+m.table.View(width, height-lipgloss.Height(topStr)), width, height)
}

func failedText(n int) string {
	t := ui.Current
	if n == 0 {
		return t.Success.Render(" · tidak ada yang gagal")
	}
	return t.Danger.Render(fmt.Sprintf(" · %d gagal", n))
}

// DetailModel menampilkan satu service.
type DetailModel struct {
	env     shared.Env
	unit    string
	user    bool
	details systemd.Details
	loaded  bool
	err     error
	logs    []syslogs.Entry
	offset  int
	message string
}

type detailMsg struct {
	owner   *DetailModel
	details systemd.Details
	logs    []syslogs.Entry
	err     error
}

// NewDetail membuat layar detail service. Dipakai juga modul lain (Diagnosa).
func NewDetail(env shared.Env, unit string, user bool) *DetailModel {
	return &DetailModel{env: env, unit: unit, user: user}
}

var _ nav.Screen = (*DetailModel)(nil)

func (m *DetailModel) Title() string { return m.unit }

func (m *DetailModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("a", "aksi"), b("l", "log lengkap"), b("↑↓", "gulir")}
}

func (m *DetailModel) Init() tea.Cmd {
	r, unit, user := m.env.Runner, m.unit, m.user
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		d, err := systemd.Show(ctx, r, unit, user)
		res, _ := syslogs.Read(ctx, r, logQuery(unit, user, 12))
		return detailMsg{owner: m, details: d, logs: res.Entries, err: err}
	}
}

func logQuery(unit string, user bool, lines int) syslogs.Query {
	return syslogs.Query{Boot: syslogs.BootPtr(0), Priority: -1, Unit: unit, UserUnit: user, Lines: lines}
}

func (m *DetailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detailMsg:
		if msg.owner == m {
			m.loaded, m.details, m.logs, m.err = true, msg.details, msg.logs, msg.err
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			p := systemd.Plan(r.Answers["aksi"].Value(), m.unit, m.user)
			if len(p.Steps) > 0 {
				return m, nav.Push(runflow.Confirm(p, m.env.Deps))
			}
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " berhasil."
				} else {
					m.message = "✗ " + r.Plan.Title + " gagal. Lihat log di bawah untuk penyebabnya."
				}
			}
			return m, m.Init()
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "a", "enter":
			if m.loaded && m.err == nil {
				return m, nav.Push(ask.New(ActionForm(m.unit, m.details)))
			}
		case "l":
			return m, nav.Push(screenlogs.NewEntries(m.env, "Log "+m.unit, logQuery(m.unit, m.user, 500)))
		case "up", "k":
			m.offset = max(m.offset-1, 0)
		case "down", "j":
			m.offset++
		}
	}
	return m, nil
}

// ActionForm menawarkan aksi yang masuk akal untuk kondisi unit saat ini.
func ActionForm(unit string, d systemd.Details) ask.Form {
	active := d.Get("ActiveState")
	file := d.Get("UnitFileState")
	failed := active == "failed" || d.CrashLooping()
	var opts []ask.Option
	add := func(value, label, desc string, rec bool, lvl risk.Level) {
		opts = append(opts, ask.Option{Value: value, Label: label, Description: desc, Recommended: rec, Risk: lvl})
	}
	crit := systemd.Critical(unit)
	stopRisk := risk.Caution
	if crit != "" {
		stopRisk = risk.Dangerous
	}
	switch {
	case active == "active" || active == "activating" || active == "reloading":
		if d.Get("CanReload") == "yes" {
			add(systemd.ActReload, "Muat ulang konfigurasi (reload)", "Membaca ulang konfigurasi tanpa memutus koneksi. Pilihan terbaik setelah mengubah file konfigurasi.", true, risk.Safe)
		}
		add(systemd.ActRestart, "Restart", "Hentikan lalu jalankan lagi. Koneksi yang sedang berjalan terputus sebentar.", d.Get("CanReload") != "yes", risk.Caution)
		desc := "Hentikan service sampai dijalankan lagi."
		if crit != "" {
			desc += " Risiko: " + crit + "."
		}
		add(systemd.ActStop, "Hentikan (stop)", desc, false, stopRisk)
	default:
		add(systemd.ActStart, "Jalankan (start)", "Jalankan service sekarang.", !failed, risk.Caution)
		if failed {
			add(systemd.ActRestart, "Coba jalankan ulang (restart)", "Service sebelumnya gagal. Lihat log dulu supaya tahu penyebabnya sebelum mencoba lagi.", true, risk.Caution)
		}
	}
	switch file {
	case "enabled", "enabled-runtime":
		desc := "Service tidak menyala otomatis setelah reboot. Yang sedang berjalan tidak dihentikan."
		if crit != "" {
			desc += " Risiko setelah reboot: " + crit + "."
		}
		add(systemd.ActDisable, "Jangan nyalakan saat boot (disable)", desc, false, stopRisk)
	case "disabled":
		add(systemd.ActEnable, "Nyalakan otomatis saat boot (enable)", "Service menyala setiap server dinyalakan.", false, risk.Safe)
		if active != "active" {
			add(systemd.ActEnableNow, "Jalankan sekarang dan saat boot (enable --now)", "Gabungan start + enable.", false, risk.Caution)
		}
	}
	if failed || d.Int("NRestarts") > 0 {
		add(systemd.ActResetFailed, "Bersihkan status gagal (reset-failed)", "Menghapus tanda gagal dan hitungan restart setelah masalahnya diperbaiki.", false, risk.Safe)
	}
	return ask.Form{
		ID:    "service-action",
		Title: "Aksi " + unit,
		Questions: []ask.Question{{
			ID: "aksi", Prompt: "Apa yang ingin dilakukan dengan " + unit + "?", Kind: ask.Single, Options: opts,
			Help: "start/stop mengubah kondisi sekarang; enable/disable mengubah perilaku saat boot. Keduanya terpisah: service bisa berjalan tapi tidak enabled, atau enabled tapi sedang berhenti.",
		}},
	}
}

func (m *DetailModel) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca "+m.unit+"…")
	case errors.Is(m.err, systemd.ErrNoSystemd):
		return ui.EmptyState("systemd tidak tersedia", m.err.Error(), "", width)
	case m.err != nil:
		return ui.ErrorState(m.err, "", width)
	}
	d := m.details
	var lines []string
	lines = append(lines, "")
	if d.Get("LoadState") == "not-found" {
		lines = append(lines, ui.Wrap(t.Warning.Render("⚠ Unit "+m.unit+" tidak ditemukan. Mungkin programnya belum terinstall atau namanya salah."), width, " "))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}
	active, sub := d.Get("ActiveState"), d.Get("SubState")
	status := StatusStyle(active, sub).Render(active + " (" + sub + ")")
	if since := d.Since("ActiveEnterTimestamp"); !since.IsZero() && active == "active" {
		status += t.Subtle.Render(" sejak " + since.Format("2006-01-02 15:04") + " (" + shared.Ago(since, m.env.Now()) + ")")
	}
	boot := d.Get("UnitFileState")
	bootHint := map[string]string{
		"enabled": "menyala otomatis saat boot", "disabled": "tidak menyala otomatis saat boot",
		"static": "tidak bisa di-enable sendiri; dijalankan oleh unit lain", "masked": "diblokir total: tidak bisa dijalankan sama sekali",
	}[boot]

	if d.CrashLooping() || active == "failed" {
		lines = append(lines, ui.Wrap(t.Danger.Render(fmt.Sprintf("✗ Service ini bermasalah: hasil terakhir %q, sudah di-restart otomatis %d kali.", d.Get("Result"), d.Int("NRestarts")))+
			t.Subtle.Render(" Baca log di bawah (biasanya baris terakhir menyebut penyebabnya), perbaiki, lalu restart."), width, " "), "")
	}
	pairs := []ui.Pair{
		{Key: "Deskripsi", Value: d.Get("Description")},
		{Key: "Status", Value: status},
		{Key: "Saat boot", Value: boot, Hint: bootHint},
		{Key: "PID utama", Value: zeroDash(d.Get("MainPID"))},
		{Key: "Perintah", Value: d.Command("ExecStart")},
		{Key: "User", Value: orDefault(d.Get("User"), "root")},
		{Key: "Folder kerja", Value: d.Get("WorkingDirectory")},
		{Key: "Restart otomatis", Value: d.Get("Restart"), Hint: restartHint(d.Get("Restart"))},
	}
	if mem := d.Int("MemoryCurrent"); mem > 0 {
		pairs = append(pairs, ui.Pair{Key: "Memori", Value: shared.Bytes(mem)})
	}
	if env := d.Get("Environment"); env != "" {
		pairs = append(pairs, ui.Pair{Key: "Environment", Value: env})
	}
	pairs = append(pairs, ui.Pair{Key: "File unit", Value: d.Get("FragmentPath"), Hint: dropins(d.Get("DropInPaths"))})
	if trig := d.Get("TriggeredBy"); trig != "" {
		pairs = append(pairs, ui.Pair{Key: "Dipicu oleh", Value: trig, Hint: "dijalankan otomatis oleh unit ini (socket/timer/path)"})
	}
	lines = append(lines, ui.Detail(pairs, width))
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}

	lines = append(lines, "", ui.Section("Log terbaru")+"   "+t.Muted.Render("tekan l untuk log lengkap"))
	if len(m.logs) == 0 {
		lines = append(lines, "   "+t.Muted.Render("(tidak ada log sejak boot ini, atau butuh grup adm untuk membacanya)"))
	}
	for _, e := range m.logs {
		lines = append(lines, "   "+screenlogs.FormatEntry(e))
	}

	scope := ""
	if m.user {
		scope = "--user "
	}
	lines = append(lines, "", ui.Section("Cara cek sendiri"))
	for _, c := range []string{"systemctl " + scope + "status " + m.unit, "journalctl " + map[bool]string{true: "--user-unit", false: "-u"}[m.user] + " " + m.unit + " -b", "systemctl " + scope + "cat " + m.unit} {
		lines = append(lines, "   "+t.Muted.Render("$ ")+t.Code.Render(" "+c+" "))
	}
	lines = append(lines, "", " "+t.Accent.Render("Tekan a untuk start/stop/restart/enable."))

	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}

// zeroDash mengosongkan PID 0 (service tidak berjalan) supaya tampil sebagai "—".
func zeroDash(s string) string {
	if s == "0" {
		return ""
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func restartHint(r string) string {
	return map[string]string{
		"no":          "tidak di-restart bila mati",
		"on-failure":  "di-restart bila keluar dengan error",
		"always":      "selalu di-restart bila berhenti",
		"on-abnormal": "di-restart bila crash/timeout",
	}[r]
}

func dropins(s string) string {
	if s == "" {
		return ""
	}
	return "diubah oleh drop-in: " + s
}
