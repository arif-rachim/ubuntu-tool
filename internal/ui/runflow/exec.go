package runflow

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// Batas baris output yang disimpan per langkah (yang lama dibuang).
const maxOutputLines = 5000

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ExecModel menjalankan langkah-langkah Plan satu per satu dan menampilkan progresnya.
type ExecModel struct {
	plan    run.Plan
	deps    Deps
	seq     []run.Step
	results []run.StepResult

	cur        int  // langkah yang sedang/akan berjalan
	running    bool // ada proses berjalan
	done       bool
	sudoReady  bool
	sudoRetry  bool // sudah pernah meminta ulang izin sudo untuk langkah ini
	stream     run.Stream
	started    time.Time
	selected   int // langkah yang outputnya ditampilkan setelah selesai
	scrollBack int // baris digulir ke atas dari akhir output
	frame      int
	warnings   []string

	width, height int
}

// Pesan internal. owner memastikan pesan milik instance ini.
type (
	streamStartedMsg struct {
		owner  *ExecModel
		stream run.Stream
		err    error
	}
	streamEventMsg struct {
		owner *ExecModel
		ev    run.Event
	}
	interactiveDoneMsg struct {
		owner *ExecModel
		err   error
	}
	sudoValidatedMsg struct {
		owner *ExecModel
		err   error
	}
	tickMsg struct{ owner *ExecModel }
)

func newExec(p run.Plan, d Deps, sudoCached bool) *ExecModel {
	seq := p.Sequence()
	m := &ExecModel{
		plan:      p,
		deps:      d,
		seq:       seq,
		results:   make([]run.StepResult, len(seq)),
		sudoReady: sudoCached || d.Env.IsRoot,
		width:     80,
		height:    24,
	}
	for i, s := range seq {
		m.results[i] = run.StepResult{Step: s, Status: run.StatusPending}
	}
	return m
}

var _ nav.Screen = (*ExecModel)(nil)

func (m *ExecModel) Title() string { return i18n.ExecTitle }

// Busy: selama proses berjalan, semua tombol (termasuk ctrl+c) ditangani layar ini.
func (m *ExecModel) Busy() bool { return !m.done }

func (m *ExecModel) HandlesBack() bool { return true }

func (m *ExecModel) Init() tea.Cmd { return tea.Batch(m.startCurrent(), m.tick()) }

func (m *ExecModel) tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{owner: m} })
}

// Outcome mengembalikan hasil saat ini.
func (m *ExecModel) Outcome() run.Outcome {
	return run.Outcome{Plan: m.plan, Approved: true, Results: append([]run.StepResult(nil), m.results...)}
}

func (m *ExecModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	if !m.done {
		return []key.Binding{b("ctrl+c", i18n.ExecKeyStop), b("pgup/pgdn", i18n.ConfirmKeyScroll)}
	}
	return []key.Binding{b("enter", i18n.ExecKeyDone), b("↑↓", i18n.ExecKeyPickStep), b("pgup/pgdn", i18n.ConfirmKeyScroll)}
}

func (m *ExecModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.SizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		if msg.owner == m && !m.done {
			m.frame++
			return m, m.tick()
		}
	case sudoValidatedMsg:
		if msg.owner == m {
			return m, m.onSudoValidated(msg.err)
		}
	case streamStartedMsg:
		if msg.owner == m {
			return m, m.onStreamStarted(msg)
		}
	case streamEventMsg:
		if msg.owner == m {
			return m, m.onStreamEvent(msg.ev)
		}
	case interactiveDoneMsg:
		if msg.owner == m {
			return m, m.finishStep(run.ExitCode(msg.err), msg.err)
		}
	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *ExecModel) handleKey(k string) tea.Cmd {
	switch k {
	case "pgup":
		m.scrollBack += 10
		return nil
	case "pgdown":
		m.scrollBack = max(m.scrollBack-10, 0)
		return nil
	}
	if !m.done {
		if k == "ctrl+c" || k == "esc" {
			if m.stream != nil {
				m.stream.Cancel()
			}
		}
		return nil
	}
	switch k {
	case "enter", "esc", "q":
		return nav.Pop(m.Outcome())
	case "up", "k":
		m.selected = max(m.selected-1, 0)
		m.scrollBack = 0
	case "down", "j":
		m.selected = min(m.selected+1, len(m.seq)-1)
		m.scrollBack = 0
	}
	return nil
}

