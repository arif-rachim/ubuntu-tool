// Package diagnose adalah layar menu Diagnosa: user memilih gejala, ubt menjalankan pemeriksaan
// lintas modul dan menawarkan membuka modul yang bisa memperbaikinya.
package diagnose

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/diagnose"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/checklist"
)

// Opener membuka layar modul berdasarkan ID ("disk", "network:inbound", ...). Nil bila tidak tersedia.
type Opener func(id string) nav.Screen

// Model adalah menu gejala.
type Model struct {
	env      diagnose.Env
	open     Opener
	names    map[string]string
	symptoms []diagnose.Symptom
	cursor   int
}

// New membuat menu Diagnosa. names memetakan ID modul ke nama tampilan.
func New(env diagnose.Env, open Opener, names map[string]string) *Model {
	return &Model{env: env, open: open, names: names, symptoms: diagnose.Symptoms()}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Diagnosa" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑↓", "pindah")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "periksa")),
	}
}

func (m *Model) HelpText() string {
	return "Pilih gejala yang kamu rasakan. ubt hanya membaca kondisi server (tidak mengubah apa pun), " +
		"lalu menjelaskan temuan dan modul yang bisa memperbaikinya. Setiap perbaikan tetap lewat layar konfirmasi di modul tersebut."
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch s := k.String(); s {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(m.symptoms)) % len(m.symptoms)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(m.symptoms)
	case "enter":
		return m, m.start(m.symptoms[m.cursor])
	default:
		if len(s) == 1 && s[0] >= '1' && int(s[0]-'1') < len(m.symptoms) {
			m.cursor = int(s[0] - '1')
			return m, m.start(m.symptoms[m.cursor])
		}
	}
	return m, nil
}

func (m *Model) start(sym diagnose.Symptom) tea.Cmd {
	if sym.Open != "" {
		if m.open != nil {
			if s := m.open(sym.Open); s != nil {
				return nav.Push(s)
			}
		}
		return nil
	}
	c := checklist.New(sym.Label, sym.Intro, sym.Steps(m.env))
	c.Independent = true
	c.ModuleName = func(id string) string {
		if n, ok := m.names[id]; ok {
			return "modul " + n
		}
		return "modul " + id
	}
	if m.open != nil {
		c.OnNext = func(id string) tea.Cmd {
			if s := m.open(id); s != nil {
				return nav.Push(s)
			}
			return nil
		}
	}
	return nav.Push(c)
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	lines := []string{"", ui.Wrap(t.Subtle.Render("Apa yang sedang terjadi? Semua pemeriksaan hanya membaca; perbaikan tetap dikonfirmasi di modul terkait."), width, " "), ""}
	cursorLine := 0
	for i, s := range m.symptoms {
		label := string(rune('1'+i)) + ". " + s.Label
		if i == m.cursor {
			cursorLine = len(lines)
			lines = append(lines, " "+t.Selected.Render("❯ "+label))
		} else {
			lines = append(lines, "   "+label)
		}
		lines = append(lines, ui.Wrap(t.Subtle.Render(s.Description), width, "      "))
	}
	all := strings.Split(strings.Join(lines, "\n"), "\n")
	offset := 0
	if cursorLine >= height-2 {
		offset = cursorLine - height + 3
	}
	return ui.FitHeight(strings.Join(all[min(offset, len(all)):], "\n"), width, height)
}
