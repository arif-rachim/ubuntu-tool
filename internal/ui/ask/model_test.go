package ask

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
)

// keyOf membuat KeyPressMsg dari nama tombol.
func keyOf(s string) tea.KeyPressMsg {
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
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

// press mengirim rangkaian tombol dan mengembalikan command terakhir.
func press(m *Model, keys ...string) tea.Cmd {
	var last tea.Cmd
	for _, k := range keys {
		_, last = m.Update(keyOf(k))
	}
	return last
}

// typeText mengetik string huruf per huruf.
func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// collect menjalankan cmd (termasuk batch) dan mengumpulkan pesan yang relevan untuk test.
// Command yang lama (mis. kedip kursor) diabaikan setelah batas waktu.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case msg := <-ch:
		if batch, ok := msg.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range batch {
				out = append(out, collect(c)...)
			}
			return out
		}
		return []tea.Msg{msg}
	case <-time.After(300 * time.Millisecond):
		return nil
	}
}

func popResult(t *testing.T, cmd tea.Cmd) Result {
	t.Helper()
	for _, msg := range collect(cmd) {
		if p, ok := msg.(nav.PopMsg); ok {
			r, ok := p.Result.(Result)
			if !ok {
				t.Fatalf("PopMsg.Result bukan ask.Result: %#v", p.Result)
			}
			return r
		}
	}
	t.Fatalf("form tidak selesai (tidak ada PopMsg)")
	return Result{}
}

func newStarted(f Form) *Model {
	m := New(f)
	for _, msg := range collect(m.Init()) {
		m.Update(msg)
	}
	return m
}

func single(id string, opts ...Option) Question {
	return Question{ID: id, Header: id, Prompt: id + "?", Kind: Single, Options: opts}
}

func TestRecommendedNaikDanAngkaMemilih(t *testing.T) {
	m := newStarted(Form{Questions: []Question{single("q",
		Option{Label: "A", Value: "a"},
		Option{Label: "B", Value: "b", Recommended: true},
		Option{Label: "C", Value: "c"},
	)}})

	if got := m.state().opts[0].Value; got != "b" {
		t.Fatalf("opsi pertama %q, ingin b (Recommended)", got)
	}
	r := popResult(t, press(m, "3"))
	if r.Cancelled || r.Answers["q"].Value() != "c" {
		t.Fatalf("hasil %+v, ingin q=c", r)
	}
}

func TestOpsiDisabledTidakBisaDipilih(t *testing.T) {
	m := newStarted(Form{Questions: []Question{single("q",
		Option{Label: "A", Value: "a", Disabled: "sudah dipakai"},
		Option{Label: "B", Value: "b"},
	)}})

	press(m, "1")
	if _, ok := m.answers["q"]; ok {
		t.Fatal("opsi disabled ikut terpilih")
	}
	if m.state().err == nil || !strings.Contains(m.state().err.Error(), "sudah dipakai") {
		t.Fatalf("error tidak menyebut alasan: %v", m.state().err)
	}
	press(m, "enter") // kursor masih di opsi disabled
	if _, ok := m.answers["q"]; ok {
		t.Fatal("enter pada opsi disabled ikut memilih")
	}
}

func TestLainnyaMengaktifkanTypingDanValidasi(t *testing.T) {
	q := single("port", Option{Label: "3000", Value: "3000"})
	q.Other = true
	q.Validate = func(s string) error {
		if s != "9090" {
			return errors.New("port salah")
		}
		return nil
	}
	m := newStarted(Form{Questions: []Question{q}})

	press(m, "2") // "Lainnya…"
	if !m.Typing() {
		t.Fatal("memilih Lainnya… harus membuat Typing() true")
	}
	typeText(m, "q12")
	if got := m.state().other.Value(); got != "q12" {
		t.Fatalf("huruf q harus masuk ke input, dapat %q", got)
	}
	press(m, "enter")
	if _, ok := m.answers["port"]; ok {
		t.Fatal("input tidak valid tetap diterima")
	}

	m.state().other.SetValue("")
	typeText(m, "9090")
	r := popResult(t, press(m, "enter"))
	if r.Answers["port"].Other != "9090" || r.Answers["port"].Value() != "9090" {
		t.Fatalf("jawaban Lainnya… salah: %+v", r.Answers["port"])
	}
}

