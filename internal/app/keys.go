package app

import (
	"charm.land/bubbles/v2/key"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
)

type globalKeys struct {
	back      key.Binding
	quit      key.Binding
	forceQuit key.Binding
	help      key.Binding
	refresh   key.Binding
}

func newGlobalKeys() globalKeys {
	return globalKeys{
		back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", i18n.KeyBack)),
		quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", i18n.KeyQuit)),
		forceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", i18n.KeyQuit)),
		help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", i18n.KeyHelp)),
		refresh:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", i18n.KeyRefresh)),
	}
}

// footer mengembalikan tombol global yang ditampilkan di footer. Saat user sedang mengetik,
// q dan ? masuk sebagai huruf, jadi hanya ctrl+c yang ditampilkan.
func (k globalKeys) footer(canGoBack, typing bool) []key.Binding {
	if typing {
		return []key.Binding{k.forceQuit}
	}
	keys := []key.Binding{k.help, k.quit}
	if canGoBack {
		keys = append([]key.Binding{k.back}, keys...)
	}
	return keys
}

func (k globalKeys) all() []key.Binding {
	return []key.Binding{k.back, k.help, k.refresh, k.quit, k.forceQuit}
}
