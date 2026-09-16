// Package ports adalah layar modul "Ports & Proses": port mana dipakai proses apa, dan cara
// menghentikannya dengan aman.
package ports

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// Data adalah hasil satu kali pembacaan.
type Data struct {
	Snap   sysports.Snapshot
	Procs  map[int]procs.Process
	Source string // "proc" atau "ss"
}

// Load membaca daftar port dari /proc, dengan cadangan `ss -tulpnH` bila /proc/net tidak tersedia.
func Load(env shared.Env) (Data, error) {
	d := Data{Procs: map[int]procs.Process{}, Source: "proc"}
	snap, err := sysports.ReadListeners(env.ProcRoot)
	if err != nil {
		out, _, ssErr := env.Runner.Capture(context.Background(), run.Command{Argv: []string{"ss", "-tulpnH"}})
		if ssErr != nil {
			return d, fmt.Errorf("%v; cadangan ss juga gagal: %v", err, ssErr)
		}
		snap = sysports.Snapshot{Listeners: sysports.ParseSS(out)}
		d.Source = "ss"
	}
	d.Snap = snap
	for _, l := range snap.Listeners {
		for _, pid := range l.PIDs {
			if _, ok := d.Procs[pid]; ok {
				continue
			}
			if p, err := procs.Read(env.ProcRoot, pid); err == nil {
				d.Procs[pid] = p
			}
		}
	}
	return d, nil
}

// Target membangun target aksi untuk listener, memilih proses induk bila socket dibagi.
func (d Data) Target(l sysports.Listener) Target {
	t := Target{Listener: l, Shared: len(l.PIDs)}
	main := MainPID(l.PIDs, func(pid int) int { return d.Procs[pid].PPID })
	if p, ok := d.Procs[main]; ok {
		t.Proc = &p
	}
	return t
}

type loadedMsg struct {
	owner *ListModel
	data  Data
	err   error
}

// ListModel adalah daftar port yang listening.
type ListModel struct {
	env     shared.Env
	load    func(shared.Env) (Data, error)
	data    Data
	loaded  bool
	err     error
	rows    []sysports.Listener // hasil filter, sejajar dengan table.Rows
	table   ui.Table
	filter  textinput.Model
	typing  bool
	proto   string // "", "tcp", "udp"
	width   int
	height  int
	keys    []key.Binding
	message string
}

// New membuat layar daftar port.
func New(env shared.Env) *ListModel { return newList(env, Load) }

func newList(env shared.Env, load func(shared.Env) (Data, error)) *ListModel {
	fi := textinput.New()
	fi.Prompt = "Filter: "
	fi.Placeholder = "port, nama proses, user, alamat…"
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return &ListModel{
		env:    env,
		load:   load,
		filter: fi,
		table: ui.Table{Columns: []ui.Column{
			{Title: "Proto", Width: 5},
			{Title: "Alamat", Width: 15, Flex: 2},
			{Title: "Port", Width: 5, Right: true},
			{Title: "PID", Width: 7, Right: true},
			{Title: "Proses", Width: 12, Flex: 2},
			{Title: "User", Width: 8, Flex: 1},
			{Title: "Dikelola", Width: 10, Flex: 2},
		}},
		keys: []key.Binding{
			b("↑↓", "pindah"), b("enter", "detail & aksi"), b("/", "filter"), b("t", "TCP/UDP"), b("r", "muat ulang"),
		},
	}
}

var _ nav.Screen = (*ListModel)(nil)

func (m *ListModel) Title() string       { return "Ports & Proses" }
func (m *ListModel) Keys() []key.Binding { return m.keys }
func (m *ListModel) Typing() bool        { return m.typing }
func (m *ListModel) HandlesBack() bool   { return m.typing || m.filter.Value() != "" }

func (m *ListModel) HelpText() string {
	return "Setiap aplikasi jaringan \"mendengarkan\" (listen) di sebuah port. Daftar ini menunjukkan port mana yang sedang dipakai dan oleh proses apa. " +
		"Alamat 0.0.0.0 atau :: berarti bisa diakses dari luar server (bila firewall mengizinkan); 127.0.0.1 atau ::1 berarti hanya dari server ini. " +
		"Command setara untuk dipelajari: sudo ss -tulpn"
}

func (m *ListModel) Init() tea.Cmd { return m.reload() }

func (m *ListModel) reload() tea.Cmd {
	env, load := m.env, m.load
	return func() tea.Msg {
		d, err := load(env)
		return loadedMsg{owner: m, data: d, err: err}
	}
}

func (m *ListModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.SizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.SetWidth(max(msg.Width-20, 20))
	case loadedMsg:
		if msg.owner == m {
			m.loaded, m.data, m.err = true, msg.data, msg.err
			m.applyFilter()
		}
	case nav.RefreshMsg:
		return m, m.reload()
	case nav.ResumedMsg:
		if s, ok := msg.Result.(string); ok {
			m.message = s
		}
		return m, m.reload()
	case tea.PasteMsg:
		if m.typing {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *ListModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
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
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return cmd
		}
		m.applyFilter()
		return nil
	}
	if m.table.HandleKey(k) {
		return nil
	}
	switch k {
	case "/":
		m.typing = true
		return m.filter.Focus()
	case "esc":
		m.filter.SetValue("")
		m.applyFilter()
	case "t":
		m.proto = map[string]string{"": "tcp", "tcp": "udp", "udp": ""}[m.proto]
		m.applyFilter()
	case "enter", "right", "l":
		if m.table.Cursor < len(m.rows) {
			l := m.rows[m.table.Cursor]
			m.message = ""
			return nav.Push(newDetail(m.env, m.data, l))
		}
	}
	return nil
}