func TestTextValidasiDanPeringatan(t *testing.T) {
	m := newStarted(Form{Questions: []Question{{
		ID: "domain", Kind: Text,
		Validate: func(s string) error {
			switch {
			case !strings.Contains(s, "."):
				return errors.New("tidak valid")
			case strings.HasSuffix(s, ".test"):
				return Warn("tidak bisa dapat sertifikat")
			}
			return nil
		},
	}}})

	press(m, "enter")
	if m.state().err == nil {
		t.Fatal("teks kosong wajib ditolak")
	}
	typeText(m, "abc")
	press(m, "enter")
	if m.done {
		t.Fatal("teks tidak valid tetap diterima")
	}

	typeText(m, ".test")
	if !isWarning(m.state().err) {
		t.Fatalf("validasi langsung saat mengetik harus memunculkan peringatan, dapat %v", m.state().err)
	}
	r := popResult(t, press(m, "enter")) // peringatan sudah terlihat → lanjut
	if r.Answers["domain"].Text != "abc.test" {
		t.Fatalf("jawaban %+v", r.Answers)
	}
}

func TestPeringatanBaruButuhEnterKedua(t *testing.T) {
	m := newStarted(Form{Questions: []Question{{
		ID: "x", Kind: Text, Default: []string{"a.test"},
		Validate: func(s string) error { return Warn("hati-hati") },
	}}})
	if cmd := press(m, "enter"); m.done {
		collect(cmd)
		t.Fatal("peringatan yang belum pernah terlihat harus ditampilkan dulu")
	}
	popResult(t, press(m, "enter"))
}

func TestMultiMinMaxToggleDanSemua(t *testing.T) {
	q := Question{ID: "m", Kind: Multi, Max: 2, Options: []Option{
		{Label: "A", Value: "a"},
		{Label: "B", Value: "b"},
		{Label: "C", Value: "c", Disabled: "tidak ada"},
		{Label: "D", Value: "d"},
	}}
	m := newStarted(Form{Questions: []Question{q}})

	press(m, "enter")
	if m.state().err == nil {
		t.Fatal("Multi tanpa pilihan harus ditolak (minimal 1 bila tidak Optional)")
	}

	press(m, "a")
	st := m.state()
	if !st.selected["a"] || !st.selected["b"] || st.selected["c"] || !st.selected["d"] {
		t.Fatalf("a harus mencentang semua kecuali disabled: %v", st.selected)
	}
	press(m, "enter")
	if m.done || m.state().err == nil {
		t.Fatal("3 pilihan melebihi Max 2 harus ditolak")
	}

	press(m, "1") // lepas A
	r := popResult(t, press(m, "enter"))
	if !reflect.DeepEqual(r.Answers["m"].Values, []string{"b", "d"}) {
		t.Fatalf("nilai %v, ingin [b d]", r.Answers["m"].Values)
	}
}

func TestMultiSpaceMencentangKursor(t *testing.T) {
	m := newStarted(Form{Questions: []Question{{ID: "m", Kind: Multi, Options: []Option{
		{Label: "A", Value: "a"}, {Label: "B", Value: "b"},
	}}}})
	press(m, "down", "space")
	r := popResult(t, press(m, "enter"))
	if !reflect.DeepEqual(r.Answers["m"].Values, []string{"b"}) {
		t.Fatalf("nilai %v", r.Answers["m"].Values)
	}
}

