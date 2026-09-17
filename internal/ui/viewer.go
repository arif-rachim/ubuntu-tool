package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Viewer menampilkan baris teks panjang dengan scroll. Follow membuat tampilan menempel di baris
// terakhir saat baris baru ditambahkan (seperti tail -f), sampai user menggulir ke atas.
type Viewer struct {
	Lines  []string
	Cursor int // baris terpilih (untuk detail); -1 bila tidak dipakai
	Follow bool
	offset int
	height int
}

// SetLines mengganti isi; bila Follow, kursor pindah ke baris terakhir.
func (v *Viewer) SetLines(lines []string) {
	v.Lines = lines
	if v.Follow || v.Cursor >= len(lines) {
		v.Cursor = len(lines) - 1
	}
}

// Append menambah baris dan membuang yang lama bila melebihi limit.
func (v *Viewer) Append(limit int, lines ...string) {
	v.Lines = append(v.Lines, lines...)
	if limit > 0 && len(v.Lines) > limit {
		drop := len(v.Lines) - limit
		v.Lines = v.Lines[drop:]
		v.Cursor = max(v.Cursor-drop, 0)
	}
	if v.Follow {
		v.Cursor = len(v.Lines) - 1
	}
}

// HandleKey menangani navigasi. Menggulir ke atas mematikan Follow; End menyalakannya lagi.
func (v *Viewer) HandleKey(k string) bool {
	last := len(v.Lines) - 1
	page := max(v.height-1, 1)
	switch k {
	case "up", "k":
		v.Cursor = max(v.Cursor-1, 0)
	case "down", "j":
		v.Cursor = min(v.Cursor+1, last)
	case "pgup":
		v.Cursor = max(v.Cursor-page, 0)
	case "pgdown", "space":
		v.Cursor = min(v.Cursor+page, last)
	case "home", "g":
		v.Cursor = 0
	case "end", "G":
		v.Cursor = last
		v.Follow = true
		return true
	default:
		return false
	}
	v.Follow = v.Cursor == last
	return true
}

// View merender jendela baris yang memuat kursor. highlight dipakai untuk baris terpilih.
func (v *Viewer) View(width, height int, highlight bool) string {
	v.height = height
	t := Current
	if len(v.Lines) == 0 {
		return ""
	}
	v.Cursor = min(max(v.Cursor, 0), len(v.Lines)-1)
	if v.Cursor < v.offset {
		v.offset = v.Cursor
	}
	if v.Cursor >= v.offset+height {
		v.offset = v.Cursor - height + 1
	}
	v.offset = max(min(v.offset, len(v.Lines)-height), 0)
	end := min(v.offset+height, len(v.Lines))
	out := make([]string, 0, height)
	for i := v.offset; i < end; i++ {
		line := ansi.Truncate(v.Lines[i], width-3, "…")
		if highlight && i == v.Cursor {
			out = append(out, " "+t.Selected.Render("❯")+" "+line)
		} else {
			out = append(out, "   "+line)
		}
	}
	if len(v.Lines) > height {
		pos := fmt.Sprintf(" %d/%d ", v.Cursor+1, len(v.Lines))
		last := len(out) - 1
		out[last] = ansi.Truncate(out[last], max(width-len(pos)-1, 0), "") + strings.Repeat(" ", max(width-len(pos)-ansi.StringWidth(ansi.Truncate(out[last], max(width-len(pos)-1, 0), "")), 0)) + t.Muted.Render(pos)
	}
	return strings.Join(out, "\n")
}