func (m *ListModel) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.rows = m.rows[:0]
	var cells [][]string
	for _, l := range m.data.Snap.Listeners {
		if m.proto != "" && string(l.Proto) != m.proto {
			continue
		}
		row := m.row(l)
		if q != "" && !strings.Contains(strings.ToLower(strings.Join(row, " ")), q) {
			continue
		}
		m.rows = append(m.rows, l)
		cells = append(cells, row)
	}
	m.table.SetRows(cells)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if r >= len(m.rows) {
			return lipgloss.NewStyle()
		}
		l := m.rows[r]
		switch c {
		case 1:
			switch l.Scope() {
			case sysports.ScopeAll:
				return t.Warning
			case sysports.ScopeLoopback:
				return t.Success
			}
		case 3, 4, 5:
			if len(l.PIDs) == 0 && l.Process == "" {
				return t.Muted
			}
		case 6:
			return t.Subtle
		}
		return lipgloss.NewStyle()
	}
}

// row mengubah listener menjadi sel tabel.
func (m *ListModel) row(l sysports.Listener) []string {
	proto := string(l.Proto)
	if l.IPv6 {
		proto += "6"
	}
	pid, name, user, managed := "?", "?", procs.Username(l.UID), ""
	if l.Process != "" {
		name = l.Process
	}
	if len(l.PIDs) > 0 {
		t := m.data.Target(l)
		if t.Proc != nil {
			pid = strconv.Itoa(t.Proc.PID)
			name, user = t.Proc.Name, t.Proc.User
			switch {
			case t.Proc.Cgroup.Container != "":
				managed = "docker " + shortID(t.Proc.Cgroup.Container)
			case t.Proc.Cgroup.Service():
				// Scope (sesi login, terminal) tidak ditampilkan di tabel supaya ringkas; ada di detail.
				managed = t.Proc.Cgroup.Unit
			}
		} else {
			pid = strconv.Itoa(l.PIDs[0])
		}
		if len(l.PIDs) > 1 {
			pid += fmt.Sprintf("+%d", len(l.PIDs)-1)
		}
	}
	if m.data.Source == "ss" {
		user = ""
	}
	return []string{proto, addrLabel(l), strconv.Itoa(int(l.Local.Port())), pid, name, user, managed}
}

func addrLabel(l sysports.Listener) string {
	return l.Local.Addr().WithZone("").String()
}

// ScopeText menjelaskan dari mana sebuah alamat bisa diakses.
func ScopeText(l sysports.Listener) string {
	switch l.Scope() {
	case sysports.ScopeAll:
		if l.IPv6 {
			return "semua alamat IPv6 (dan biasanya IPv4 juga) — bisa diakses dari luar server bila firewall mengizinkan"
		}
		return "semua alamat IPv4 — bisa diakses dari luar server bila firewall mengizinkan"
	case sysports.ScopeLoopback:
		return "hanya dari server ini sendiri (localhost) — tidak bisa diakses dari luar"
	}
	return "hanya lewat alamat " + addrLabel(l)
}

func (m *ListModel) View(width, height int) string {
	t := ui.Current
	var top []string
	top = append(top, "")
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca port yang terbuka…")
	case m.err != nil:
		return ui.ErrorState(m.err, "pastikan /proc terpasang, atau install iproute2 untuk command ss", width)
	}

	counts := fmt.Sprintf("%d port sedang menunggu koneksi", len(m.data.Snap.Listeners))
	if m.proto != "" {
		counts += " · menampilkan " + strings.ToUpper(m.proto) + " saja"
	}
	top = append(top, " "+t.Title.Render(counts)+"   "+t.Muted.Render("command setara: sudo ss -tulpn"))
	if m.data.Source == "ss" {
		top = append(top, ui.Wrap(t.Warning.Render("⚠ /proc/net tidak tersedia, data diambil dari ss (tanpa root, pemilik port mungkin kosong)."), width, " "))
	}
	if m.data.Snap.Denied > 0 && !m.env.IsRoot && hasUnknown(m.data.Snap.Listeners) {
		top = append(top, ui.Wrap(t.Warning.Render("⚠ Sebagian pemilik port tidak terlihat karena milik user lain (PID ?). Jalankan sudo ubt untuk melihat semuanya."), width, " "))
	}
	if m.message != "" {
		top = append(top, ui.Wrap(t.Success.Render(m.message), width, " "))
	}
	if m.typing || m.filter.Value() != "" {
		top = append(top, " "+m.filter.View())
	}
	top = append(top, "")

	var bottom []string
	if m.table.Cursor < len(m.rows) {
		l := m.rows[m.table.Cursor]
		bottom = append(bottom, "", ui.Wrap(t.Subtle.Render(fmt.Sprintf("%s:%d → %s", addrLabel(l), l.Local.Port(), ScopeText(l))), width, " "))
	}
	topStr := strings.Join(top, "\n")
	bottomStr := strings.Join(bottom, "\n")
	tableHeight := height - lipgloss.Height(topStr) - lipgloss.Height(bottomStr)

	var body string
	if len(m.rows) == 0 {
		body = ui.EmptyState("Tidak ada port yang cocok", "Tidak ada port listening yang cocok dengan filter.", "tekan esc untuk menghapus filter", width)
		if len(m.data.Snap.Listeners) == 0 {
			body = ui.EmptyState("Tidak ada port yang terbuka", "Belum ada aplikasi yang menunggu koneksi di server ini.", "", width)
		}
	} else {
		body = m.table.View(width, max(tableHeight, 3))
	}
	return ui.FitHeight(topStr+"\n"+body, width, height-lipgloss.Height(bottomStr)) + bottomStr
}

func hasUnknown(ls []sysports.Listener) bool {
	for _, l := range ls {
		if len(l.PIDs) == 0 {
			return true
		}
	}
	return false
}