func TestWhenMelewatiDanMembuangJawabanTersembunyi(t *testing.T) {
	f := Form{SkipReview: true, Questions: []Question{
		{ID: "https", Kind: Confirm},
		{ID: "email", Kind: Text, When: func(a Answers) bool { return a["https"].Yes() }},
		single("akhir", Option{Label: "OK", Value: "ok"}),
	}}

	m := newStarted(f)
	press(m, "y")
	if m.form.Questions[m.pos].ID != "email" {
		t.Fatalf("setelah ya, harus ke email; sekarang %s", m.form.Questions[m.pos].ID)
	}
	typeText(m, "a@b.c")
	press(m, "enter")

	// Kembali ke https dan ganti jadi tidak: email harus dilewati dan jawabannya dibuang.
	press(m, "esc", "esc")
	if m.form.Questions[m.pos].ID != "https" {
		t.Fatalf("esc dua kali harus kembali ke https; sekarang %s", m.form.Questions[m.pos].ID)
	}
	press(m, "n")
	if m.form.Questions[m.pos].ID != "akhir" {
		t.Fatalf("setelah tidak, email harus dilewati; sekarang %s", m.form.Questions[m.pos].ID)
	}
	r := popResult(t, press(m, "1"))
	if _, ok := r.Answers["email"]; ok {
		t.Fatalf("jawaban pertanyaan tersembunyi tidak boleh ikut: %+v", r.Answers)
	}
	if r.Answers["https"].Yes() {
		t.Fatal("https harus tidak")
	}
}

func TestEscMundurTanpaMenghapusJawaban(t *testing.T) {
	f := Form{SkipReview: true, Questions: []Question{
		single("a", Option{Label: "X", Value: "x"}, Option{Label: "Y", Value: "y"}),
		{ID: "b", Kind: Text},
	}}
	m := newStarted(f)
	press(m, "2")
	typeText(m, "halo")
	press(m, "esc")
	if m.pos != 0 {
		t.Fatal("esc harus kembali ke pertanyaan pertama")
	}
	if m.answers["a"].Value() != "y" || m.state().cursor != 1 {
		t.Fatalf("jawaban & kursor pertanyaan pertama harus tetap: %+v cursor=%d", m.answers["a"], m.state().cursor)
	}
	press(m, "enter")
	if got := m.state().input.Value(); got != "halo" {
		t.Fatalf("teks yang sudah diketik hilang: %q", got)
	}
}

func TestBatalDiPertanyaanPertama(t *testing.T) {
	t.Run("tanpa jawaban langsung batal", func(t *testing.T) {
		m := newStarted(Form{Questions: []Question{single("a", Option{Label: "X", Value: "x"})}})
		if r := popResult(t, press(m, "esc")); !r.Cancelled {
			t.Fatal("harus Cancelled")
		}
	})
	t.Run("dengan jawaban minta konfirmasi", func(t *testing.T) {
		m := newStarted(Form{Questions: []Question{
			single("a", Option{Label: "X", Value: "x"}),
			single("b", Option{Label: "Y", Value: "y"}),
		}})
		press(m, "1", "esc", "esc")
		if !m.confirmCancel {
			t.Fatal("harus minta konfirmasi batal")
		}
		press(m, "n")
		if m.confirmCancel || m.done {
			t.Fatal("n harus membatalkan pembatalan")
		}
		press(m, "esc")
		if r := popResult(t, press(m, "y")); !r.Cancelled || r.Answers != nil {
			t.Fatalf("hasil %+v", r)
		}
	})
}

func TestRingkasanDanUbahJawaban(t *testing.T) {
	m := newStarted(Form{Questions: []Question{
		single("a", Option{Label: "X", Value: "x"}, Option{Label: "Y", Value: "y"}),
		{ID: "b", Kind: Text},
	}})
	press(m, "1")
	typeText(m, "halo")
	press(m, "enter")
	if !m.inReview {
		t.Fatal("setelah pertanyaan terakhir harus ke ringkasan")
	}
	if m.Typing() {
		t.Fatal("ringkasan tidak sedang mengetik")
	}

	press(m, "1") // ubah jawaban a
	if m.inReview || m.pos != 0 {
		t.Fatal("angka di ringkasan harus membuka pertanyaan itu")
	}
	press(m, "2")
	if !m.inReview {
		t.Fatal("setelah mengubah dari ringkasan harus kembali ke ringkasan")
	}
	r := popResult(t, press(m, "enter"))
	if r.Answers["a"].Value() != "y" || r.Answers["b"].Text != "halo" {
		t.Fatalf("hasil %+v", r.Answers)
	}
}

