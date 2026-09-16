package ask

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
)

// Model adalah layar Form. Dibuat lewat New dan ditaruh di tumpukan dengan nav.Push.
// Hasilnya dikirim ke layar di bawahnya sebagai nav.ResumedMsg{Result: ask.Result{...}}.
type Model struct {
	form    Form
	answers Answers
	states  []*qstate

	pos           int  // indeks pertanyaan aktif di form.Questions
	inReview      bool // sedang di layar ringkasan
	reviewCursor  int
	fromReview    bool // sedang mengubah jawaban yang dibuka dari ringkasan
	confirmCancel bool
	done          bool

	preview    viewport.Model
	previewFor string

	width, height int

	ctx    context.Context
	cancel context.CancelFunc
}

// qstate adalah state tampilan satu pertanyaan.
type qstate struct {
	ready    bool
	loading  bool
	loadErr  error
	opts     []Option // sudah diurutkan, termasuk baris "Lainnya…"
	cursor   int      // indeks ke daftar opsi yang lolos filter
	selected map[string]bool

	otherActive bool
	other       textinput.Model
	input       textinput.Model
	area        textarea.Model

	filtering bool
	filter    textinput.Model

	err error
}

type loadedMsg struct {
	owner *Model
	index int
	opts  []Option
	err   error
}

// New membuat layar Form.
func New(f Form) *Model {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{
		form:    f,
		answers: Answers{},
		states:  make([]*qstate, len(f.Questions)),
		preview: viewport.New(),
		width:   80,
		height:  24,
		ctx:     ctx,
		cancel:  cancel,
	}
	for i := range m.states {
		m.states[i] = &qstate{selected: map[string]bool{}}
	}
	return m
}

var _ nav.Screen = (*Model)(nil)

// Init mengaktifkan pertanyaan pertama yang terlihat.
func (m *Model) Init() tea.Cmd {
	vis := m.visible()
	if len(vis) == 0 {
		return m.finish(false)
	}
	return m.activate(vis[0])
}

// Title dipakai untuk breadcrumb.
func (m *Model) Title() string { return m.form.Title }

// HandlesBack: esc berarti mundur ke pertanyaan sebelumnya, bukan menutup layar.
func (m *Model) HandlesBack() bool { return true }

// HelpText mengembalikan penjelasan pertanyaan aktif.
func (m *Model) HelpText() string {
	if m.inReview || m.pos >= len(m.form.Questions) {
		return ""
	}
	return m.form.Questions[m.pos].Help
}

// Typing melaporkan apakah user sedang mengetik, supaya huruf tidak dicuri tombol global.
func (m *Model) Typing() bool {
	if m.inReview || m.confirmCancel || m.done {
		return false
	}
	st := m.state()
	if st.otherActive || st.filtering {
		return true
	}
	k := m.question().Kind
	return k == Text || k == TextArea
}

// Answers mengembalikan jawaban sejauh ini (untuk test dan debugging).
func (m *Model) Answers() Answers { return m.visibleAnswers() }

func (m *Model) question() Question { return m.form.Questions[m.pos] }
func (m *Model) state() *qstate     { return m.states[m.pos] }

// visible mengembalikan indeks pertanyaan yang lolos When.
func (m *Model) visible() []int {
	var out []int
	for i, q := range m.form.Questions {
		if q.When == nil || q.When(m.answers) {
			out = append(out, i)
		}
	}
	return out
}

func (m *Model) reviewEnabled() bool {
	return !m.form.SkipReview && len(m.visible()) > 1
}

// Update menangani pesan.
func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.SizeMsg:
		m.setSize(msg.Width, msg.Height)
		return m, nil
	case loadedMsg:
		if msg.owner == m {
			m.applyLoaded(msg)
		}
		return m, nil
	case tea.PasteMsg:
		return m, m.updateInputs(msg)
	case tea.KeyPressMsg:
		if m.done {
			return m, nil
		}
		return m, m.handleKey(msg)
	}
	return m, m.updateInputs(msg)
}

