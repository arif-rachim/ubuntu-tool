package ui

import (
	"fmt"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
)

// EmptyState menampilkan kondisi "tidak ada data" beserta penyebab dan saran, bukan layar kosong.
func EmptyState(title, cause, hint string, width int) string {
	t := Current
	var b strings.Builder
	b.WriteString("\n " + t.Title.Render(title) + "\n")
	if cause != "" {
		b.WriteString("\n" + Wrap(t.Subtle.Render(cause), width, " "))
		b.WriteByte('\n')
	}
	if hint != "" {
		b.WriteString("\n" + Wrap(t.Accent.Render(fmt.Sprintf(i18n.HintOf, hint)), width, " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// ErrorState menampilkan error yang dapat dipahami beserta saran perbaikan.
func ErrorState(err error, hint string, width int) string {
	t := Current
	var b strings.Builder
	b.WriteString("\n" + Wrap(t.Danger.Render("✗ "+fmt.Sprintf(i18n.ErrorOf, err)), width, " ") + "\n")
	if hint != "" {
		b.WriteString("\n" + Wrap(t.Accent.Render(fmt.Sprintf(i18n.HintOf, hint)), width, " ") + "\n")
	}
	return b.String()
}
