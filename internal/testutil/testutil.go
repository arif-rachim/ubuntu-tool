// Package testutil berisi helper test yang dipakai banyak layar.
package testutil

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Key membuat KeyPressMsg dari nama tombol seperti yang dikembalikan KeyPressMsg.String().
func Key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// Type mengubah string menjadi rangkaian ketikan.
func Type(s string) []tea.Msg {
	var out []tea.Msg
	for _, r := range s {
		out = append(out, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return out
}

// Run menjalankan cmd dengan batas waktu dan mengembalikan pesannya (BatchMsg diratakan).
// Command yang tidak selesai dalam batas waktu (mis. tick) diabaikan.
func Run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if b, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, Run(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(500 * time.Millisecond):
		return nil
	}
}
