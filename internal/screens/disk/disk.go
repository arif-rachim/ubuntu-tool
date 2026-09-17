// Package disk adalah layar modul Disk & Storage: "disk penuh, apa yang makan tempat?".
package disk

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdisk "github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	sysres "github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Data adalah hasil pembacaan dashboard.
type Data struct {
	Mounts   []sysdisk.Mount
	Swaps    []sysdisk.Swap
	SwapErr  error
	Devices  []sysdisk.BlockDevice
	Deleted  sysdisk.DeletedOpen
	RAMBytes int64
}

// Load membaca semua data dashboard.
func Load(env shared.Env) (Data, error) {
	var d Data
	var err error
	if d.Mounts, err = sysdisk.ReadMounts(env.ProcRoot, false); err != nil {
		return d, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _, serr := env.Runner.Capture(ctx, run.Command{Argv: sysdisk.SwaponArgs})
	d.Swaps, d.SwapErr = sysdisk.ParseSwapon(out), serr
	if out, _, err := env.Runner.Capture(ctx, run.Command{Argv: sysdisk.LsblkArgs}); err == nil {
		d.Devices, _ = sysdisk.ParseLsblk([]byte(out))
	}
	d.Deleted, _ = sysdisk.FindDeletedOpen(env.ProcRoot)
	if mem, err := os.ReadFile(env.ProcRoot + "/meminfo"); err == nil {
		d.RAMBytes = sysres.ParseMeminfo(string(mem)).TotalKB * 1024
	}
	return d, nil
}

type (
	loadedMsg struct {
		owner *Model
		data  Data
		err   error
	}
	candidatesMsg struct {
		owner *Model
		cands []sysdisk.Candidate
	}
)

// Model adalah dashboard Disk & Storage.
type Model struct {
	env     shared.Env
	load    func(shared.Env) (Data, error)
	sources sysdisk.CleanupSources
	data    Data
	loaded  bool
	err     error
	table   ui.Table
	busy    string
	message string
	cands   []sysdisk.Candidate
}

// New membuat dashboard Disk & Storage.
func New(env shared.Env) *Model { return newModel(env, Load, sysdisk.DefaultCleanupSources) }

func newModel(env shared.Env, load func(shared.Env) (Data, error), src sysdisk.CleanupSources) *Model {
	return &Model{
		env: env, load: load, sources: src,
		table: ui.Table{Columns: []ui.Column{
			{Title: "Dipasang di", Width: 10, Flex: 3},
			{Title: "Perangkat", Width: 9, Flex: 2},
			{Title: "Tipe", Width: 5},
			{Title: "Ukuran", Width: 9, Right: true},
			{Title: "Sisa", Width: 9, Right: true},
			{Title: "Pakai", Width: 17},
			{Title: "Inode", Width: 5, Right: true},
		}},
	}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Disk & Storage" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("↑↓", "pilih"), b("a", "apa yang makan tempat"), b("c", "bersih-bersih"), b("h", "file terhapus"), b("s", "swap"), b("r", "muat ulang")}
}

func (m *Model) HelpText() string {
	return "Ukuran dihitung seperti df: \"Sisa\" adalah ruang yang bisa dipakai user biasa (sebagian kecil disimpan khusus untuk root). " +
		"Inode adalah jatah jumlah file; bila inode habis, file baru tidak bisa dibuat walau ruang masih ada. " +
		"Command setara: df -h, df -i, lsblk, swapon --show, sudo du -xh -d1 / | sort -h"
}

func (m *Model) Init() tea.Cmd { return m.reload() }

func (m *Model) reload() tea.Cmd {
	env, load := m.env, m.load
	return func() tea.Msg {
		d, err := load(env)
		return loadedMsg{owner: m, data: d, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case loadedMsg:
		if msg.owner == m {
			m.loaded, m.data, m.err = true, msg.data, msg.err
			m.fillTable()
		}
	case candidatesMsg:
		if msg.owner == m {
			m.busy, m.cands = "", msg.cands
			return m, nav.Push(ask.New(cleanupForm(msg.cands)))
		}
	case nav.RefreshMsg:
		return m, m.reload()
	case nav.ResumedMsg:
		return m, m.onResumed(msg.Result)
	case tea.KeyPressMsg:
		k := msg.String()
		if m.table.HandleKey(k) || m.busy != "" {
			return m, nil
		}
		m.message = ""
		switch k {
		case "a", "enter":
			if mt, ok := m.selected(); ok {
				return m, nav.Push(newExplorer(m.env, mt.Target))
			}
		case "h":
			return m, nav.Push(newDeleted(m.env, m.data.Deleted))
		case "c":
			m.busy = "Menghitung ruang yang bisa dibersihkan…"
			r, src := m.env.Runner, m.sources
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return candidatesMsg{owner: m, cands: sysdisk.FindCandidates(ctx, r, src)}
			}
		case "s":
			return m, nav.Push(ask.New(swapForm(m.data)))
		}
	}
	return m, nil
}

func (m *Model) selected() (sysdisk.Mount, bool) {
	if m.table.Cursor < len(m.data.Mounts) {
		return m.data.Mounts[m.table.Cursor], true
	}
	return sysdisk.Mount{}, false
}

func (m *Model) onResumed(result any) tea.Cmd {
	switch r := result.(type) {
	case ask.Result:
		if r.Cancelled {
			return nil
		}
		var plan run.Plan
		switch r.ID {
		case "cleanup":
			plan = cleanupPlan(m.cands, r.Answers["pilih"].Values)
		case "swap":
			size, _ := strconv.Atoi(r.Answers["size"].Value())
			plan = sysdisk.SwapfilePlan(r.Answers["path"].Value(), size, m.env.Now())
		}
		if len(plan.Steps) == 0 {
			return nil
		}
		return nav.Push(runflow.Confirm(plan, m.env.Deps))
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			} else {
				m.message = "✗ " + r.Plan.Title + " tidak selesai. Lihat output langkah yang gagal."
			}
		}
	}
	return m.reload()
}

