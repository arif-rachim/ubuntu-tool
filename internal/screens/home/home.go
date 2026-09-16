// Package home berisi layar menu utama.
package home

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// Item adalah satu entri menu.
type Item struct {
	Label string
	Desc  string
	Phase int // fase di docs/PLAN.md tempat modul ini dibangun
	Open  func() nav.Screen
}

// Group adalah kelompok entri menu.
type Group struct {
	Icon  string
	Title string
	Items []Item
}

// Groups adalah isi menu utama sesuai docs/PLAN.md.
func Groups() []Group {
	return []Group{
		{"🩺", i18n.GroupDiagnose, []Item{
			{Label: "Diagnosa berdasarkan gejala", Desc: "Disk penuh, server lambat, web tidak bisa diakses, service mati terus, dan lainnya", Phase: 16},
		}},
		{"💻", i18n.GroupSystem, []Item{
			{Label: "Resource", Desc: "CPU, RAM, load, dan proses yang paling berat", Phase: 5},
			{Label: "Disk & Storage", Desc: "Apa yang makan tempat, inode, swap, bersih-bersih terpandu", Phase: 6},
			{Label: "Log", Desc: "Error sejak boot, log per service, ikuti log secara langsung", Phase: 7},
			{Label: "Service (systemd)", Desc: "Start, stop, restart, enable service dan lihat lognya", Phase: 8},
			{Label: "Penjadwalan", Desc: "Cron dan systemd timer: lihat, jelaskan, dan buat jadwal", Phase: 11},
			{Label: "Paket (apt)", Desc: "Cari, install, update keamanan, dan perbaiki paket rusak", Phase: 10},
		}},
		{"🌐", i18n.GroupNetwork, []Item{
			{Label: "Network & konektivitas", Desc: "IP, DNS, route, dan wizard \"kenapa tidak bisa konek\"", Phase: 9},
			{Label: "Ports & Proses", Desc: "Port mana dipakai proses apa, dan hentikan dengan aman", Phase: 4},
			{Label: "Firewall (ufw)", Desc: "Buka/tutup port dengan pengaman supaya SSH tidak terputus", Phase: 13},
			{Label: "Web & TLS", Desc: "Reverse proxy nginx, HTTPS Let's Encrypt, cek sertifikat", Phase: 14},
		}},
		{"🔑", i18n.GroupAccess, []Item{
			{Label: "User & SSH", Desc: "Tambah user, sudo, SSH key, dan amankan server SSH", Phase: 12},
		}},
		{"📦", i18n.GroupContainer, []Item{
			{Label: "Docker", Desc: "Image, container, shell interaktif, dan wizard compose", Phase: 15},
		}},
		{"📜", i18n.GroupHistory, []Item{
			{Label: "Riwayat perintah", Desc: "Command yang pernah dijalankan ubt, dan ekspor jadi script", Phase: 17},
		}},
	}
}

// Model adalah layar menu utama.
type Model struct {
	groups []Group
	flat   []Item
	cursor int
	keys   []key.Binding
}

// New membuat layar menu utama.
func New(groups []Group) *Model {
	m := &Model{groups: groups}
	for _, g := range groups {
		m.flat = append(m.flat, g.Items...)
	}
	m.keys = []key.Binding{
		key.NewBinding(key.WithKeys("up", "k", "down", "j"), key.WithHelp("↑↓", i18n.KeyMove)),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", i18n.KeyOpen)),
	}
	return m
}

func (m *Model) Init() tea.Cmd       { return nil }
func (m *Model) Title() string       { return i18n.HomeTitle }
func (m *Model) Keys() []key.Binding { return m.keys }

// Update menangani navigasi menu.
func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok || len(m.flat) == 0 {
		return m, nil
	}
	switch k.String() {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(m.flat)) % len(m.flat)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(m.flat)
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.flat) - 1
	case "enter", "right", "l":
		it := m.flat[m.cursor]
		if it.Open != nil {
			return m, nav.Push(it.Open())
		}
		return m, nav.Push(newPlaceholder(it))
	}
	return m, nil
}

// View merender menu berkelompok dengan penjelasan item yang sedang dipilih.
func (m *Model) View(width, height int) string {
	t := ui.Current
	var lines []string
	lines = append(lines, "", ui.Wrap(t.Subtle.Render(i18n.HomeIntro), width, " "), "")

	cursorLine := 0
	n := 0
	for _, g := range m.groups {
		lines = append(lines, " "+g.Icon+" "+t.Title.Render(g.Title))
		for _, it := range g.Items {
			if n == m.cursor {
				cursorLine = len(lines)
				lines = append(lines, "   "+t.Selected.Render("❯ "+it.Label))
			} else {
				lines = append(lines, "     "+it.Label)
			}
			n++
		}
		lines = append(lines, "")
	}

	desc := ui.Wrap(t.Subtle.Render(m.flat[m.cursor].Desc), width, "   ")
	descLines := strings.Split(desc, "\n")
	listHeight := max(height-len(descLines)-1, 3)

	// Geser daftar supaya kursor selalu terlihat.
	offset := 0
	if cursorLine >= listHeight {
		offset = cursorLine - listHeight + 2
	}
	end := min(offset+listHeight, len(lines))
	visible := lines[offset:end]
	for len(visible) < listHeight {
		visible = append(visible, "")
	}
	return strings.Join(visible, "\n") + "\n" + t.Border.Render(" "+strings.Repeat("─", max(width-2, 0))) + "\n" + desc
}

// placeholder adalah layar untuk modul yang belum dibangun.
type placeholder struct{ item Item }

func newPlaceholder(it Item) *placeholder { return &placeholder{item: it} }

func (p *placeholder) Init() tea.Cmd                        { return nil }
func (p *placeholder) Update(tea.Msg) (nav.Screen, tea.Cmd) { return p, nil }
func (p *placeholder) Title() string                        { return p.item.Label }
func (p *placeholder) Keys() []key.Binding                  { return nil }
func (p *placeholder) View(width, _ int) string {
	body := p.item.Desc + ".\n\n" + i18n.PlaceholderBody + " " + fmt.Sprintf(i18n.PlaceholderPlan, p.item.Phase)
	return ui.EmptyState(p.item.Label, body, "", width)
}