func (m *Model) setSize(w, h int) {
	m.width, m.height = w, h
	inputWidth := max(min(w-8, 72), 20)
	// Input "Lainnya…" dirender di dalam baris opsi, setelah kursor, checkbox, dan nomor.
	otherWidth := max(min(w-18, 72), 10)
	for i, st := range m.states {
		st.other.SetWidth(otherWidth)
		st.input.SetWidth(inputWidth)
		st.filter.SetWidth(inputWidth)
		// textarea bernilai nol belum punya viewport; hanya atur yang sudah dibuat.
		if st.ready && m.form.Questions[i].Kind == TextArea {
			st.area.SetWidth(inputWidth)
		}
	}
}

// activate menyiapkan dan memfokuskan pertanyaan ke-i.
func (m *Model) activate(i int) tea.Cmd {
	m.pos = i
	m.inReview = false
	q := m.form.Questions[i]
	st := m.states[i]
	var cmds []tea.Cmd

	if !st.ready {
		st.ready = true
		st.other = newTextInput("")
		st.filter = newTextInput("")
		st.filter.Prompt = i18n.AskFilterPrompt
		switch q.Kind {
		case Text:
			st.input = newTextInput(q.Placeholder)
			if len(q.Default) > 0 {
				st.input.SetValue(q.Default[0])
				st.input.CursorEnd()
			}
		case TextArea:
			st.area = textarea.New()
			st.area.Placeholder = q.Placeholder
			st.area.ShowLineNumbers = false
			st.area.SetHeight(6)
			if len(q.Default) > 0 {
				st.area.SetValue(q.Default[0])
			}
		case Confirm:
			st.opts = confirmOptions(q)
			m.applyDefault(q, st)
		case Single, Multi:
			if q.Load != nil {
				st.loading = true
				cmds = append(cmds, m.loadCmd(i, q))
			} else {
				st.opts = withOther(sortRecommended(q.Options), q.Other)
				m.applyDefault(q, st)
			}
		}
		m.setSize(m.width, m.height)
	}

	switch q.Kind {
	case Text:
		cmds = append(cmds, st.input.Focus())
	case TextArea:
		cmds = append(cmds, st.area.Focus())
	}
	m.previewFor = "\x00" // paksa preview dirender ulang
	return tea.Batch(cmds...)
}

func newTextInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.Placeholder = placeholder
	return ti
}

func confirmOptions(q Question) []Option {
	yes := Option{Label: i18n.AskYes, Value: ValueYes}
	no := Option{Label: i18n.AskNo, Value: ValueNo}
	if len(q.Options) == 2 {
		yes.Label, yes.Description, yes.Recommended = q.Options[0].Label, q.Options[0].Description, q.Options[0].Recommended
		no.Label, no.Description, no.Recommended = q.Options[1].Label, q.Options[1].Description, q.Options[1].Recommended
	}
	return []Option{yes, no}
}

func withOther(opts []Option, other bool) []Option {
	if !other {
		return opts
	}
	return append(opts, Option{Label: i18n.AskOther, Value: otherValue, Description: i18n.AskOtherDesc})
}

// applyDefault menaruh kursor dan pilihan sesuai Default atau jawaban yang sudah ada.
func (m *Model) applyDefault(q Question, st *qstate) {
	vals := q.Default
	if a, ok := m.answers[q.ID]; ok {
		vals = a.Values
	}
	if q.Kind == Multi {
		for _, v := range vals {
			st.selected[v] = true
		}
		return
	}
	if len(vals) == 0 {
		return
	}
	for i, o := range st.opts {
		if o.Value == vals[0] {
			st.cursor = i
			return
		}
	}
	// Default yang tidak cocok dengan opsi mana pun dianggap isian "Lainnya…".
	if q.Other {
		st.other.SetValue(vals[0])
		st.cursor = len(st.opts) - 1
	}
}

func (m *Model) loadCmd(i int, q Question) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		opts, err := q.Load(ctx)
		return loadedMsg{owner: m, index: i, opts: opts, err: err}
	}
}

