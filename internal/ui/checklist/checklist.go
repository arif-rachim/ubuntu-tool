// Package checklist menjalankan pemeriksaan bertahap (check.Step) satu per satu dan menampilkan
// hasilnya secara langsung, berhenti di langkah pertama yang menemukan penyebab masalah
// (kecuali mode Independent, yang menjalankan semua langkah).
package checklist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Model adalah layar pemeriksaan bertahap.
type Model struct {
	title   string
	intro   string
	steps   []check.Step
	results []*check.Result
	cur     int
	done    bool
	frame   int
	cancel  context.CancelFunc
	offset  int
	started time.Time
	// OnNext dipanggil saat user menekan enter pada langkah yang punya Result.Next (membuka modul lain).
	OnNext func(moduleID string) tea.Cmd
	// Independent: langkah tidak saling bergantung, jadi semua tetap dijalankan walau ada yang gagal
	// (dipakai Diagnosa). Angka 1-9 membuka modul yang disarankan langkah tersebut.
	Independent bool
	// ModuleName menerjemahkan ID modul menjadi nama untuk petunjuk tombol (opsional).
	ModuleName func(id string) string
}

type (
	stepMsg struct {
		owner *Model
		index int
		res   check.Result
	}
	tickMsg struct{ owner *Model }
)

// New membuat layar pemeriksaan.
func New(title, intro string, steps []check.Step) *Model {
	return &Model{title: title, intro: intro, steps: steps, results: make([]*check.Result, len(steps))}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return m.title }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	keys := []key.Binding{b("r", "periksa ulang"), b("↑↓", "gulir")}
	if next := m.nextModule(); next != "" && m.OnNext != nil {
		keys = append([]key.Binding{b("enter", "buka modul yang disarankan")}, keys...)
	}
	return keys
}

// Init memulai pemeriksaan dari awal.
func (m *Model) Init() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.results = make([]*check.Result, len(m.steps))
	m.cur, m.done, m.started = 0, false, time.Now()
	return tea.Batch(m.runStep(ctx, 0), m.tick())
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{owner: m} })
}

