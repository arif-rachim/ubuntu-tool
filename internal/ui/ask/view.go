package ask

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

const (
	sideBySideWidth = 100 // lebar minimum layout preview berdampingan
	maxMetaColumn   = 80  // kolom terjauh untuk info rata kanan (Option.Meta)
)

// View merender body layar.
func (m *Model) View(width, height int) string {
	if m.width != width || m.height != height {
		m.setSize(width, height)
	}
	t := ui.Current
	var top []string

	if chips := m.renderChips(width); chips != "" {
		top = append(top, chips, "")
	}
	if m.confirmCancel {
		top = append(top, " "+t.Warning.Render("⚠ "+i18n.AskCancelConfirm), "")
	}
	if m.inReview {
		return ui.FitHeight(strings.Join(append(top, m.renderReview(width)...), "\n"), width, height)
	}

	q := m.question()
	st := m.state()

	prompt := t.Title.Render(q.Prompt)
	if q.Kind == Multi {
		prompt += " " + t.Muted.Render(i18n.AskMultiHint)
	}
	top = append(top, ui.Wrap(prompt, width, " "))
	// Saat mengetik, ? masuk sebagai huruf; jadi penjelasan pertanyaan teks ditampilkan langsung.
	if (q.Kind == Text || q.Kind == TextArea) && q.Help != "" {
		top = append(top, ui.Wrap(t.Subtle.Render(q.Help), width, " "))
	}
	top = append(top, "")

	var bottom []string
	if st.err != nil {
		bottom = append(bottom, "", m.renderErr(st.err, width))
	}
	if q.Kind == Multi && q.Summary != nil {
		bottom = append(bottom, "", " "+t.Subtle.Render(q.Summary(st.selectedOptions())))
	}

	topStr := strings.Join(top, "\n")
	bottomStr := strings.Join(bottom, "\n")
	avail := height - lipgloss.Height(topStr) - lipgloss.Height(bottomStr)
	if bottomStr == "" {
		avail = height - lipgloss.Height(topStr)
	}

	var body string
	switch q.Kind {
	case Text:
		body = "   " + st.input.View()
	case TextArea:
		body = indentLines(st.area.View(), "   ")
	default:
		body = m.renderOptionsArea(q, st, width, max(avail, 3))
	}

	out := topStr + "\n" + body
	if bottomStr != "" {
		out += "\n" + bottomStr
	}
	return ui.FitHeight(out, width, height)
}