func TestConfirmTombolYN(t *testing.T) {
	m := newStarted(Form{Questions: []Question{{ID: "c", Kind: Confirm, Default: []string{ValueNo}}}})
	if m.state().cursor != 1 {
		t.Fatal("Default no harus menaruh kursor di Tidak")
	}
	r := popResult(t, press(m, "y"))
	if !r.Answers["c"].Yes() {
		t.Fatal("y harus menghasilkan ya")
	}
}

func TestLoadOpsiDinamis(t *testing.T) {
	q := Question{ID: "u", Kind: Single, Load: func(ctx context.Context) ([]Option, error) {
		return []Option{{Label: "root", Value: "root"}, {Label: "www-data", Value: "www-data", Recommended: true}}, nil
	}}
	m := New(Form{Questions: []Question{q}})
	cmd := m.Init()
	if !m.state().loading {
		t.Fatal("harus loading sebelum opsi datang")
	}
	press(m, "enter")
	if m.done {
		t.Fatal("enter saat loading tidak boleh menyelesaikan form")
	}
	for _, msg := range collect(cmd) {
		m.Update(msg)
	}
	r := popResult(t, press(m, "1"))
	if r.Answers["u"].Value() != "www-data" {
		t.Fatalf("hasil %+v", r.Answers)
	}
}

func TestLoadGagalTampilSebagaiError(t *testing.T) {
	m := New(Form{Questions: []Question{{ID: "u", Kind: Single, Load: func(context.Context) ([]Option, error) {
		return nil, errors.New("getent tidak ada")
	}}}})
	for _, msg := range collect(m.Init()) {
		m.Update(msg)
	}
	if m.state().loadErr == nil {
		t.Fatal("error Load harus disimpan")
	}
	if !strings.Contains(m.View(80, 20), "getent tidak ada") {
		t.Fatal("error Load harus tampil")
	}
}

func TestFilterUntukOpsiBanyak(t *testing.T) {
	var opts []Option
	for _, name := range []string{"apache2", "bash", "curl", "dnsutils", "git", "htop", "jq", "nginx", "openssl", "vim"} {
		opts = append(opts, Option{Label: name, Value: name})
	}
	m := newStarted(Form{Questions: []Question{{ID: "p", Kind: Single, Options: opts}}})
	press(m, "/")
	if !m.Typing() {
		t.Fatal("/ harus masuk mode filter")
	}
	typeText(m, "ngi")
	if n := len(m.state().filtered()); n != 1 {
		t.Fatalf("filter ngi harus menyisakan 1 opsi, dapat %d", n)
	}
	r := popResult(t, press(m, "enter"))
	if r.Answers["p"].Value() != "nginx" {
		t.Fatalf("hasil %+v", r.Answers)
	}
}

func TestSlashTidakFilterBilaOpsiSedikit(t *testing.T) {
	m := newStarted(Form{Questions: []Question{single("q", Option{Label: "A", Value: "a"})}})
	press(m, "/")
	if m.Typing() {
		t.Fatal("filter hanya aktif bila opsi lebih dari 9")
	}
}

func TestMultiDenganLainnya(t *testing.T) {
	m := newStarted(Form{Questions: []Question{{ID: "m", Kind: Multi, Other: true, Options: []Option{
		{Label: "A", Value: "a"},
	}}}})
	press(m, "2")
	if !m.Typing() {
		t.Fatal("memilih Lainnya… di Multi harus membuka input")
	}
	typeText(m, "tmp")
	press(m, "enter")
	if m.Typing() || !m.state().selected[otherValue] {
		t.Fatal("setelah enter, Lainnya… harus tercentang dan input ditutup")
	}
	press(m, "1")
	r := popResult(t, press(m, "enter"))
	if got := r.Answers["m"]; !reflect.DeepEqual(got.Values, []string{"a"}) || got.Other != "tmp" {
		t.Fatalf("hasil %+v", got)
	}

	// Mencentang ulang Lainnya… melepasnya.
	m = newStarted(Form{Questions: []Question{{ID: "m", Kind: Multi, Optional: true, Other: true}}})
	press(m, "1")
	typeText(m, "x")
	press(m, "enter", "1")
	if m.state().selected[otherValue] {
		t.Fatal("angka pada Lainnya… yang tercentang harus melepasnya")
	}
}
