package ui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Header berisi data baris atas setiap layar.
type Header struct {
	Breadcrumb []string
	UserHost   string
	IsRoot     bool
}

// HeaderHeight dan FooterHeight adalah tinggi tetap bagian atas & bawah frame.
const (
	HeaderHeight = 2 // baris header + garis pemisah
	FooterHeight = 2 // garis pemisah + baris keybinding
)

// Frame menyusun header, body, dan footer menjadi satu layar penuh.
func Frame(h Header, body string, keys []key.Binding, width, height int) string {
	t := Current
	bodyHeight := max(height-HeaderHeight-FooterHeight, 0)

	var b strings.Builder
	b.WriteString(renderHeader(h, width))
	b.WriteByte('\n')
	b.WriteString(t.Border.Render(strings.Repeat("─", max(width, 0))))
	b.WriteByte('\n')
	b.WriteString(FitHeight(body, width, bodyHeight))
	b.WriteByte('\n')
	b.WriteString(t.Border.Render(strings.Repeat("─", max(width, 0))))
	b.WriteByte('\n')
	b.WriteString(FooterKeys(keys, width))
	return b.String()
}

func renderHeader(h Header, width int) string {
	t := Current
	crumbs := make([]string, len(h.Breadcrumb))
	for i, c := range h.Breadcrumb {
		if i == len(h.Breadcrumb)-1 {
			crumbs[i] = t.Title.Render(c)
		} else {
			crumbs[i] = t.Subtle.Render(c)
		}
	}
	left := " " + t.Accent.Bold(true).Render("ubt") + t.Muted.Render(" › ") +
		strings.Join(crumbs, t.Muted.Render(" › "))

	badge := t.Subtle.Render("non-root")
	if h.IsRoot {
		badge = t.BadgeHot.Render("root")
	}
	right := t.Subtle.Render(h.UserHost) + "  " + badge + " "

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// FooterKeys merender daftar keybinding satu baris, dipotong rapi bila terlalu lebar.
func FooterKeys(keys []key.Binding, width int) string {
	t := Current
	sep := t.Muted.Render(" · ")
	var parts []string
	used := 1
	for _, k := range keys {
		if !k.Enabled() || k.Help().Key == "" {
			continue
		}
		part := t.KeyName.Render(k.Help().Key) + " " + t.KeyDesc.Render(k.Help().Desc)
		w := lipgloss.Width(part)
		if len(parts) > 0 {
			w += lipgloss.Width(sep)
		}
		if used+w > width {
			break
		}
		parts = append(parts, part)
		used += w
	}
	return " " + strings.Join(parts, sep)
}

// FitHeight memotong atau menambah baris kosong supaya s tepat setinggi height,
// dan memotong baris yang lebih lebar dari width.
func FitHeight(s string, width, height int) string {
	if height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		if lipgloss.Width(l) > width {
			lines[i] = ansi.Truncate(l, width, "…")
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// Wrap membungkus teks ke lebar tertentu dengan indentasi pada setiap baris.
func Wrap(s string, width int, indent string) string {
	w := max(width-lipgloss.Width(indent), 10)
	wrapped := ansi.Wrap(s, w, "")
	lines := strings.Split(wrapped, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}
