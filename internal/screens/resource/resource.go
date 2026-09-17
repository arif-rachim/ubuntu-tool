// Package resource adalah layar modul Resource: "server saya lambat, kenapa?".
package resource

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	sysres "github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// RefreshEvery adalah jeda pembaruan otomatis.
const RefreshEvery = 2 * time.Second

// Model adalah layar Resource.
type Model struct {
	env     shared.Env
	sampler *sysres.Sampler
	snap    sysres.Snapshot
	err     error
	paused  bool
	byMem   bool
	table   ui.Table
	top     []sysres.Proc
	oom     []sysres.OOMEvent
	oomErr  error
	oomDone bool
	target  *procs.Process
	message string
	moved   bool // user sudah memindahkan kursor: kursor mengikuti PID yang sama
	// Snapshot pertama belum punya CPU per proses; sampel kedua diambil cepat.
	quickSecond bool
}

type (
	sampledMsg struct {
		owner *Model
		snap  sysres.Snapshot
		err   error
	}
	tickMsg struct{ owner *Model }
	oomMsg  struct {
		owner  *Model
		events []sysres.OOMEvent
		err    error
	}
)

// New membuat layar Resource.
func New(env shared.Env) *Model {
	return &Model{
		env:     env,
		sampler: &sysres.Sampler{ProcRoot: env.ProcRoot},
		table: ui.Table{Columns: []ui.Column{
			{Title: "PID", Width: 7, Right: true},
			{Title: "User", Width: 8, Flex: 1},
			{Title: "CPU%", Width: 5, Right: true},
			{Title: "RAM", Width: 9, Right: true},
			{Title: "Perintah", Width: 20, Flex: 4},
		}},
	}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Resource" }

func (m *Model) HelpText() string {
	return "CPU%: persen satu core, jadi proses multi-thread bisa di atas 100%. RAM: memori fisik yang dipakai proses (RSS). " +
		"Load average: rata-rata tugas yang berjalan atau mengantre dalam 1, 5, dan 15 menit; bandingkan dengan jumlah core. " +
		"Tekanan (PSI): persen waktu ada tugas yang tertahan menunggu CPU, disk (IO), atau memori. " +
		"Command setara: top, free -h, uptime, ps aux --sort=-%cpu | head, cat /proc/pressure/*"
}

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	pause := "jeda"
	if m.paused {
		pause = "lanjutkan"
	}
	sort := "urut RAM"
	if m.byMem {
		sort = "urut CPU"
	}
	return []key.Binding{b("↑↓", "pindah"), b("enter", "aksi proses"), b("s", sort), b("p", pause)}
}

func (m *Model) Init() tea.Cmd {
	m.quickSecond = true
	return tea.Batch(m.sample(), m.loadOOM())
}

func (m *Model) sample() tea.Cmd {
	s, now := m.sampler, m.env.Now
	return func() tea.Msg {
		snap, err := s.Sample(now())
		return sampledMsg{owner: m, snap: snap, err: err}
	}
}

func (m *Model) loadOOM() tea.Cmd {
	r := m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ev, err := sysres.ReadOOM(ctx, r)
		return oomMsg{owner: m, events: ev, err: err}
	}
}

func (m *Model) schedule(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{owner: m} })
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case sampledMsg:
		if msg.owner != m {
			return m, nil
		}
		m.snap, m.err = msg.snap, msg.err
		m.refreshTable()
		if m.quickSecond {
			m.quickSecond = false
			return m, m.schedule(500 * time.Millisecond)
		}
		return m, m.schedule(RefreshEvery)
	case tickMsg:
		if msg.owner != m {
			return m, nil
		}
		if m.paused {
			return m, m.schedule(RefreshEvery)
		}
		return m, m.sample()
	case oomMsg:
		if msg.owner == m {
			m.oom, m.oomErr, m.oomDone = msg.events, msg.err, true
		}
	case nav.RefreshMsg:
		return m, m.loadOOM()
	case nav.ResumedMsg:
		return m, m.onResumed(msg.Result)
	case tea.KeyPressMsg:
		k := msg.String()
		if m.table.HandleKey(k) {
			m.moved = true
			return m, nil
		}
		switch k {
		case "s":
			m.byMem = !m.byMem
			m.refreshTable()
		case "p":
			m.paused = !m.paused
		case "enter":
			return m, m.openActions()
		}
	}
	return m, nil
}