// startCurrent memulai langkah m.cur, meminta izin sudo dulu bila perlu.
func (m *ExecModel) startCurrent() tea.Cmd {
	if m.cur >= len(m.seq) {
		m.done = true
		m.running = false
		return nil
	}
	step := m.seq[m.cur]
	c := step.Command
	isRoot := m.deps.Env.IsRoot
	m.selected = m.cur
	m.scrollBack = 0
	m.running = true
	m.results[m.cur].Status = run.StatusRunning
	m.started = time.Now()

	if c.UsesSudo(isRoot) && !m.sudoReady {
		v := run.SudoValidate()
		cmd := exec.Command(v.Argv[0], v.Argv[1:]...)
		return m.deps.Interactive(cmd, func(err error) tea.Msg { return sudoValidatedMsg{owner: m, err: err} })
	}

	if c.Interactive {
		cmd := interactiveCmd(c.ExecArgv(isRoot, false), c.Stdin)
		return m.deps.Interactive(cmd, func(err error) tea.Msg { return interactiveDoneMsg{owner: m, err: err} })
	}

	argv := c.ExecArgv(isRoot, true)
	start, stdin := m.deps.Start, c.Stdin
	return func() tea.Msg {
		s, err := start(context.Background(), argv, stdin)
		return streamStartedMsg{owner: m, stream: s, err: err}
	}
}