func (m *Model) applyLoaded(msg loadedMsg) {
	q := m.form.Questions[msg.index]
	st := m.states[msg.index]
	st.loading = false
	st.loadErr = msg.err
	if msg.err != nil {
		return
	}
	st.opts = withOther(sortRecommended(msg.opts), q.Other)
	m.applyDefault(q, st)
}

// filtered mengembalikan indeks opsi yang lolos filter.
func (st *qstate) filtered() []int {
	f := st.filter.Value()
	out := make([]int, 0, len(st.opts))
	for i, o := range st.opts {
		if o.Value == otherValue || matchesFilter(o, f) {
			out = append(out, i)
		}
	}
	return out
}

// current mengembalikan opsi di bawah kursor.
func (st *qstate) current() (Option, bool) {
	idx := st.filtered()
	if st.cursor < 0 || st.cursor >= len(idx) {
		return Option{}, false
	}
	return st.opts[idx[st.cursor]], true
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	s := msg.String()
	if s == "ctrl+c" {
		return nil // ditangani root
	}
	if m.confirmCancel {
		switch s {
		case "y", "Y":
			return m.finish(true)
		case "n", "N", "esc":
			m.confirmCancel = false
		}
		return nil
	}
	if m.inReview {
		return m.handleReviewKey(s)
	}

	q := m.question()
	st := m.state()

	if st.otherActive {
		return m.handleOtherKey(msg, q, st)
	}
	if st.filtering {
		return m.handleFilterKey(msg, q, st)
	}

	switch q.Kind {
	case Text, TextArea:
		return m.handleTextKey(msg, q, st)
	}

	// Single, Multi, Confirm
	switch s {
	case "esc", "shift+tab", "left", "h":
		return m.back()
	case "up", "k":
		m.moveCursor(st, -1)
	case "down", "j":
		m.moveCursor(st, 1)
	case "home", "g":
		st.cursor = 0
	case "end", "G":
		st.cursor = max(len(st.filtered())-1, 0)
	case "pgup":
		m.preview.PageUp()
	case "pgdown":
		m.preview.PageDown()
	case "/":
		if len(st.opts) > 9 {
			st.filtering = true
			return st.filter.Focus()
		}
	case "space":
		if q.Kind == Multi {
			if o, ok := st.current(); ok {
				return m.toggle(q, st, o)
			}
		}
	case "a":
		if q.Kind == Multi {
			m.toggleAll(st)
		}
	case "y", "Y":
		if q.Kind == Confirm {
			return m.chooseSingle(q, st, st.opts[0])
		}
	case "n", "N":
		if q.Kind == Confirm {
			return m.chooseSingle(q, st, st.opts[1])
		}
	case "enter", "tab", "right", "l":
		if st.loading || st.loadErr != nil {
			return nil
		}
		if q.Kind == Multi {
			return m.submitMulti(q, st)
		}
		if o, ok := st.current(); ok {
			return m.chooseSingle(q, st, o)
		}
		st.err = errors.New(i18n.AskErrNoOptions)
	default:
		if n, ok := digit(s); ok {
			idx := st.filtered()
			if n >= 1 && n <= 9 && n <= len(idx) {
				st.cursor = n - 1
				o := st.opts[idx[n-1]]
				if q.Kind == Multi {
					return m.toggle(q, st, o)
				}
				return m.chooseSingle(q, st, o)
			}
		}
	}
	return nil
}

func digit(s string) (int, bool) {
	if len(s) == 1 && s[0] >= '0' && s[0] <= '9' {
		return int(s[0] - '0'), true
	}
	return 0, false
}

func (m *Model) moveCursor(st *qstate, d int) {
	n := len(st.filtered())
	if n == 0 {
		return
	}
	st.cursor = (st.cursor + d + n) % n
	st.err = nil
}

