package runflow

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// Jumlah baris stdin maksimum yang ditampilkan di layar konfirmasi.
const maxStdinLines = 12

// ConfirmModel adalah layar "preview command + penjelasan + konfirmasi".
type ConfirmModel struct {
	plan  run.Plan
	deps  Deps
	sudo  run.SudoState
	known bool // status sudo sudah diketahui
	armed bool // aksi berbahaya: y pertama sudah ditekan
	toast string
	err   string

	offset        int
	width, height int
}

type sudoCheckedMsg struct {
	owner *ConfirmModel
	state run.SudoState
}

func newConfirm(p run.Plan, d Deps) *ConfirmModel {
	m := &ConfirmModel{plan: p, deps: d, width: 80, height: 24}
	if !p.NeedsRoot() || d.Env.IsRoot {
		m.sudo, m.known = run.SudoNotNeeded, true
	}
	return m
}

var _ nav.Screen = (*ConfirmModel)(nil)

func (m *ConfirmModel) Title() string { return i18n.ConfirmTitle }

// HandlesBack: esc berarti batal dengan hasil Outcome{Approved:false}.
func (m *ConfirmModel) HandlesBack() bool { return true }

// Init memeriksa sudo di latar belakang bila ada langkah yang butuh root.
func (m *ConfirmModel) Init() tea.Cmd {
	if m.known || m.deps.Env.SudoCheck == nil {
		if !m.known {
			m.sudo, m.known = run.SudoNeedsPasswd, true
		}
		return nil
	}
	check := m.deps.Env.SudoCheck
	return func() tea.Msg { return sudoCheckedMsg{owner: m, state: check(context.Background())} }
}

func (m *ConfirmModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{
		b("y", i18n.ConfirmKeyRun),
		b("n", i18n.ConfirmKeyCancel),
		b("c", i18n.ConfirmKeyCopy),
		b("↑↓", i18n.ConfirmKeyScroll),
	}
}