// interactiveCmd membungkus command interaktif supaya output terakhirnya sempat dibaca user
// sebelum layar ubt kembali. Command aslinya tetap argv yang sama persis.
func interactiveCmd(argv []string, stdin string) *exec.Cmd {
	script := `"$@"; s=$?; printf '\n[ubt] ` + i18n.ExecPressEnter + `' "$s"; read _ </dev/tty; exit $s`
	cmd := exec.Command("sh", append([]string{"-c", script, "ubt"}, argv...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	return cmd
}

func (m *ExecModel) onSudoValidated(err error) tea.Cmd {
	if err != nil {
		return m.finishStep(-1, errors.New(i18n.ExecSudoFailed))
	}
	m.sudoReady = true
	return m.startCurrent()
}

func (m *ExecModel) onStreamStarted(msg streamStartedMsg) tea.Cmd {
	if msg.err != nil {
		return m.finishStep(-1, msg.err)
	}
	m.stream = msg.stream
	return m.waitEvent()
}

func (m *ExecModel) waitEvent() tea.Cmd {
	s := m.stream
	return func() tea.Msg { return streamEventMsg{owner: m, ev: s.Next()} }
}

func (m *ExecModel) onStreamEvent(ev run.Event) tea.Cmd {
	r := &m.results[m.cur]
	if !ev.Done {
		r.Output = append(r.Output, ev.Line)
		if len(r.Output) > maxOutputLines {
			r.Output = r.Output[len(r.Output)-maxOutputLines:]
		}
		return m.waitEvent()
	}
	m.stream = nil

	// Izin sudo kedaluwarsa di tengah jalan: minta ulang sekali lalu ulangi langkah ini.
	if ev.ExitCode == 1 && m.seq[m.cur].Command.UsesSudo(m.deps.Env.IsRoot) && !m.sudoRetry && sudoNeedsPassword(r.Output) {
		m.sudoRetry = true
		m.sudoReady = false
		r.Output = nil
		return m.startCurrent()
	}
	if errors.Is(ev.Err, context.Canceled) {
		return m.cancelStep()
	}
	return m.finishStep(ev.ExitCode, ev.Err)
}

// sudoNeedsPassword mengenali kegagalan `sudo -n` karena izin kedaluwarsa: sudo keluar dengan
// satu baris pesan berawalan "sudo:" sebelum command dijalankan (pesannya bisa diterjemahkan
// sesuai locale, jadi yang dicek bentuknya, bukan kalimatnya).
func sudoNeedsPassword(output []string) bool {
	return len(output) == 1 && strings.HasPrefix(output[0], "sudo:")
}

func (m *ExecModel) record(exitCode int, err error) {
	if m.deps.History == nil {
		return
	}
	step := m.seq[m.cur]
	e := run.NewEntry(step, step.Command.UsesSudo(m.deps.Env.IsRoot), m.started, exitCode, err)
	if herr := m.deps.History.Append(e); herr != nil {
		m.warnings = append(m.warnings, fmt.Sprintf(i18n.ExecHistoryFailed, herr))
	}
}

// finishStep mencatat hasil langkah lalu lanjut atau berhenti.
func (m *ExecModel) finishStep(exitCode int, err error) tea.Cmd {
	r := &m.results[m.cur]
	r.ExitCode, r.Err = exitCode, err
	m.sudoRetry = false
	if err == nil && exitCode == 0 {
		r.Status = run.StatusOK
		m.record(exitCode, nil)
		m.cur++
		return m.startCurrent()
	}
	r.Status = run.StatusFailed
	m.record(exitCode, err)
	m.stopRemaining()
	return nil
}

func (m *ExecModel) cancelStep() tea.Cmd {
	r := &m.results[m.cur]
	r.Status, r.ExitCode, r.Err = run.StatusCancelled, -1, context.Canceled
	m.record(-1, context.Canceled)
	m.stopRemaining()
	return nil
}

func (m *ExecModel) stopRemaining() {
	for i := m.cur + 1; i < len(m.results); i++ {
		m.results[i].Status = run.StatusSkipped
	}
	m.done, m.running = true, false
	m.selected = m.cur
}

// View merender daftar langkah, output langkah terpilih, dan ringkasan.
func (m *ExecModel) View(width, height int) string {
	t := ui.Current
	var top []string
	top = append(top, "", " "+t.Title.Render(m.plan.Title), "")
	for i, r := range m.results {
		top = append(top, m.renderStepLine(i, r, width))
	}

	var bottom []string
	if m.done {
		bottom = append(bottom, "", m.summary(width))
	}
	for _, w := range m.warnings {
		bottom = append(bottom, ui.Wrap(t.Warning.Render("⚠ "+w), width, " "))
	}

	topStr := strings.Join(top, "\n")
	bottomStr := strings.Join(bottom, "\n")
	outHeight := height - lipgloss.Height(topStr) - 2
	if bottomStr != "" {
		outHeight -= lipgloss.Height(bottomStr)
	}
	out := m.renderOutput(width, max(outHeight, 1))
	return ui.FitHeight(topStr+"\n\n"+out+"\n"+bottomStr, width, height)
}

func (m *ExecModel) renderStepLine(i int, r run.StepResult, width int) string {
	t := ui.Current
	var icon string
	switch r.Status {
	case run.StatusRunning:
		icon = t.Accent.Render(spinnerFrames[m.frame%len(spinnerFrames)])
	case run.StatusOK:
		icon = t.Success.Render("✓")
	case run.StatusFailed:
		icon = t.Danger.Render("✗")
	case run.StatusCancelled:
		icon = t.Warning.Render("■")
	case run.StatusSkipped:
		icon = t.Muted.Render("·")
	default:
		icon = t.Muted.Render("○")
	}
	cursor := "  "
	if m.done && i == m.selected && len(m.results) > 1 {
		cursor = t.Selected.Render("❯ ")
	}
	title := r.Step.Command.Title
	if title == "" {
		title = r.Step.Command.Preview(m.deps.Env.IsRoot)
	}
	style := lipgloss.NewStyle()
	if r.Status == run.StatusSkipped || r.Status == run.StatusPending {
		style = t.Muted
	}
	line := fmt.Sprintf(" %s%s %s", cursor, icon, style.Render(fmt.Sprintf("%d. %s", i+1, title)))
	switch r.Status {
	case run.StatusSkipped:
		line += " " + t.Muted.Render("("+i18n.ExecSkipped+")")
	case run.StatusCancelled:
		line += " " + t.Warning.Render("("+i18n.ExecCancelled+")")
	case run.StatusFailed:
		line += " " + t.Danger.Render(fmt.Sprintf("(exit %d)", r.ExitCode))
	}
	preview := t.Muted.Render("$ " + r.Step.Command.Preview(m.deps.Env.IsRoot))
	if lipgloss.Width(line)+lipgloss.Width(preview)+3 <= width {
		line += "   " + preview
	}
	return line
}

func (m *ExecModel) renderOutput(width, height int) string {
	t := ui.Current
	idx := m.selected
	if !m.done {
		idx = m.cur
	}
	if idx >= len(m.results) {
		return ""
	}
	r := m.results[idx]
	header := " " + t.Subtle.Render(fmt.Sprintf(i18n.ExecOutputOf, idx+1))
	bodyHeight := max(height-1, 1)

	var lines []string
	switch {
	case r.Step.Command.Interactive:
		lines = []string{t.Muted.Render(i18n.ExecInteractiveOut)}
	case len(r.Output) == 0 && r.Status != run.StatusPending && r.Status != run.StatusRunning:
		lines = []string{t.Muted.Render(i18n.ExecNoOutput)}
	default:
		end := max(len(r.Output)-m.scrollBack, 0)
		m.scrollBack = len(r.Output) - end
		start := max(end-bodyHeight, 0)
		// Salin: baris akan diberi gutter di bawah dan tidak boleh mengubah output yang tersimpan.
		lines = append([]string(nil), r.Output[start:end]...)
	}
	// Kode keluar -1 berarti command tidak sempat berjalan (program tidak ada, izin sudo gagal):
	// pesan error-nya yang menjelaskan, bukan output.
	if r.Err != nil && !errors.Is(r.Err, context.Canceled) && r.ExitCode == -1 {
		lines = append(lines, t.Danger.Render(r.Err.Error()))
	}
	if len(lines) > bodyHeight {
		lines = lines[len(lines)-bodyHeight:]
	}
	gutter := t.Border.Render("│ ")
	for i, l := range lines {
		lines[i] = " " + gutter + l
	}
	return header + "\n" + strings.Join(lines, "\n")
}

func (m *ExecModel) summary(width int) string {
	t := ui.Current
	if m.cur >= len(m.results) {
		return " " + t.Success.Render("✓ "+i18n.ExecAllOK)
	}
	r := m.results[m.cur]
	var s string
	switch r.Status {
	case run.StatusCancelled:
		s = t.Warning.Render("■ " + fmt.Sprintf(i18n.ExecCancelledAt, m.cur+1))
	default:
		if r.ExitCode == -1 && r.Err != nil {
			s = t.Danger.Render("✗ " + fmt.Sprintf(i18n.ExecFailedErr, m.cur+1, r.Err))
		} else {
			s = t.Danger.Render("✗ " + fmt.Sprintf(i18n.ExecFailedAt, m.cur+1, r.ExitCode))
		}
	}
	var ran []string
	for i := 0; i < m.cur; i++ {
		if m.results[i].Status == run.StatusOK {
			ran = append(ran, fmt.Sprint(i+1))
		}
	}
	if len(ran) > 0 {
		s += " " + t.Subtle.Render(fmt.Sprintf(i18n.ExecAlreadyRan, strings.Join(ran, ", ")))
	}
	return ui.Wrap(s, width, " ")
}