func (m *Model) chooseSingle(q Question, st *qstate, o Option) tea.Cmd {
	if o.Disabled != "" {
		st.err = fmt.Errorf(i18n.AskErrDisabled, o.Disabled)
		return nil
	}
	if o.Value == otherValue {
		st.otherActive = true
		st.err = nil
		return st.other.Focus()
	}
	st.err = nil
	m.answers[q.ID] = Answer{Values: []string{o.Value}}
	return m.advance()
}

func (m *Model) toggle(q Question, st *qstate, o Option) tea.Cmd {
	if o.Disabled != "" {
		st.err = fmt.Errorf(i18n.AskErrDisabled, o.Disabled)
		return nil
	}
	st.err = nil
	if o.Value == otherValue {
		if st.selected[otherValue] {
			delete(st.selected, otherValue)
			return nil
		}
		st.otherActive = true
		return st.other.Focus()
	}
	if st.selected[o.Value] {
		delete(st.selected, o.Value)
	} else {
		st.selected[o.Value] = true
	}
	return nil
}

func (m *Model) toggleAll(st *qstate) {
	all := true
	for _, o := range st.opts {
		if o.Disabled == "" && o.Value != otherValue && !st.selected[o.Value] {
			all = false
			break
		}
	}
	for _, o := range st.opts {
		if o.Disabled != "" || o.Value == otherValue {
			continue
		}
		if all {
			delete(st.selected, o.Value)
		} else {
			st.selected[o.Value] = true
		}
	}
	st.err = nil
}

// selectedOptions mengembalikan opsi yang dicentang sesuai urutan tampil.
func (st *qstate) selectedOptions() []Option {
	var out []Option
	for _, o := range st.opts {
		if st.selected[o.Value] && o.Value != otherValue {
			out = append(out, o)
		}
	}
	return out
}

func (m *Model) submitMulti(q Question, st *qstate) tea.Cmd {
	sel := st.selectedOptions()
	vals := make([]string, 0, len(sel))
	for _, o := range sel {
		vals = append(vals, o.Value)
	}
	other := ""
	if st.selected[otherValue] {
		other = strings.TrimSpace(st.other.Value())
	}
	count := len(vals)
	if other != "" {
		count++
	}
	minSel := q.Min
	if minSel == 0 && !q.Optional {
		minSel = 1
	}
	if count < minSel {
		st.err = fmt.Errorf(i18n.AskErrMinSelect, minSel)
		return nil
	}
	if q.Max > 0 && count > q.Max {
		st.err = fmt.Errorf(i18n.AskErrMaxSelect, q.Max)
		return nil
	}
	st.err = nil
	m.answers[q.ID] = Answer{Values: vals, Other: other}
	return m.advance()
}

func (m *Model) handleOtherKey(msg tea.KeyPressMsg, q Question, st *qstate) tea.Cmd {
	switch msg.String() {
	case "esc":
		st.otherActive = false
		st.other.Blur()
		st.err = nil
		return nil
	case "enter", "tab":
		v := strings.TrimSpace(st.other.Value())
		if v == "" {
			st.err = errors.New(i18n.AskErrRequired)
			return nil
		}
		if !m.passesValidation(q, st, v) {
			return nil
		}
		st.otherActive = false
		st.other.Blur()
		if q.Kind == Multi {
			st.selected[otherValue] = true
			return nil
		}
		m.answers[q.ID] = Answer{Other: v}
		return m.advance()
	}
	var cmd tea.Cmd
	st.other, cmd = st.other.Update(msg)
	m.liveValidate(q, st, st.other.Value())
	return cmd
}

func (m *Model) handleFilterKey(msg tea.KeyPressMsg, q Question, st *qstate) tea.Cmd {
	switch msg.String() {
	case "esc":
		st.filtering = false
		st.filter.SetValue("")
		st.filter.Blur()
		st.cursor = 0
		return nil
	case "up":
		m.moveCursor(st, -1)
		return nil
	case "down":
		m.moveCursor(st, 1)
		return nil
	case "enter":
		st.filtering = false
		st.filter.Blur()
		if o, ok := st.current(); ok {
			if q.Kind == Multi {
				return m.toggle(q, st, o)
			}
			return m.chooseSingle(q, st, o)
		}
		return nil
	}
	var cmd tea.Cmd
	st.filter, cmd = st.filter.Update(msg)
	st.cursor = 0
	return cmd
}