func (m *ConfirmModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.SizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case sudoCheckedMsg:
		if msg.owner == m {
			m.sudo, m.known = msg.state, true
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *ConfirmModel) handleKey(k string) tea.Cmd {
	wasArmed := m.armed
	m.armed = false
	m.toast, m.err = "", ""

	switch k {
	case "y", "Y":
		if m.known && m.sudo == run.SudoMissing {
			m.err = i18n.ConfirmSudoMissing
			return nil
		}
		if m.plan.Risk() == risk.Dangerous && !wasArmed {
			m.armed = true
			return nil
		}
		return nav.Replace(newExec(m.plan, m.deps, m.sudo == run.SudoCached))
	case "n", "N", "esc", "q":
		return nav.Pop(run.Outcome{Plan: m.plan})
	case "c":
		m.toast = i18n.ConfirmCopied
		return tea.SetClipboard(m.script())
	case "up", "k":
		m.offset = max(m.offset-1, 0)
	case "down", "j":
		m.offset++
	case "pgup":
		m.offset = max(m.offset-10, 0)
	case "pgdown", "space":
		m.offset += 10
	case "home", "g":
		m.offset = 0
	}
	return nil
}

// script adalah seluruh Plan sebagai teks shell untuk disalin.
func (m *ConfirmModel) script() string {
	var parts []string
	for _, s := range m.plan.Sequence() {
		parts = append(parts, s.Command.Script(m.deps.Env.IsRoot))
	}
	return strings.Join(parts, "\n")
}

// View merender isi konfirmasi dengan bagian bawah (status sudo & tombol) selalu terlihat.
func (m *ConfirmModel) View(width, height int) string {
	t := ui.Current
	body := m.renderBody(width)

	var footer []string
	footer = append(footer, "")
	if line := m.sudoLine(); line != "" {
		footer = append(footer, ui.Wrap(line, width, " "))
	}
	switch {
	case m.err != "":
		footer = append(footer, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	case m.armed:
		footer = append(footer, ui.Wrap(t.Danger.Render("⚠ "+i18n.ConfirmArmed), width, " "))
	case m.toast != "":
		footer = append(footer, ui.Wrap(t.Success.Render("✓ "+m.toast), width, " "))
	}
	footer = append(footer, " "+m.buttons())
	footerStr := strings.Join(footer, "\n")

	lines := strings.Split(body, "\n")
	avail := max(height-lipgloss.Height(footerStr), 3)
	m.offset = min(m.offset, max(len(lines)-avail, 0))
	visible := lines[m.offset:min(m.offset+avail, len(lines))]
	if m.offset > 0 {
		visible[0] = " " + t.Muted.Render(fmt.Sprintf(i18n.AskMoreAbove, m.offset))
	}
	if rest := len(lines) - m.offset - avail; rest > 0 {
		visible[len(visible)-1] = " " + t.Muted.Render(fmt.Sprintf(i18n.AskMoreBelow, rest+1))
	}
	return ui.FitHeight(strings.Join(visible, "\n"), width, avail) + "\n" + footerStr
}

func (m *ConfirmModel) buttons() string {
	t := ui.Current
	run := lipgloss.NewStyle().Bold(true).Foreground(t.RiskStyle(m.plan.Risk()).GetForeground()).Render("[ y Jalankan ]")
	return run + "   " + t.Subtle.Render("[ n Batal ]") + "   " + t.Subtle.Render("[ c Salin command ]")
}

func riskBadge(l risk.Level) string {
	t := ui.Current
	switch l {
	case risk.Dangerous:
		return t.Danger.Render("⛔ " + i18n.RiskDangerous)
	case risk.Caution:
		return t.Warning.Bold(true).Render("⚠  " + i18n.RiskCaution)
	default:
		return t.Success.Bold(true).Render("✓ " + i18n.RiskSafe)
	}
}

func (m *ConfirmModel) sudoLine() string {
	t := ui.Current
	if !m.plan.NeedsRoot() {
		return ""
	}
	switch {
	case !m.known:
		return t.Subtle.Render(i18n.ConfirmSudoChecking)
	case m.deps.Env.IsRoot:
		return t.Warning.Render("# " + i18n.ConfirmSudoRoot)
	case m.sudo == run.SudoCached:
		return t.Subtle.Render("🔑 " + i18n.ConfirmSudoCached)
	case m.sudo == run.SudoMissing:
		return t.Danger.Render("✗ " + i18n.ConfirmSudoMissing)
	default:
		return t.Accent.Render("🔑 " + i18n.ConfirmSudoPasswd)
	}
}

func (m *ConfirmModel) renderBody(width int) string {
	t := ui.Current
	seq := m.plan.Sequence()
	var b strings.Builder

	b.WriteString("\n " + riskBadge(m.plan.Risk()) + "  " + t.Title.Render(m.plan.Title) + "\n\n")

	if len(seq) == 1 {
		b.WriteString(" " + t.Subtle.Render(i18n.ConfirmCommand) + "\n")
		m.renderStep(&b, seq[0], "", width)
	} else {
		b.WriteString(" " + t.Subtle.Render(fmt.Sprintf(i18n.ConfirmSteps, len(seq))) + "\n")
		for i, s := range seq {
			b.WriteString("\n")
			title := s.Command.Title
			if s.IsCheck {
				title += " " + t.Muted.Render("("+i18n.ConfirmCheck+")")
			}
			marker := t.RiskMarker(s.Command.Risk)
			if marker != "" {
				marker = " " + marker
			}
			b.WriteString(fmt.Sprintf(" %s %s%s\n", t.Accent.Render(fmt.Sprintf("%d.", i+1)), t.Title.Render(title), marker))
			m.renderStep(&b, s, "   ", width)
		}
		b.WriteString("\n " + t.Muted.Render(i18n.ConfirmStopOnFail) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderStep menulis command, penjelasan, stdin, efek, dan saran untuk satu langkah.
func (m *ConfirmModel) renderStep(b *strings.Builder, s run.Step, indent string, width int) {
	t := ui.Current
	c := s.Command
	pad := indent + "    "

	cmdLine := t.Muted.Render("$ ") + t.Code.Render(" "+c.Preview(m.deps.Env.IsRoot)+" ")
	b.WriteString(ui.Wrap(cmdLine, width, pad) + "\n")

	if len(c.Explain) > 0 {
		b.WriteString(indent + "  " + t.Subtle.Render(i18n.ConfirmMeaning) + "\n")
		tokenW := 0
		for _, l := range c.Explain {
			tokenW = max(tokenW, lipgloss.Width(l.Token))
		}
		for _, l := range c.Explain {
			tok := t.Accent.Render(l.Token) + strings.Repeat(" ", tokenW-lipgloss.Width(l.Token))
			prefix := pad + tok + " → "
			meaning := ui.Wrap(l.Meaning, width-lipgloss.Width(prefix), "")
			mlines := strings.Split(meaning, "\n")
			b.WriteString(prefix + mlines[0] + "\n")
			for _, ml := range mlines[1:] {
				b.WriteString(strings.Repeat(" ", lipgloss.Width(prefix)) + ml + "\n")
			}
		}
	}

	if c.Stdin != "" {
		label := i18n.ConfirmStdin
		if c.StdinLabel != "" {
			label += " " + c.StdinLabel
		}
		b.WriteString(indent + "  " + t.Subtle.Render(label+":") + "\n")
		if c.Sensitive {
			b.WriteString(pad + t.Muted.Render("("+i18n.ConfirmStdinHidden+")") + "\n")
		} else {
			lines := strings.Split(strings.TrimRight(c.Stdin, "\n"), "\n")
			shown := lines
			if len(lines) > maxStdinLines {
				shown = lines[:maxStdinLines]
			}
			for _, l := range shown {
				b.WriteString(pad + t.Border.Render("│ ") + l + "\n")
			}
			if len(lines) > maxStdinLines {
				b.WriteString(pad + t.Muted.Render(fmt.Sprintf(i18n.ConfirmStdinMore, len(lines)-maxStdinLines)) + "\n")
			}
		}
	}
	if c.Interactive {
		b.WriteString(ui.Wrap(t.Subtle.Render(i18n.ConfirmInteractive), width, indent+"  ") + "\n")
	}
	if c.Effect != "" {
		b.WriteString(ui.Wrap(t.Subtle.Render(i18n.ConfirmEffect)+" "+c.Effect, width, indent+"  ") + "\n")
	}
	if c.Safer != "" {
		b.WriteString(ui.Wrap(t.Success.Render(i18n.ConfirmSafer)+" "+c.Safer, width, indent+"  ") + "\n")
	}
}