func usageStyle(p int) lipgloss.Style {
	t := ui.Current
	switch {
	case p >= 90:
		return t.Danger
	case p >= 80:
		return t.Warning
	}
	return t.Success
}

func (m *Model) fillTable() {
	rows := make([][]string, len(m.data.Mounts))
	for i, mt := range m.data.Mounts {
		if mt.Err != nil {
			rows[i] = []string{mt.Target, mt.Source, mt.FSType, "?", "?", "tidak terbaca", "?"}
			continue
		}
		p := mt.UsePercent()
		rows[i] = []string{
			mt.Target, strings.TrimPrefix(mt.Source, "/dev/"), mt.FSType,
			shared.Bytes(mt.Size), shared.Bytes(mt.Avail),
			sysres.Bar(float64(p), 10) + fmt.Sprintf(" %3d%%", p),
			strconv.Itoa(mt.InodePercent()) + "%",
		}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		if r >= len(m.data.Mounts) {
			return lipgloss.NewStyle()
		}
		mt := m.data.Mounts[r]
		switch c {
		case 5:
			return usageStyle(mt.UsePercent())
		case 6:
			return usageStyle(mt.InodePercent())
		case 1, 2:
			return ui.Current.Subtle
		}
		return lipgloss.NewStyle()
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca filesystem…")
	case m.err != nil:
		return ui.ErrorState(m.err, "modul Disk membutuhkan /proc/self/mountinfo", width)
	}
	var lines []string
	lines = append(lines, "", " "+t.Title.Render("Filesystem")+"   "+t.Muted.Render("command setara: df -h · df -i"))
	tableLines := strings.Split(m.table.View(width, len(m.data.Mounts)+1), "\n")
	lines = append(lines, tableLines...)

	var warn []string
	for _, mt := range m.data.Mounts {
		if mt.Err != nil {
			continue
		}
		if p := mt.UsePercent(); p >= 80 {
			warn = append(warn, usageStyle(p).Render(fmt.Sprintf("⚠ %s terpakai %d%%, sisa %s.", mt.Target, p, shared.Bytes(mt.Avail)))+
				t.Subtle.Render(" Tekan a untuk mencari yang makan tempat, atau c untuk bersih-bersih."))
		}
		if p := mt.InodePercent(); p >= 80 {
			warn = append(warn, usageStyle(p).Render(fmt.Sprintf("⚠ Inode %s terpakai %d%%.", mt.Target, p))+
				t.Subtle.Render(" Biasanya karena jutaan file kecil (cache, sesi PHP, mail queue); disk bisa \"penuh\" walau ruang masih ada."))
		}
	}
	if len(warn) > 0 {
		lines = append(lines, "")
		for _, w := range warn {
			lines = append(lines, ui.Wrap(w, width, " "))
		}
	}

	lines = append(lines, "", " "+t.Title.Render("Swap")+"   "+t.Muted.Render("command setara: swapon --show"))
	switch {
	case m.data.SwapErr != nil && len(m.data.Swaps) == 0:
		lines = append(lines, "   "+t.Muted.Render("tidak terbaca: "+m.data.SwapErr.Error()))
	case len(m.data.Swaps) == 0:
		lines = append(lines, "   "+t.Warning.Render("tidak ada swap aktif")+t.Subtle.Render(" — tekan s untuk membuat swapfile"))
	default:
		for _, s := range m.data.Swaps {
			lines = append(lines, fmt.Sprintf("   %s %s  %s, terpakai %s", s.Name, t.Subtle.Render("("+s.Type+")"), shared.Bytes(s.Size), shared.Bytes(s.Used)))
		}
	}

	if dev := describeDevices(m.data.Devices); len(dev) > 0 {
		lines = append(lines, "", " "+t.Title.Render("Perangkat")+"   "+t.Muted.Render("command setara: lsblk"))
		for _, d := range dev {
			lines = append(lines, ui.Wrap(d, width, "   "))
		}
	}

	lines = append(lines, "", " "+t.Title.Render("File terhapus tapi masih dibuka")+"   "+t.Muted.Render("command setara: sudo lsof +L1"))
	if del := m.data.Deleted; len(del.Files) > 0 {
		lines = append(lines, ui.Wrap(t.Warning.Render(fmt.Sprintf("⚠ %d file (%s) sudah dihapus tapi ruangnya belum kembali karena masih dibuka proses.", len(del.Files), shared.Bytes(del.Total())))+
			t.Subtle.Render(" Tekan h untuk melihat dan me-restart pemiliknya."), width, "   "))
	} else {
		lines = append(lines, "   "+t.Success.Render("✓")+t.Subtle.Render(" tidak ada"))
	}
	if del := m.data.Deleted; del.Denied > 0 && !m.env.IsRoot {
		lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf("(%d proses milik user lain tidak bisa diperiksa tanpa sudo)", del.Denied)))
	}

	if m.busy != "" {
		lines = append(lines, "", " "+t.Accent.Render(m.busy))
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}

// describeDevices meringkas disk fisik: model, ukuran, SSD/HDD, dan partisi yang belum dipasang.
func describeDevices(devs []sysdisk.BlockDevice) []string {
	t := ui.Current
	var out []string
	for _, d := range devs {
		if d.Type != "disk" {
			continue
		}
		kind := "SSD"
		if d.Rotational {
			kind = "HDD"
		}
		model := strings.TrimSpace(d.Model)
		if model == "" {
			model = "tanpa nama model"
		}
		line := fmt.Sprintf("%s  %s  %s %s", d.Name, shared.Bytes(d.Size), kind, t.Subtle.Render("("+model+")"))
		var unmounted []string
		for _, c := range append([]sysdisk.BlockDevice{d}, d.Children...) {
			if c.Unmounted() {
				unmounted = append(unmounted, c.Name+" ("+c.FSType+")")
			}
		}
		if len(d.Children) == 0 && d.FSType == "" && len(d.Mountpoints) == 0 {
			line += t.Warning.Render("  belum dipartisi/diformat")
		}
		if len(unmounted) > 0 {
			line += t.Warning.Render("  belum dipasang: " + strings.Join(unmounted, ", "))
		}
		out = append(out, line)
	}
	return out
}