func (m *Model) handleTextKey(msg tea.KeyPressMsg, q Question, st *qstate) tea.Cmd {
	s := msg.String()
	switch s {
	case "esc", "shift+tab":
		return m.back()
	case "tab":
		return m.submitText(q, st)
	case "enter":
		if q.Kind == Text {
			return m.submitText(q, st)
		}
	}
	var cmd tea.Cmd
	if q.Kind == Text {
		st.input, cmd = st.input.Update(msg)
		m.liveValidate(q, st, st.input.Value())
	} else {
		st.area, cmd = st.area.Update(msg)
		m.liveValidate(q, st, st.area.Value())
	}
	return cmd
}

func (m *Model) submitText(q Question, st *qstate) tea.Cmd {
	v := st.input.Value()
	if q.Kind == TextArea {
		v = st.area.Value()
	}
	if strings.TrimSpace(v) == "" {
		if !q.Optional {
			st.err = errors.New(i18n.AskErrRequired)
			return nil
		}
		st.err = nil
		m.answers[q.ID] = Answer{}
		return m.advance()
	}
	if !m.passesValidation(q, st, v) {
		return nil
	}
	m.answers[q.ID] = Answer{Text: v}
	return m.advance()
}

// passesValidation menjalankan Validate. Peringatan tidak memblokir, tetapi bila belum pernah
// terlihat user, ditampilkan dulu dan butuh satu kali konfirmasi (enter) lagi.
func (m *Model) passesValidation(q Question, st *qstate, v string) bool {
	if q.Validate == nil {
		st.err = nil
		return true
	}
	err := q.Validate(v)
	if err == nil {
		st.err = nil
		return true
	}
	if isWarning(err) {
		seen := st.err != nil && st.err.Error() == err.Error()
		st.err = err
		return seen
	}
	st.err = err
	return false
}

func (m *Model) liveValidate(q Question, st *qstate, v string) {
	if strings.TrimSpace(v) == "" || q.Validate == nil {
		st.err = nil
		return
	}
	st.err = q.Validate(v)
}

// advance pindah ke pertanyaan berikutnya, ke ringkasan, atau selesai.
func (m *Model) advance() tea.Cmd {
	m.blurCurrent()
	vis := m.visible()
	if m.fromReview {
		for _, i := range vis {
			if _, ok := m.answers[m.form.Questions[i].ID]; !ok {
				return m.activate(i)
			}
		}
		m.fromReview = false
		return m.toReview()
	}
	for _, i := range vis {
		if i > m.pos {
			return m.activate(i)
		}
	}
	if m.reviewEnabled() {
		return m.toReview()
	}
	return m.finish(false)
}

func (m *Model) toReview() tea.Cmd {
	m.inReview = true
	m.reviewCursor = len(m.visible()) // baris "Lanjut"
	return nil
}

// back mundur satu langkah; di pertanyaan pertama berarti batal.
func (m *Model) back() tea.Cmd {
	m.blurCurrent()
	vis := m.visible()
	prev := -1
	for _, i := range vis {
		if i < m.pos {
			prev = i
		}
	}
	if prev >= 0 {
		return m.activate(prev)
	}
	if len(m.answers) > 0 {
		m.confirmCancel = true
		return nil
	}
	return m.finish(true)
}

func (m *Model) blurCurrent() {
	if m.pos >= len(m.states) {
		return
	}
	st := m.state()
	st.input.Blur()
	st.area.Blur()
	st.other.Blur()
	st.filter.Blur()
	st.otherActive = false
	st.filtering = false
}

