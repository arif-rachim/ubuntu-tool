package logs

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Batas baris yang disimpan saat mengikuti log.
const followLimit = 3000

// EntriesModel menampilkan hasil satu query journal, dengan mode ikuti langsung.
type EntriesModel struct {
	env     shared.Env
	title   string
	query   syslogs.Query
	entries []syslogs.Entry
	shown   []int // indeks entries yang lolos filter
	loaded  bool
	limited bool
	err     error
	viewer  ui.Viewer
	filter  textinput.Model
	typing  bool
	detail  *syslogs.Entry

	stream    run.Stream
	following bool
}

type (
	entriesMsg struct {
		owner *EntriesModel
		res   syslogs.Result
		err   error
	}
	followStartedMsg struct {
		owner  *EntriesModel
		stream run.Stream
		err    error
	}
	followEventMsg struct {
		owner *EntriesModel
		ev    run.Event
	}
)

// NewEntries membuat layar hasil query journal. Dipakai juga modul Service untuk log per unit.
func NewEntries(env shared.Env, title string, q syslogs.Query) *EntriesModel {
	fi := textinput.New()
	fi.Prompt = "Filter: "
	return &EntriesModel{env: env, title: title, query: q, filter: fi, viewer: ui.Viewer{Follow: true}}
}

var _ nav.Screen = (*EntriesModel)(nil)

func (m *EntriesModel) Title() string { return m.title }
func (m *EntriesModel) Typing() bool  { return m.typing }
func (m *EntriesModel) Busy() bool    { return m.following }

// HandlesBack: esc menutup detail/filter/mode ikuti lebih dulu.
func (m *EntriesModel) HandlesBack() bool {
	return m.detail != nil || m.typing || m.filter.Value() != "" || m.following
}

func (m *EntriesModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	if m.detail != nil {
		return []key.Binding{b("esc", "tutup detail")}
	}
	follow := "ikuti langsung"
	if m.following {
		follow = "berhenti mengikuti"
		return []key.Binding{b("f", follow), b("↑↓", "gulir"), b("end", "ke terbaru"), b("esc", "berhenti")}
	}
	return []key.Binding{b("↑↓", "pilih"), b("enter", "detail"), b("/", "filter"), b("f", follow), b("r", "muat ulang")}
}

func (m *EntriesModel) Init() tea.Cmd { return m.load() }

func (m *EntriesModel) load() tea.Cmd {
	r, q := m.env.Runner, m.query
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := syslogs.Read(ctx, r, q)
		return entriesMsg{owner: m, res: res, err: err}
	}
}

func (m *EntriesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case entriesMsg:
		if msg.owner == m {
			m.loaded, m.err, m.limited = true, msg.err, msg.res.Limited
			m.entries = msg.res.Entries
			m.viewer.Follow = true
			m.rebuild()
		}
	case followStartedMsg:
		if msg.owner != m {
			return m, nil
		}
		if msg.err != nil {
			m.following, m.err = false, msg.err
			return m, nil
		}
		m.stream = msg.stream
		return m, m.waitFollow()
	case followEventMsg:
		if msg.owner != m {
			return m, nil
		}
		if msg.ev.Done {
			m.following, m.stream = false, nil
			return m, nil
		}
		if e, err := syslogs.ParseJSONLine([]byte(msg.ev.Line)); err == nil {
			m.entries = append(m.entries, e)
			if len(m.entries) > followLimit {
				m.entries = m.entries[len(m.entries)-followLimit:]
			}
			m.rebuild()
		}
		return m, m.waitFollow()
	case nav.RefreshMsg:
		if !m.following {
			return m, m.load()
		}
	case nav.ResumedMsg:
		return m, nil
	case tea.PasteMsg:
		if m.typing {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.rebuild()
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *EntriesModel) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if m.detail != nil {
		if k == "esc" || k == "enter" || k == "q" {
			m.detail = nil
		}
		return nil
	}
	if m.typing {
		switch k {
		case "esc":
			m.typing = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.rebuild()
		case "enter":
			m.typing = false
			m.filter.Blur()
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.rebuild()
			return cmd
		}
		return nil
	}
	if m.viewer.HandleKey(k) {
		return nil
	}
	switch k {
	case "f", "ctrl+c", "q":
		if m.following {
			m.stopFollow()
			return nil
		}
		if k == "f" {
			return m.startFollow()
		}
	case "esc":
		switch {
		case m.following:
			m.stopFollow()
		case m.filter.Value() != "":
			m.filter.SetValue("")
			m.rebuild()
		}
	case "/":
		m.typing = true
		return m.filter.Focus()
	case "enter":
		if m.viewer.Cursor >= 0 && m.viewer.Cursor < len(m.shown) {
			e := m.entries[m.shown[m.viewer.Cursor]]
			m.detail = &e
		}
	case "u":
		if m.limited && !m.env.IsRoot {
			return nav.Push(runflow.Confirm(AddToAdmPlan(currentUser()), m.env.Deps))
		}
	}
	return nil
}