func indentLines(s, indent string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderErr(err error, width int) string {
	t := ui.Current
	if isWarning(err) {
		return ui.Wrap(t.Warning.Render("⚠ "+err.Error()), width, "   ")
	}
	return ui.Wrap(t.Danger.Render("✗ "+err.Error()), width, "   ")
}

// renderChips menampilkan posisi dan status semua pertanyaan.
func (m *Model) renderChips(width int) string {
	vis := m.visible()
	if len(vis) <= 1 {
		return ""
	}
	t := ui.Current
	parts := make([]string, 0, len(vis)+1)
	for _, i := range vis {
		q := m.form.Questions[i]
		label := q.Header
		if label == "" {
			label = q.ID
		}
		_, answered := m.answers[q.ID]
		switch {
		case !m.inReview && i == m.pos:
			parts = append(parts, t.ChipOn.Render("● "+label))
		case answered:
			parts = append(parts, t.Success.Render("✓")+" "+t.Subtle.Render(label))
		default:
			parts = append(parts, t.Chip.Render("○ "+label))
		}
	}
	if m.reviewEnabled() {
		if m.inReview {
			parts = append(parts, t.ChipOn.Render("● "+i18n.AskReview))
		} else {
			parts = append(parts, t.Chip.Render("○ "+i18n.AskReview))
		}
	}
	line := " " + strings.Join(parts, "   ")
	if lipgloss.Width(line) > width {
		// Terlalu sempit: tampilkan hanya posisi.
		pos := 0
		for n, i := range vis {
			if i == m.pos {
				pos = n + 1
			}
		}
		if m.inReview {
			return " " + t.ChipOn.Render(i18n.AskReview)
		}
		return fmt.Sprintf(" %s %s", t.ChipOn.Render(fmt.Sprintf("%d/%d", pos, len(vis))), t.Subtle.Render(m.question().Header))
	}
	return line
}

func (m *Model) hasPreview(st *qstate) bool {
	for _, o := range st.opts {
		if o.Preview != "" {
			return true
		}
	}
	return false
}

// renderOptionsArea menggabungkan daftar opsi dengan panel preview bila ada.
func (m *Model) renderOptionsArea(q Question, st *qstate, width, height int) string {
	t := ui.Current
	if st.loading {
		return "   " + t.Subtle.Render(i18n.Loading)
	}
	if st.loadErr != nil {
		return ui.ErrorState(fmt.Errorf(i18n.AskLoadFailed, st.loadErr), "", width)
	}

	var filterLine string
	if st.filtering || st.filter.Value() != "" {
		filterLine = "   " + st.filter.View()
		height--
	}

	if !m.hasPreview(st) {
		list := m.renderOptions(q, st, width, height)
		return joinNonEmpty(filterLine, list)
	}

	cur, _ := st.current()
	if cur.Preview != m.previewFor {
		m.previewFor = cur.Preview
		m.preview.SetContent(cur.Preview)
		m.preview.GotoTop()
	}

	if width >= sideBySideWidth {
		listWidth := width * 45 / 100
		boxWidth := width - listWidth - 1
		list := m.renderOptions(q, st, listWidth, height)
		box := m.renderPreviewBox(cur, boxWidth, min(height, previewLines(st)+2))
		return joinNonEmpty(filterLine, lipgloss.JoinHorizontal(lipgloss.Top, list, " ", box))
	}

	boxHeight := min(max(height/2, 5), 12, previewLines(st)+2)
	list := m.renderOptions(q, st, width, max(height-boxHeight-1, 3))
	box := m.renderPreviewBox(cur, width-2, boxHeight)
	return joinNonEmpty(filterLine, list+"\n\n"+indentLines(box, " "))
}

// previewLines mengembalikan jumlah baris preview terpanjang, supaya tinggi kotak tidak
// berubah-ubah saat kursor berpindah antar opsi.
func previewLines(st *qstate) int {
	n := 1
	for _, o := range st.opts {
		n = max(n, strings.Count(o.Preview, "\n")+1)
	}
	return n
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	return a + "\n" + b
}

func (m *Model) renderPreviewBox(o Option, width, height int) string {
	t := ui.Current
	innerW := max(width-4, 10) // border + padding kiri-kanan
	innerH := max(height-2, 1)
	m.preview.SetWidth(innerW)
	m.preview.SetHeight(innerH)
	content := m.preview.View()
	if o.Preview == "" {
		content = t.Muted.Render("—")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		BorderForeground(t.BorderColor()).
		Width(width).
		Height(height).
		Render(content)
}

// renderOptions merender daftar opsi dengan scroll di sekitar kursor.
func (m *Model) renderOptions(q Question, st *qstate, width, height int) string {
	t := ui.Current
	idx := st.filtered()
	if len(idx) == 0 {
		return "   " + t.Muted.Render(i18n.AskNoMatch)
	}

	answered, hasAnswer := m.answers[q.ID]
	blocks := make([][]string, len(idx))
	for n, i := range idx {
		o := st.opts[i]
		chosen := false
		switch {
		case q.Kind == Multi:
			chosen = st.selected[o.Value]
		case hasAnswer && o.Value == otherValue:
			chosen = answered.Other != ""
		case hasAnswer:
			chosen = answered.Has(o.Value)
		}
		blocks[n] = m.renderOption(q, st, o, n, n == st.cursor, chosen, width)
	}

	// Pilih jendela blok yang memuat kursor.
	start, end := st.cursor, st.cursor+1
	used := len(blocks[st.cursor])
	for {
		grew := false
		if end < len(blocks) && used+len(blocks[end])+2 <= height {
			used += len(blocks[end])
			end++
			grew = true
		}
		if start > 0 && used+len(blocks[start-1])+2 <= height {
			start--
			used += len(blocks[start])
			grew = true
		}
		if !grew {
			break
		}
	}

	var lines []string
	if start > 0 {
		lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf(i18n.AskMoreAbove, start)))
	}
	for _, b := range blocks[start:end] {
		lines = append(lines, b...)
	}
	if end < len(blocks) {
		lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf(i18n.AskMoreBelow, len(blocks)-end)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderOption(q Question, st *qstate, o Option, n int, focused, chosen bool, width int) []string {
	t := ui.Current

	cursor := "   "
	if focused {
		cursor = " " + t.Selected.Render("❯") + " "
	}
	check := ""
	if q.Kind == Multi {
		if chosen {
			check = t.Success.Render("[✓]") + " "
		} else {
			check = t.Muted.Render("[ ]") + " "
		}
	}
	num := "   "
	if n < 9 {
		num = fmt.Sprintf("%d. ", n+1)
	}
	prefix := cursor + check + num
	indent := strings.Repeat(" ", lipgloss.Width(prefix))

	labelStyle := lipgloss.NewStyle()
	switch {
	case o.Disabled != "":
		labelStyle = t.Muted
	case focused:
		labelStyle = t.Selected
	}

	var label string
	if o.Value == otherValue && st.otherActive {
		label = st.other.View()
	} else {
		label = labelStyle.Render(o.Label)
		if o.Value == otherValue && st.other.Value() != "" && chosen {
			label += t.Subtle.Render(": " + st.other.Value())
		}
		if o.Recommended {
			label += " " + t.Accent.Render(i18n.AskRecommended)
		}
		if marker := t.RiskMarker(o.Risk); marker != "" {
			label += " " + marker
		}
		if chosen && q.Kind != Multi {
			label += " " + t.Success.Render("✓")
		}
	}

	line := prefix + label
	if o.Meta != "" {
		meta := t.Subtle.Render(o.Meta)
		// Batasi kolom meta supaya tidak terlempar jauh di terminal yang sangat lebar.
		gap := min(width, maxMetaColumn) - lipgloss.Width(line) - lipgloss.Width(meta) - 1
		if gap >= 2 {
			line += strings.Repeat(" ", gap) + meta
		} else {
			line += "  " + meta
		}
	}
	lines := []string{line}

	desc := o.Description
	descStyle := t.Subtle
	if o.Disabled != "" {
		desc = strings.TrimSpace(desc + " — " + o.Disabled)
		descStyle = t.Muted
	}
	if desc != "" {
		lines = append(lines, strings.Split(ui.Wrap(descStyle.Render(desc), width, indent), "\n")...)
	}
	return lines
}

// renderReview menampilkan ringkasan semua jawaban.
func (m *Model) renderReview(width int) []string {
	t := ui.Current
	vis := m.visible()
	labelW := 0
	for _, i := range vis {
		labelW = max(labelW, lipgloss.Width(headerOf(m.form.Questions[i])))
	}

	lines := []string{" " + t.Title.Render(i18n.AskReview), ""}
	for n, i := range vis {
		q := m.form.Questions[i]
		cursor := "   "
		style := t.Subtle
		if m.reviewCursor == n {
			cursor = " " + t.Selected.Render("❯") + " "
			style = t.Selected
		}
		label := style.Render(padRight(headerOf(q), labelW))
		value := m.describeAnswer(q)
		lines = append(lines, fmt.Sprintf("%s%d. %s   %s", cursor, n+1, label, value))
	}
	lines = append(lines, "")
	cursor := "   "
	submit := t.Title.Render(i18n.AskReviewSubmit)
	if m.reviewCursor >= len(vis) {
		cursor = " " + t.Selected.Render("❯") + " "
		submit = t.Selected.Render(i18n.AskReviewSubmit)
	}
	lines = append(lines, cursor+submit, "", " "+t.Muted.Render(i18n.AskReviewEditKey))
	return lines
}

func headerOf(q Question) string {
	if q.Header != "" {
		return q.Header
	}
	return q.ID
}

func padRight(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}

// describeAnswer mengubah jawaban menjadi teks yang mudah dibaca.
func (m *Model) describeAnswer(q Question) string {
	t := ui.Current
	a, ok := m.answers[q.ID]
	if !ok {
		return t.Muted.Render(i18n.AskEmptyAnswer)
	}
	st := m.states[indexOf(m.form.Questions, q.ID)]
	labelOf := func(v string) string {
		for _, o := range st.opts {
			if o.Value == v {
				return o.Label
			}
		}
		return v
	}
	switch q.Kind {
	case Text:
		if a.Text == "" {
			return t.Muted.Render(i18n.AskEmptyAnswer)
		}
		return a.Text
	case TextArea:
		if a.Text == "" {
			return t.Muted.Render(i18n.AskEmptyAnswer)
		}
		lines := strings.Split(strings.TrimRight(a.Text, "\n"), "\n")
		first := lines[0]
		if len(lines) > 1 {
			first += " " + t.Muted.Render(fmt.Sprintf(i18n.AskLines, len(lines)))
		}
		return first
	}
	var parts []string
	for _, v := range a.Values {
		parts = append(parts, labelOf(v))
	}
	if a.Other != "" {
		parts = append(parts, a.Other)
	}
	if len(parts) == 0 {
		return t.Muted.Render(i18n.AskEmptyAnswer)
	}
	return strings.Join(parts, ", ")
}

func indexOf(qs []Question, id string) int {
	for i, q := range qs {
		if q.ID == id {
			return i
		}
	}
	return 0
}