func (m *Model) handleReviewKey(s string) tea.Cmd {
	vis := m.visible()
	switch s {
	case "up", "k":
		m.reviewCursor = (m.reviewCursor + len(vis)) % (len(vis) + 1)
	case "down", "j":
		m.reviewCursor = (m.reviewCursor + 1) % (len(vis) + 1)
	case "esc", "shift+tab", "left":
		m.inReview = false
		return m.activate(vis[len(vis)-1])
	case "enter", "right":
		if m.reviewCursor >= len(vis) {
			return m.finish(false)
		}
		m.fromReview = true
		return m.activate(vis[m.reviewCursor])
	default:
		if n, ok := digit(s); ok && n >= 1 && n <= len(vis) {
			m.fromReview = true
			return m.activate(vis[n-1])
		}
	}
	return nil
}

// visibleAnswers membuang jawaban untuk pertanyaan yang kini tersembunyi oleh When.
func (m *Model) visibleAnswers() Answers {
	out := Answers{}
	for _, i := range m.visible() {
		id := m.form.Questions[i].ID
		if a, ok := m.answers[id]; ok {
			out[id] = a
		}
	}
	return out
}

func (m *Model) finish(cancelled bool) tea.Cmd {
	m.done = true
	m.cancel()
	res := Result{Form: m.form.Title, Cancelled: cancelled}
	if !cancelled {
		res.Answers = m.visibleAnswers()
	}
	return nav.Pop(res)
}

// updateInputs meneruskan pesan non-tombol (blink kursor, paste) ke input yang aktif.
func (m *Model) updateInputs(msg tea.Msg) tea.Cmd {
	if m.done || m.inReview || m.pos >= len(m.states) {
		return nil
	}
	st := m.state()
	if !st.ready {
		return nil
	}
	var cmd tea.Cmd
	switch {
	case st.otherActive:
		st.other, cmd = st.other.Update(msg)
	case st.filtering:
		st.filter, cmd = st.filter.Update(msg)
	case m.question().Kind == Text:
		st.input, cmd = st.input.Update(msg)
	case m.question().Kind == TextArea:
		st.area, cmd = st.area.Update(msg)
	}
	return cmd
}

// Keys mengembalikan tombol kontekstual untuk footer.
func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	if m.confirmCancel {
		return []key.Binding{b("y", i18n.AskYes), b("n", i18n.AskNo)}
	}
	if m.inReview {
		return []key.Binding{b("↑↓", i18n.KeyMove), b("enter", i18n.KeySelect), b("esc", i18n.KeyBack)}
	}
	q := m.question()
	st := m.state()
	switch {
	case st.otherActive:
		return []key.Binding{b("enter", i18n.AskKeyNext), b("esc", i18n.AskKeyCancelEdit)}
	case st.filtering:
		return []key.Binding{b("↑↓", i18n.KeyMove), b("enter", i18n.KeySelect), b("esc", i18n.AskKeyCancelEdit)}
	}
	switch q.Kind {
	case Text:
		return []key.Binding{b("enter", i18n.AskKeyNext), b("shift+tab", i18n.AskKeyPrev), b("esc", i18n.KeyBack)}
	case TextArea:
		return []key.Binding{b("enter", i18n.AskKeyNewline), b("tab", i18n.AskKeyDone), b("esc", i18n.KeyBack)}
	}
	n := min(len(st.filtered()), 9)
	keys := []key.Binding{b("↑↓", i18n.KeyMove)}
	if n > 0 {
		keys = append(keys, b(fmt.Sprintf("1-%d", n), i18n.AskKeyNumbers))
	}
	if q.Kind == Multi {
		keys = append(keys, b("space", i18n.AskKeyToggle), b("a", i18n.AskKeyAll))
	}
	if q.Kind == Confirm {
		keys = append(keys, b("y/n", i18n.KeySelect))
	}
	keys = append(keys, b("enter", i18n.AskKeyNext))
	if len(st.opts) > 9 {
		keys = append(keys, b("/", i18n.AskKeyFilter))
	}
	if m.hasPreview(st) {
		keys = append(keys, b("pgup/pgdn", i18n.AskKeyScrollPrev))
	}
	keys = append(keys, b("esc", i18n.KeyBack))
	if q.Help != "" {
		keys = append(keys, b("?", i18n.KeyHelp))
	}
	return keys
}
