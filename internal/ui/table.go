package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Column adalah satu kolom Table.
type Column struct {
	Title string
	Width int  // lebar minimum
	Flex  int  // bobot pembagian sisa lebar (0 = tetap)
	Right bool // rata kanan (angka)
}

// Table adalah tabel sederhana dengan kursor dan scroll. Sel berisi teks polos;
// warna per sel diatur lewat CellStyle.
type Table struct {
	Columns   []Column
	Rows      [][]string
	Cursor    int
	offset    int
	CellStyle func(row, col int) lipgloss.Style // opsional
}

// SetRows mengganti isi tabel dan menjaga kursor tetap valid.
func (t *Table) SetRows(rows [][]string) {
	t.Rows = rows
	t.Cursor = min(t.Cursor, max(len(rows)-1, 0))
}

// Move menggeser kursor sebanyak d baris.
func (t *Table) Move(d int) {
	if len(t.Rows) == 0 {
		return
	}
	t.Cursor = min(max(t.Cursor+d, 0), len(t.Rows)-1)
}

// HandleKey menangani tombol navigasi umum. Mengembalikan true bila tombol dipakai.
func (t *Table) HandleKey(k string) bool {
	switch k {
	case "up", "k":
		t.Move(-1)
	case "down", "j":
		t.Move(1)
	case "pgup":
		t.Move(-10)
	case "pgdown":
		t.Move(10)
	case "home", "g":
		t.Cursor = 0
	case "end", "G":
		t.Cursor = max(len(t.Rows)-1, 0)
	default:
		return false
	}
	return true
}

func (t *Table) widths(width int) []int {
	ws := make([]int, len(t.Columns))
	used, flex := 0, 0
	for i, c := range t.Columns {
		ws[i] = max(c.Width, lipgloss.Width(c.Title))
		used += ws[i] + 2
		flex += c.Flex
	}
	rest := width - used - 2
	if rest > 0 && flex > 0 {
		for i, c := range t.Columns {
			ws[i] += rest * c.Flex / flex
		}
	}
	return ws
}

// View merender tabel setinggi height (termasuk baris judul).
func (t *Table) View(width, height int) string {
	th := Current
	ws := t.widths(width)

	cell := func(s string, w int, right bool) string {
		s = ansi.Truncate(s, w, "…")
		pad := strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
		if right {
			return pad + s
		}
		return s + pad
	}

	var header strings.Builder
	header.WriteString("   ")
	for i, c := range t.Columns {
		header.WriteString(cell(c.Title, ws[i], c.Right) + "  ")
	}
	lines := []string{th.Subtle.Bold(true).Render(strings.TrimRight(header.String(), " "))}

	bodyHeight := max(height-1, 1)
	if len(t.Rows) > bodyHeight {
		if t.Cursor < t.offset {
			t.offset = t.Cursor
		}
		if t.Cursor >= t.offset+bodyHeight {
			t.offset = t.Cursor - bodyHeight + 1
		}
		t.offset = min(t.offset, len(t.Rows)-bodyHeight)
	} else {
		t.offset = 0
	}

	end := min(t.offset+bodyHeight, len(t.Rows))
	for r := t.offset; r < end; r++ {
		var b strings.Builder
		focused := r == t.Cursor
		if focused {
			b.WriteString(" " + th.Selected.Render("❯") + " ")
		} else {
			b.WriteString("   ")
		}
		for c := range t.Columns {
			v := ""
			if c < len(t.Rows[r]) {
				v = t.Rows[r][c]
			}
			style := lipgloss.NewStyle()
			if t.CellStyle != nil {
				style = t.CellStyle(r, c)
			}
			if focused {
				style = style.Bold(true)
			}
			b.WriteString(style.Render(cell(v, ws[c], t.Columns[c].Right)) + "  ")
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	if len(t.Rows) > bodyHeight {
		pos := fmt.Sprintf(" %d/%d ", t.Cursor+1, len(t.Rows))
		last := len(lines) - 1
		lines[last] = ansi.Truncate(lines[last], max(width-len(pos)-1, 0), "") + th.Muted.Render(pos)
	}
	return strings.Join(lines, "\n")
}

// Pair adalah satu baris panel detail.
type Pair struct {
	Key   string
	Value string
	Hint  string // penjelasan kecil di bawah nilai (opsional)
}

// Detail merender panel key/value dengan kunci rata kiri dan nilai yang di-wrap.
func Detail(pairs []Pair, width int) string {
	th := Current
	keyW := 0
	for _, p := range pairs {
		keyW = max(keyW, lipgloss.Width(p.Key))
	}
	indent := strings.Repeat(" ", keyW+4)
	var lines []string
	for _, p := range pairs {
		if p.Key == "" && p.Value == "" {
			lines = append(lines, "")
			continue
		}
		val := p.Value
		if val == "" {
			val = th.Muted.Render("—")
		}
		wrapped := strings.Split(Wrap(val, width, indent), "\n")
		key := th.Subtle.Render(p.Key + strings.Repeat(" ", keyW-lipgloss.Width(p.Key)))
		lines = append(lines, " "+key+"   "+strings.TrimPrefix(wrapped[0], indent))
		lines = append(lines, wrapped[1:]...)
		if p.Hint != "" {
			lines = append(lines, strings.Split(Wrap(th.Muted.Render(p.Hint), width, indent), "\n")...)
		}
	}
	return strings.Join(lines, "\n")
}

// Section merender judul bagian kecil.
func Section(title string) string { return " " + Current.Accent.Bold(true).Render(title) }
