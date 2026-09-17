// Package ui berisi tema dan komponen tampilan bersama. Paket ini tidak tahu soal domain (port, ufw, dll).
package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

// Theme adalah kumpulan style yang dipakai semua layar.
type Theme struct {
	IsDark bool

	Title    lipgloss.Style
	Subtle   lipgloss.Style
	Muted    lipgloss.Style
	Accent   lipgloss.Style
	Selected lipgloss.Style
	Success  lipgloss.Style
	Warning  lipgloss.Style
	Danger   lipgloss.Style
	Code     lipgloss.Style
	Badge    lipgloss.Style
	BadgeHot lipgloss.Style
	KeyName  lipgloss.Style
	KeyDesc  lipgloss.Style
	Border   lipgloss.Style
	Chip     lipgloss.Style
	ChipOn   lipgloss.Style
}

// Current adalah tema aktif. Diubah oleh root model saat warna latar terminal diketahui;
// semua rendering terjadi di goroutine event loop yang sama, jadi aman tanpa lock.
var Current = NewTheme(true)

// SetDark mengganti tema aktif sesuai warna latar terminal.
func SetDark(isDark bool) { Current = NewTheme(isDark) }

// NewTheme membuat tema untuk latar gelap atau terang.
func NewTheme(isDark bool) Theme {
	ld := lipgloss.LightDark(isDark)
	var (
		fg      = ld(lipgloss.Color("#1F2328"), lipgloss.Color("#E6EDF3"))
		subtle  = ld(lipgloss.Color("#57606A"), lipgloss.Color("#9DA7B3"))
		muted   = ld(lipgloss.Color("#8C959F"), lipgloss.Color("#6E7781"))
		accent  = ld(lipgloss.Color("#0969DA"), lipgloss.Color("#58A6FF"))
		success = ld(lipgloss.Color("#1A7F37"), lipgloss.Color("#3FB950"))
		warning = ld(lipgloss.Color("#9A6700"), lipgloss.Color("#D29922"))
		danger  = ld(lipgloss.Color("#CF222E"), lipgloss.Color("#F85149"))
		codeBg  = ld(lipgloss.Color("#EFF2F5"), lipgloss.Color("#262C36"))
		border  = ld(lipgloss.Color("#D0D7DE"), lipgloss.Color("#3D444D"))
	)
	return Theme{
		IsDark:   isDark,
		Title:    lipgloss.NewStyle().Foreground(fg).Bold(true),
		Subtle:   lipgloss.NewStyle().Foreground(subtle),
		Muted:    lipgloss.NewStyle().Foreground(muted),
		Accent:   lipgloss.NewStyle().Foreground(accent),
		Selected: lipgloss.NewStyle().Foreground(accent).Bold(true),
		Success:  lipgloss.NewStyle().Foreground(success),
		Warning:  lipgloss.NewStyle().Foreground(warning),
		Danger:   lipgloss.NewStyle().Foreground(danger).Bold(true),
		Code:     lipgloss.NewStyle().Foreground(fg).Background(codeBg),
		Badge:    lipgloss.NewStyle().Foreground(subtle).Padding(0, 1).Border(lipgloss.HiddenBorder(), false),
		BadgeHot: lipgloss.NewStyle().Foreground(danger).Bold(true).Padding(0, 1),
		KeyName:  lipgloss.NewStyle().Foreground(fg).Bold(true),
		KeyDesc:  lipgloss.NewStyle().Foreground(muted),
		Border:   lipgloss.NewStyle().Foreground(border),
		Chip:     lipgloss.NewStyle().Foreground(muted),
		ChipOn:   lipgloss.NewStyle().Foreground(accent).Bold(true),
	}
}

// RiskStyle mengembalikan style untuk tingkat bahaya.
func (t Theme) RiskStyle(l risk.Level) lipgloss.Style {
	switch l {
	case risk.Caution:
		return t.Warning
	case risk.Dangerous:
		return t.Danger
	default:
		return t.Success
	}
}

// RiskMarker mengembalikan penanda teks untuk tingkat bahaya, supaya informasi tidak hanya lewat warna.
func (t Theme) RiskMarker(l risk.Level) string {
	switch l {
	case risk.Caution:
		return t.Warning.Render("⚠")
	case risk.Dangerous:
		return t.Danger.Render("⚠")
	default:
		return ""
	}
}

// BorderColor dipakai komponen yang menggambar border sendiri.
func (t Theme) BorderColor() color.Color { return t.Border.GetForeground() }