func (m *Model) runStep(ctx context.Context, i int) tea.Cmd {
	if i >= len(m.steps) {
		m.done = true
		return nil
	}
	step := m.steps[i]
	return func() tea.Msg {
		stepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return stepMsg{owner: m, index: i, res: step.Run(stepCtx)}
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		if msg.owner == m && !m.done {
			m.frame++
			return m, m.tick()
		}
	case stepMsg:
		if msg.owner != m || msg.index != m.cur || m.done {
			return m, nil
		}
		res := msg.res
		m.results[msg.index] = &res
		m.cur++
		if (res.Status == check.Fail && !m.Independent) || m.cur >= len(m.steps) {
			m.done = true
			return m, nil
		}
		ctx := context.Background()
		return m, m.runStep(ctx, m.cur)
	case nav.RefreshMsg:
		return m, m.Init()
	case tea.KeyPressMsg:
		switch msg.String() {
		case "r":
			return m, m.Init()
		case "up", "k":
			m.offset = max(m.offset-1, 0)
		case "down", "j":
			m.offset++
		case "enter":
			if next := m.nextModule(); next != "" && m.OnNext != nil {
				return m, m.OnNext(next)
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			i := int(msg.String()[0] - '1')
			if m.Independent && m.OnNext != nil && i < len(m.results) && m.results[i] != nil && m.results[i].Next != "" && m.results[i].Status != check.OK {
				return m, m.OnNext(m.results[i].Next)
			}
		}
	}
	return m, nil
}

// Results mengembalikan hasil yang sudah ada (untuk test & modul Diagnosa).
func (m *Model) Results() []*check.Result { return m.results }

// Done melaporkan pemeriksaan selesai.
func (m *Model) Done() bool { return m.done }

func (m *Model) nextModule() string {
	for _, want := range []check.Status{check.Fail, check.Warn} {
		for _, r := range m.results {
			if r != nil && r.Status == want && r.Next != "" {
				return r.Next
			}
		}
		if !m.Independent {
			break
		}
	}
	return ""
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	var lines []string
	lines = append(lines, "")
	if m.intro != "" {
		lines = append(lines, ui.Wrap(t.Subtle.Render(m.intro), width, " "), "")
	}
	failed := -1
	for i, step := range m.steps {
		res := m.results[i]
		var icon string
		switch {
		case res == nil && i == m.cur && !m.done:
			icon = t.Accent.Render(spinner[m.frame%len(spinner)])
		case res == nil:
			icon = t.Muted.Render("○")
		case res.Status == check.OK:
			icon = t.Success.Render("✓")
		case res.Status == check.Warn:
			icon = t.Warning.Render("⚠")
		case res.Status == check.Fail:
			icon = t.Danger.Render("✗")
			failed = i
		default:
			icon = t.Muted.Render("–")
		}
		title := fmt.Sprintf("%d. %s", i+1, step.Title)
		if res == nil && (i != m.cur || m.done) {
			title = t.Muted.Render(title)
		} else {
			title = t.Title.Render(title)
		}
		lines = append(lines, " "+icon+" "+title)
		if res != nil {
			summaryStyle := t.Subtle
			switch res.Status {
			case check.Fail:
				summaryStyle = t.Danger
			case check.Warn:
				summaryStyle = t.Warning
			}
			if res.Summary != "" {
				lines = append(lines, ui.Wrap(summaryStyle.Render(res.Summary), width, "     "))
			}
			if res.Explain != "" && res.Status != check.OK {
				lines = append(lines, ui.Wrap(res.Explain, width, "     "))
			}
			if m.Independent && m.OnNext != nil && res.Next != "" && (res.Status == check.Fail || res.Status == check.Warn) && i < 9 {
				name := res.Next
				if m.ModuleName != nil {
					name = m.ModuleName(res.Next)
				}
				lines = append(lines, "     "+t.Accent.Render(fmt.Sprintf("→ tekan %d untuk membuka %s", i+1, name)))
			}
		}
		if step.Equivalent != "" && (res != nil || (i == m.cur && !m.done)) {
			lines = append(lines, "     "+t.Muted.Render("$ "+step.Equivalent))
		}
	}
	lines = append(lines, "")
	switch {
	case !m.done:
		lines = append(lines, " "+t.Subtle.Render("Memeriksa…"))
	case m.Independent:
		fails, warns := 0, 0
		for _, r := range m.results {
			switch {
			case r == nil:
			case r.Status == check.Fail:
				fails++
			case r.Status == check.Warn:
				warns++
			}
		}
		switch {
		case fails+warns == 0:
			lines = append(lines, " "+t.Success.Render("✓ Tidak ditemukan masalah.")+t.Subtle.Render(fmt.Sprintf(" (%s)", time.Since(m.started).Round(100*time.Millisecond))))
		default:
			summary := fmt.Sprintf("%d masalah, %d catatan.", fails, warns)
			style := t.Warning
			if fails > 0 {
				style = t.Danger
			}
			lines = append(lines, ui.Wrap(style.Render(summary)+t.Subtle.Render(" Setelah memperbaiki, tekan r untuk memeriksa ulang."), width, " "))
		}
	case failed >= 0:
		lines = append(lines, ui.Wrap(t.Danger.Render(fmt.Sprintf("Penyebab ditemukan di langkah %d.", failed+1))+t.Subtle.Render(" Langkah setelahnya tidak diperiksa karena bergantung pada langkah ini. Perbaiki, lalu tekan r untuk memeriksa ulang."), width, " "))
		if next := m.nextModule(); next != "" && m.OnNext != nil {
			lines = append(lines, " "+t.Accent.Render("Tekan enter untuk membuka modul yang bisa memperbaikinya."))
		}
	default:
		lines = append(lines, " "+t.Success.Render("✓ Semua langkah lolos.")+t.Subtle.Render(fmt.Sprintf(" (%s)", time.Since(m.started).Round(100*time.Millisecond))))
	}
	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}
