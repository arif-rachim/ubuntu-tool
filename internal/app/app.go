// Package app berisi root model Bubble Tea: tumpukan layar, tombol global, dan frame.
package app

import (
	"os"
	"os/user"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// Model adalah root model.
type Model struct {
	stack    []nav.Screen
	width    int
	height   int
	header   ui.Header
	showHelp bool
	keys     globalKeys
}

// New membuat root model dengan layar awal.
func New(root nav.Screen) Model {
	return Model{
		stack:  []nav.Screen{root},
		width:  80,
		height: 24,
		header: ui.Header{UserHost: userHost(), IsRoot: os.Geteuid() == 0},
		keys:   newGlobalKeys(),
	}
}

func userHost() string {
	name := "?"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil {
		host = "?"
	}
	return name + "@" + host
}

// Init meminta warna latar terminal lalu menjalankan Init layar awal.
func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.top().Init())
}

func (m Model) top() nav.Screen { return m.stack[len(m.stack)-1] }

func (m Model) bodySize() (int, int) {
	return m.width, max(m.height-ui.HeaderHeight-ui.FooterHeight, 0)
}

// Update menangani pesan global dan meneruskan sisanya ke layar teratas.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		ui.SetDark(msg.IsDark())
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		w, h := m.bodySize()
		var cmds []tea.Cmd
		for i, s := range m.stack {
			var cmd tea.Cmd
			m.stack[i], cmd = s.Update(nav.SizeMsg{Width: w, Height: h})
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case nav.PushMsg:
		m.stack = append(m.stack, msg.Screen)
		w, h := m.bodySize()
		s, sizeCmd := msg.Screen.Update(nav.SizeMsg{Width: w, Height: h})
		m.stack[len(m.stack)-1] = s
		return m, tea.Batch(sizeCmd, s.Init())

	case nav.ReplaceMsg:
		m.stack[len(m.stack)-1] = msg.Screen
		w, h := m.bodySize()
		s, sizeCmd := msg.Screen.Update(nav.SizeMsg{Width: w, Height: h})
		m.stack[len(m.stack)-1] = s
		return m, tea.Batch(sizeCmd, s.Init())

	case nav.PopMsg:
		if len(m.stack) == 1 {
			return m, tea.Quit
		}
		m.stack = m.stack[:len(m.stack)-1]
		return m.forward(nav.ResumedMsg{Result: msg.Result})

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m.forward(msg)
}

func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	s, cmd := m.top().Update(msg)
	m.stack[len(m.stack)-1] = s
	return m, cmd
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.forceQuit) {
		return m, tea.Quit
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	top := m.top()
	if t, ok := top.(nav.Typer); ok && t.Typing() {
		return m.forward(msg)
	}

	switch {
	case key.Matches(msg, m.keys.quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.help):
		m.showHelp = true
		return m, nil
	case key.Matches(msg, m.keys.refresh):
		return m.forward(nav.RefreshMsg{})
	case key.Matches(msg, m.keys.back):
		if b, ok := top.(nav.BackHandler); ok && b.HandlesBack() {
			return m.forward(msg)
		}
		if len(m.stack) > 1 {
			return m, nav.Pop(nil)
		}
		return m, nil
	}
	return m.forward(msg)
}

// View merender frame lengkap.
func (m Model) View() tea.View {
	w, h := m.bodySize()
	top := m.top()

	crumbs := make([]string, 0, len(m.stack))
	for _, s := range m.stack {
		crumbs = append(crumbs, s.Title())
	}
	header := m.header
	header.Breadcrumb = crumbs

	var body string
	var keys []key.Binding
	if m.showHelp {
		body = m.helpView(top, w)
		keys = []key.Binding{key.NewBinding(key.WithKeys("any"), key.WithHelp("•", i18n.HelpDismiss))}
	} else {
		body = top.View(w, h)
		typing := false
		if t, ok := top.(nav.Typer); ok {
			typing = t.Typing()
		}
		keys = dedupeKeys(append(top.Keys(), m.keys.footer(len(m.stack) > 1, typing)...))
	}

	v := tea.NewView(ui.Frame(header, body, keys, m.width, m.height))
	v.AltScreen = true
	v.WindowTitle = "ubt — " + top.Title()
	return v
}

func (m Model) helpView(top nav.Screen, width int) string {
	t := ui.Current
	var b strings.Builder
	b.WriteString("\n " + t.Title.Render(i18n.HelpTitle) + "\n")

	if h, ok := top.(nav.Helper); ok && h.HelpText() != "" {
		b.WriteString("\n " + t.Accent.Render(i18n.HelpAbout) + "\n")
		b.WriteString(ui.Wrap(h.HelpText(), width, "   ") + "\n")
	}

	section := func(title string, keys []key.Binding) {
		b.WriteString("\n " + t.Accent.Render(title) + "\n")
		keyW := 0
		for _, k := range keys {
			keyW = max(keyW, lipgloss.Width(k.Help().Key))
		}
		for _, k := range keys {
			if k.Help().Key == "" {
				continue
			}
			pad := strings.Repeat(" ", keyW-lipgloss.Width(k.Help().Key))
			b.WriteString("   " + t.KeyName.Render(k.Help().Key) + pad + "  " + t.KeyDesc.Render(k.Help().Desc) + "\n")
		}
	}
	section(i18n.HelpScreenKeys, top.Keys())
	section(i18n.HelpGlobalKeys, m.keys.all())
	return b.String()
}

// dedupeKeys membuang binding dengan label tombol yang sama; yang pertama (milik layar) menang.
func dedupeKeys(keys []key.Binding) []key.Binding {
	seen := map[string]bool{}
	out := keys[:0:0]
	for _, k := range keys {
		if seen[k.Help().Key] {
			continue
		}
		seen[k.Help().Key] = true
		out = append(out, k)
	}
	return out
}
