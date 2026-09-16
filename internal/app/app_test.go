package app

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
)

// fakeScreen mencatat pesan yang diterima.
type fakeScreen struct {
	title   string
	typing  bool
	back    bool
	got     []tea.Msg
	resumed any
}

func (f *fakeScreen) Init() tea.Cmd { return nil }
func (f *fakeScreen) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	f.got = append(f.got, msg)
	if r, ok := msg.(nav.ResumedMsg); ok {
		f.resumed = r.Result
	}
	return f, nil
}
func (f *fakeScreen) View(w, h int) string { return "isi " + f.title }
func (f *fakeScreen) Title() string        { return f.title }
func (f *fakeScreen) Keys() []key.Binding  { return nil }
func (f *fakeScreen) Typing() bool         { return f.typing }
func (f *fakeScreen) HandlesBack() bool    { return f.back }

func keyPress(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func send(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func TestQDiteruskanSaatMengetik(t *testing.T) {
	s := &fakeScreen{title: "form", typing: true}
	m := New(s)
	m, cmd := send(m, keyPress("q"))
	if isQuit(cmd) {
		t.Fatal("q tidak boleh keluar saat layar sedang mengetik")
	}
	if len(s.got) != 1 {
		t.Fatalf("q harus diteruskan ke layar, diterima %d pesan", len(s.got))
	}
	for _, k := range []string{"r", "?"} {
		m, _ = send(m, keyPress(k))
	}
	if m.showHelp || len(s.got) != 3 {
		t.Fatal("r dan ? harus diteruskan saat mengetik")
	}
	if _, cmd := send(m, keyPress("ctrl+c")); !isQuit(cmd) {
		t.Fatal("ctrl+c harus selalu keluar")
	}
}

func TestQKeluarSaatTidakMengetik(t *testing.T) {
	m := New(&fakeScreen{title: "menu"})
	if _, cmd := send(m, keyPress("q")); !isQuit(cmd) {
		t.Fatal("q harus keluar")
	}
}

func TestEscPopDanResultSampai(t *testing.T) {
	bottom := &fakeScreen{title: "bawah"}
	top := &fakeScreen{title: "atas"}
	m := New(bottom)
	m, _ = send(m, nav.PushMsg{Screen: top})
	if len(m.stack) != 2 {
		t.Fatal("push gagal")
	}

	m, cmd := send(m, keyPress("esc"))
	m, _ = send(m, cmd())
	if len(m.stack) != 1 {
		t.Fatal("esc harus pop layar yang tidak menangani esc sendiri")
	}

	m, _ = send(m, nav.PushMsg{Screen: top})
	m, _ = send(m, nav.PopMsg{Result: "hasil"})
	if bottom.resumed != "hasil" {
		t.Fatalf("ResumedMsg harus membawa hasil, dapat %v", bottom.resumed)
	}
}

func TestEscKeBackHandler(t *testing.T) {
	bottom := &fakeScreen{title: "bawah"}
	top := &fakeScreen{title: "form", back: true}
	m := New(bottom)
	m, _ = send(m, nav.PushMsg{Screen: top})
	m, cmd := send(m, keyPress("esc"))
	if cmd != nil || len(m.stack) != 2 || len(top.got) == 0 {
		t.Fatal("esc harus diteruskan ke layar yang menangani esc sendiri")
	}
}

func TestPopDiDasarKeluar(t *testing.T) {
	m := New(&fakeScreen{title: "menu"})
	if _, cmd := send(m, nav.PopMsg{}); !isQuit(cmd) {
		t.Fatal("pop layar terakhir harus keluar")
	}
}

func TestSizeKeSemuaLayarDanBreadcrumb(t *testing.T) {
	bottom := &fakeScreen{title: "Menu utama"}
	top := &fakeScreen{title: "Ports"}
	m := New(bottom)
	m, _ = send(m, nav.PushMsg{Screen: top})
	m, _ = send(m, tea.WindowSizeMsg{Width: 90, Height: 20})

	for _, s := range []*fakeScreen{bottom, top} {
		last := s.got[len(s.got)-1]
		size, ok := last.(nav.SizeMsg)
		if !ok || size.Width != 90 || size.Height != 16 {
			t.Fatalf("%s: SizeMsg salah: %#v", s.title, last)
		}
	}

	out := ansi.Strip(m.View().Content)
	if !strings.Contains(out, "Menu utama › Ports") {
		t.Fatalf("breadcrumb tidak ada:\n%s", out)
	}
	if lines := strings.Count(out, "\n") + 1; lines != 20 {
		t.Fatalf("frame harus setinggi terminal (20), dapat %d", lines)
	}
}

func TestHelpOverlay(t *testing.T) {
	m := New(&fakeScreen{title: "menu"})
	m, _ = send(m, keyPress("?"))
	if !m.showHelp || !strings.Contains(ansi.Strip(m.View().Content), "Tombol umum") {
		t.Fatal("? harus membuka layar bantuan")
	}
	m, _ = send(m, keyPress("x"))
	if m.showHelp {
		t.Fatal("tombol apa pun harus menutup bantuan")
	}
}

type busyScreen struct {
	fakeScreen
	busy bool
}

func (b *busyScreen) Busy() bool { return b.busy }

// Update mengembalikan dirinya sendiri (bukan fakeScreen yang di-embed) supaya tetap Busy di tumpukan.
func (b *busyScreen) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	b.fakeScreen.Update(msg)
	return b, nil
}

func TestBusyMenerimaCtrlC(t *testing.T) {
	s := &busyScreen{fakeScreen: fakeScreen{title: "jalan"}, busy: true}
	m := New(s)
	for _, k := range []string{"ctrl+c", "q", "esc"} {
		if _, cmd := send(m, keyPress(k)); isQuit(cmd) {
			t.Fatalf("%s tidak boleh keluar saat layar Busy", k)
		}
	}
	if len(s.got) != 3 {
		t.Fatalf("semua tombol harus diteruskan ke layar Busy, diterima %d", len(s.got))
	}
	s.busy = false
	if _, cmd := send(m, keyPress("ctrl+c")); !isQuit(cmd) {
		t.Fatal("setelah selesai, ctrl+c harus keluar")
	}
}
