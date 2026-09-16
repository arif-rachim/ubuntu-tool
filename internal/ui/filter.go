package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// Filter adalah kotak filter teks yang dibuka dengan "/" dan ditutup dengan enter/esc.
type Filter struct {
	input  textinput.Model
	typing bool
}

// NewFilter membuat filter dengan placeholder.
func NewFilter(placeholder string) Filter {
	in := textinput.New()
	in.Prompt = "Filter: "
	in.Placeholder = placeholder
	return Filter{input: in}
}

// Typing melaporkan apakah user sedang mengetik di filter.
func (f *Filter) Typing() bool { return f.typing }

// Active melaporkan apakah filter terlihat (sedang diketik atau berisi).
func (f *Filter) Active() bool { return f.typing || f.input.Value() != "" }

// Query adalah isi filter dalam huruf kecil.
func (f *Filter) Query() string { return strings.ToLower(strings.TrimSpace(f.input.Value())) }

// Match melaporkan apakah teks cocok dengan filter.
func (f *Filter) Match(text string) bool {
	q := f.Query()
	return q == "" || strings.Contains(strings.ToLower(text), q)
}

// Update menangani tombol dan paste. handled bernilai true bila pesan dipakai filter;
// changed bernilai true bila isi filter berubah (daftar perlu disaring ulang).
func (f *Filter) Update(msg tea.Msg) (handled, changed bool, cmd tea.Cmd) {
	switch msg := msg.(type) {
	case tea.PasteMsg:
		if f.typing {
			f.input, cmd = f.input.Update(msg)
			return true, true, cmd
		}
	case tea.KeyPressMsg:
		k := msg.String()
		if !f.typing {
			switch k {
			case "/":
				f.typing = true
				return true, false, f.input.Focus()
			case "esc":
				if f.input.Value() != "" {
					f.input.SetValue("")
					return true, true, nil
				}
			}
			return false, false, nil
		}
		switch k {
		case "esc":
			f.typing = false
			f.input.SetValue("")
			f.input.Blur()
			return true, true, nil
		case "enter":
			f.typing = false
			f.input.Blur()
			return true, false, nil
		case "up", "down", "pgup", "pgdown":
			return false, false, nil // biarkan daftar di bawahnya bergerak
		}
		before := f.input.Value()
		f.input, cmd = f.input.Update(msg)
		return true, before != f.input.Value(), cmd
	}
	return false, false, nil
}

// SetWidth mengatur lebar input.
func (f *Filter) SetWidth(w int) { f.input.SetWidth(w) }

// View merender filter (kosong bila tidak aktif).
func (f *Filter) View() string {
	if !f.Active() {
		return ""
	}
	return " " + f.input.View()
}