func (m *Model) refreshTable() {
	selected := 0
	if m.table.Cursor < len(m.top) {
		selected = m.top[m.table.Cursor].PID
	}
	m.top = sysres.SortBy(m.snap.Procs, m.byMem, 50)
	rows := make([][]string, len(m.top))
	cursor := 0
	for i, p := range m.top {
		cpu := "…"
		if m.snap.HasDelta {
			cpu = strconv.FormatFloat(p.CPU, 'f', 1, 64)
		}
		cmd := p.Command
		if cmd == "" {
			cmd = p.Name
		}
		rows[i] = []string{strconv.Itoa(p.PID), p.User, cpu, shared.Bytes(p.RSSKB * 1024), cmd}
		if p.PID == selected {
			cursor = i
		}
	}
	m.table.SetRows(rows)
	if m.moved {
		m.table.Cursor = cursor // kursor mengikuti proses yang sama walau urutan berubah
	}
}

func (m *Model) openActions() tea.Cmd {
	if m.table.Cursor >= len(m.top) {
		return nil
	}
	sel := m.top[m.table.Cursor]
	p, err := procs.Read(m.env.ProcRoot, sel.PID)
	if err != nil {
		m.message = fmt.Sprintf("Proses %d sudah tidak ada.", sel.PID)
		return nil
	}
	m.target = &p
	hint := ""
	if m.snap.HasDelta {
		hint = fmt.Sprintf("Proses ini memakai CPU %.0f%% dan RAM %s.", sel.CPU, shared.Bytes(sel.RSSKB*1024))
	}
	subj := procact.Subject{Renice: true, TopHint: hint}
	return nav.Push(ask.New(ask.Form{
		ID:    "proc-action",
		Title: fmt.Sprintf("Aksi %s (PID %d)", p.Name, p.PID),
		Questions: []ask.Question{{
			ID:      "aksi",
			Prompt:  fmt.Sprintf("Apa yang ingin dilakukan dengan %s?", ansi.Truncate(p.CommandLine(), 80, "…")),
			Kind:    ask.Single,
			Options: procact.Options(p, subj, procact.CurrentContext(m.env.UID, m.env.IsRoot)),
		}},
	}))
}

func (m *Model) onResumed(result any) tea.Cmd {
	switch r := result.(type) {
	case ask.Result:
		if r.Cancelled || m.target == nil {
			return nil
		}
		plan := procact.Plan(r.Answers["aksi"].Value(), *m.target, procact.Subject{Renice: true}, procact.CurrentContext(m.env.UID, m.env.IsRoot))
		if len(plan.Steps) == 0 {
			return nil
		}
		return nav.Push(runflow.Confirm(plan, m.env.Deps))
	case run.Outcome:
		if r.Approved && r.OK() {
			m.message = "✓ " + r.Plan.Title + " berhasil."
		} else if r.Approved {
			m.message = "✗ " + r.Plan.Title + " tidak berhasil."
		}
		return m.sample()
	}
	return nil
}

func percent(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return 100 * float64(part) / float64(total)
}

func pct(v float64) string { return strings.Replace(fmt.Sprintf("%.1f%%", v), ".", ",", 1) }