func (m *EntriesModel) startFollow() tea.Cmd {
	m.following = true
	m.viewer.Follow = true
	cursor := ""
	if len(m.entries) > 0 {
		cursor = m.entries[len(m.entries)-1].Cursor
	}
	start, argv := m.env.Deps.Start, m.query.FollowArgs(cursor)
	return func() tea.Msg {
		s, err := start(context.Background(), argv, "")
		return followStartedMsg{owner: m, stream: s, err: err}
	}
}

func (m *EntriesModel) waitFollow() tea.Cmd {
	s := m.stream
	return func() tea.Msg { return followEventMsg{owner: m, ev: s.Next()} }
}

func (m *EntriesModel) stopFollow() {
	if m.stream != nil {
		m.stream.Cancel()
	}
	m.following = false
}

func (m *EntriesModel) rebuild() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.shown = m.shown[:0]
	lines := make([]string, 0, len(m.entries))
	for i, e := range m.entries {
		if q != "" && !strings.Contains(strings.ToLower(e.Message+" "+e.Source()), q) {
			continue
		}
		m.shown = append(m.shown, i)
		lines = append(lines, FormatEntry(e))
	}
	m.viewer.SetLines(lines)
}

// PriorityStyle memberi warna per prioritas journal.
func PriorityStyle(p int) lipgloss.Style {
	t := ui.Current
	switch {
	case p <= syslogs.PrioErr:
		return t.Danger
	case p == syslogs.PrioWarning:
		return t.Warning
	case p == syslogs.PrioDebug:
		return t.Muted
	}
	return lipgloss.NewStyle()
}

// FormatEntry merender satu entri menjadi satu baris berwarna.
func FormatEntry(e syslogs.Entry) string {
	t := ui.Current
	ts := e.Time.Format("01-02 15:04:05")
	prio := fmt.Sprintf("%-10s", syslogs.PriorityName(e.Priority))
	src := e.Source()
	if len(src) > 18 {
		src = src[:17] + "…"
	}
	msg := strings.ReplaceAll(e.Message, "\n", " ⏎ ")
	return t.Muted.Render(ts) + " " + PriorityStyle(e.Priority).Render(prio) + " " + t.Accent.Render(fmt.Sprintf("%-18s", src)) + " " + PriorityStyle(e.Priority).Render(msg)
}

func (m *EntriesModel) View(width, height int) string {
	t := ui.Current
	cmdLine := run.JoinShell(m.query.Args())
	if m.following {
		cmdLine = run.JoinShell(append(m.query.Args(), "-f"))
	}
	var top []string
	top = append(top, "", " "+t.Muted.Render("command setara: ")+t.Code.Render(" "+cmdLine+" "))
	if m.limited && !m.env.IsRoot {
		top = append(top, ui.Wrap(t.Warning.Render("⚠ Sebagian log (milik sistem & user lain) tidak terlihat. Tekan u untuk menambahkan user ini ke grup adm, atau jalankan sudo ubt."), width, " "))
	}
	if m.following {
		top = append(top, " "+t.Success.Render("● mengikuti log baru secara langsung")+t.Muted.Render(" — tekan f atau esc untuk berhenti"))
	}
	if m.typing || m.filter.Value() != "" {
		top = append(top, " "+m.filter.View())
	}
	top = append(top, "")
	topStr := strings.Join(top, "\n")

	if m.detail != nil {
		return ui.FitHeight(topStr+"\n"+m.renderDetail(*m.detail, width), width, height)
	}
	switch {
	case !m.loaded:
		return topStr + "\n " + t.Subtle.Render("Membaca journal…")
	case m.err != nil && len(m.entries) == 0:
		return topStr + "\n" + ui.ErrorState(m.err, "", width)
	case len(m.shown) == 0:
		msg := "Tidak ada log yang cocok."
		if len(m.entries) == 0 {
			msg = "Journal tidak berisi entri untuk kriteria ini — kabar baik bila yang dicari adalah error."
		}
		return topStr + "\n" + ui.EmptyState("Kosong", msg, "", width)
	}
	return topStr + "\n" + m.viewer.View(width, max(height-lipgloss.Height(topStr), 3), true)
}

func (m *EntriesModel) renderDetail(e syslogs.Entry, width int) string {
	t := ui.Current
	pairs := []ui.Pair{
		{Key: "Waktu", Value: e.Time.Format("2006-01-02 15:04:05.000") + " (" + shared.Ago(e.Time, m.env.Now()) + ")"},
		{Key: "Tingkat", Value: PriorityStyle(e.Priority).Render(fmt.Sprintf("%s (%d)", syslogs.PriorityName(e.Priority), e.Priority)), Hint: "0–3 error & lebih parah, 4 peringatan, 5–6 informasi, 7 debug"},
		{Key: "Sumber", Value: e.Source()},
		{Key: "Unit", Value: e.Unit},
		{Key: "PID", Value: fmt.Sprint(e.PID)},
		{Key: "Host", Value: e.Hostname},
	}
	lines := []string{ui.Detail(pairs, width), "", ui.Section("Pesan")}
	lines = append(lines, ui.Wrap(e.Message, width, "   "))
	if e.Unit != "" {
		lines = append(lines, "", " "+t.Muted.Render("Lihat semua log unit ini: ")+t.Code.Render(" journalctl -u "+e.Unit+" -b "))
	}
	return strings.Join(lines, "\n")
}