func (m *Model) View(width, height int) string {
	t := ui.Current
	if m.err != nil {
		return ui.ErrorState(m.err, "modul Resource membutuhkan /proc", width)
	}
	s := m.snap
	if s.Time.IsZero() {
		return "\n " + t.Subtle.Render("Membaca pemakaian sumber daya…")
	}
	var lines []string
	lines = append(lines, "")
	barW := min(max(width/5, 10), 30)
	meterStyle := func(p float64) lipgloss.Style {
		switch {
		case p >= 90:
			return t.Danger
		case p >= 75:
			return t.Warning
		}
		return t.Success
	}
	meter := func(label string, p float64, detail string) string {
		return fmt.Sprintf(" %-5s %s %6s   %s", label, meterStyle(p).Render(sysres.Bar(p, barW)), pct(p), t.Subtle.Render(detail))
	}

	cpuDetail := fmt.Sprintf("%d core · load %.2f %.2f %.2f (1/5/15 menit)", s.Cores, s.Load.One, s.Load.Five, s.Load.Fifteen)
	if !s.HasDelta {
		cpuDetail = "mengukur… · " + cpuDetail
	}
	mem := s.Memory
	lines = append(lines,
		meter("CPU", s.CPUBusy, cpuDetail),
		meter("RAM", percent(mem.UsedKB(), mem.TotalKB), fmt.Sprintf("%s dipakai dari %s · %s tersedia · cache %s",
			shared.Bytes(mem.UsedKB()*1024), shared.Bytes(mem.TotalKB*1024), shared.Bytes(mem.AvailableKB*1024), shared.Bytes(max(mem.CacheKB(), 0)*1024))),
	)
	if mem.SwapTotalKB > 0 {
		lines = append(lines, meter("Swap", percent(mem.SwapUsedKB(), mem.SwapTotalKB), fmt.Sprintf("%s dari %s", shared.Bytes(mem.SwapUsedKB()*1024), shared.Bytes(mem.SwapTotalKB*1024))))
	} else {
		lines = append(lines, fmt.Sprintf(" %-5s %s", "Swap", t.Muted.Render("tidak ada swap")))
	}
	psi := t.Muted.Render("tekanan (PSI) tidak tersedia di kernel ini")
	if s.PSI.CPU.Available {
		psi = t.Subtle.Render(fmt.Sprintf("Tekanan 60 detik: CPU %s · IO %s · RAM %s", pct(s.PSI.CPU.Avg60), pct(s.PSI.IO.Avg60), pct(s.PSI.Memory.Avg60)))
	}
	up := t.Subtle.Render("menyala " + shared.Duration(s.Uptime))
	if m.paused {
		up += "  " + t.Warning.Render("⏸ dijeda")
	}
	lines = append(lines, " "+psi+"   "+up, "")

	lines = append(lines, ui.Section("Temuan"))
	for _, in := range sysres.Analyze(s) {
		icon := map[risk.Level]string{risk.Safe: t.Success.Render("✓"), risk.Caution: t.Warning.Render("⚠"), risk.Dangerous: t.Danger.Render("✗")}[in.Level]
		lines = append(lines, ui.Wrap(icon+" "+t.Title.Render(in.Title)+t.Subtle.Render(" — "+in.Detail), width, "   "))
		if in.Next != "" {
			lines = append(lines, ui.Wrap(t.Accent.Render("→ "+in.Next), width, "     "))
		}
	}
	switch {
	case !m.oomDone:
	case m.oomErr != nil:
		lines = append(lines, ui.Wrap(t.Muted.Render("Riwayat OOM tidak terbaca ("+m.oomErr.Error()+"). Tambahkan user ke grup adm: sudo usermod -aG adm $USER"), width, "   "))
	case len(m.oom) == 0:
		lines = append(lines, ui.Wrap(t.Success.Render("✓")+t.Subtle.Render(" Tidak ada proses yang dimatikan kernel karena RAM habis sejak boot."), width, "   "))
	default:
		last := m.oom[len(m.oom)-1]
		lines = append(lines, ui.Wrap(t.Danger.Render("✗")+" "+t.Title.Render(fmt.Sprintf("%d proses dimatikan OOM killer sejak boot", len(m.oom)))+
			t.Subtle.Render(fmt.Sprintf(" — terakhir %s (PID %d) %s. Lihat: journalctl -k -b --grep oom-kill", last.Process, last.PID, shared.Ago(last.Time, m.env.Now()))), width, "   "))
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	sortLabel := "CPU"
	if m.byMem {
		sortLabel = "RAM"
	}
	lines = append(lines, "", ui.Section("Proses teratas (urut "+sortLabel+")")+"   "+t.Muted.Render("command setara: ps aux --sort=-%cpu | head"))

	top := strings.Join(lines, "\n")
	tableH := height - lipgloss.Height(top)
	if tableH < 4 {
		// Terminal pendek: dahulukan tabel proses.
		return ui.FitHeight(m.table.View(width, height), width, height)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, tableH), width, height)
}
