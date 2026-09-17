package checklist

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
)

func step(title string, st check.Status, next string, ran *[]string) check.Step {
	return check.Step{Title: title, Equivalent: "cmd " + title, Run: func(context.Context) check.Result {
		*ran = append(*ran, title)
		return check.Result{Status: st, Summary: title + " hasil", Explain: "arti " + title, Next: next}
	}}
}

func pumpAll(m *Model, cmd tea.Cmd) {
	for i := 0; cmd != nil && i < 20; i++ {
		var next tea.Cmd
		for _, msg := range testutil.Run(cmd) {
			if _, ok := msg.(tickMsg); ok {
				continue
			}
			_, c := m.Update(msg)
			if c != nil {
				next = c
			}
		}
		cmd = next
	}
}

func TestBerhentiDiLangkahGagal(t *testing.T) {
	var ran []string
	m := New("Cek", "pengantar", []check.Step{
		step("satu", check.OK, "", &ran),
		step("dua", check.Warn, "", &ran),
		step("tiga", check.Fail, "firewall", &ran),
		step("empat", check.OK, "", &ran),
	})
	opened := ""
	m.OnNext = func(id string) tea.Cmd { opened = id; return nil }
	pumpAll(m, m.Init())
	if strings.Join(ran, ",") != "satu,dua,tiga" || !m.Done() {
		t.Fatalf("langkah dijalankan: %v done=%v", ran, m.Done())
	}
	view := ansi.Strip(m.View(100, 40))
	for _, s := range []string{"✓ 1. satu", "⚠ 2. dua", "✗ 3. tiga", "arti tiga", "$ cmd tiga", "Penyebab ditemukan di langkah 3", "Tekan enter"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	if strings.Contains(view, "tiga hasil\n     arti satu") {
		t.Error("penjelasan langkah OK tidak perlu ditampilkan")
	}
	m.Update(testutil.Key("enter"))
	if opened != "firewall" {
		t.Errorf("enter harus membuka modul saran, dapat %q", opened)
	}

	ran = nil
	pumpAll(m, m.Init())
	if strings.Join(ran, ",") != "satu,dua,tiga" {
		t.Errorf("r/Init harus memeriksa ulang dari awal: %v", ran)
	}
}

func TestSemuaLolos(t *testing.T) {
	var ran []string
	m := New("Cek", "", []check.Step{step("a", check.OK, "", &ran), step("b", check.Skip, "", &ran)})
	pumpAll(m, m.Init())
	if !strings.Contains(ansi.Strip(m.View(80, 20)), "Semua langkah lolos") {
		t.Error("ringkasan sukses tidak tampil")
	}
}

func TestIndependenMenjalankanSemua(t *testing.T) {
	var ran []string
	m := New("Sehat", "", []check.Step{
		step("satu", check.Fail, "disk", &ran),
		step("dua", check.OK, "", &ran),
		step("tiga", check.Warn, "packages", &ran),
	})
	m.Independent = true
	m.ModuleName = func(id string) string { return "modul " + id }
	opened := ""
	m.OnNext = func(id string) tea.Cmd { opened = id; return nil }
	pumpAll(m, m.Init())
	if strings.Join(ran, ",") != "satu,dua,tiga" || !m.Done() {
		t.Fatalf("semua langkah harus dijalankan: %v", ran)
	}
	view := ansi.Strip(m.View(100, 40))
	for _, s := range []string{"1 masalah, 1 catatan", "tekan 1 untuk membuka modul disk", "tekan 3 untuk membuka modul packages"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	m.Update(testutil.Key("3"))
	if opened != "packages" {
		t.Errorf("angka 3 harus membuka packages, dapat %q", opened)
	}
	m.Update(testutil.Key("2"))
	if opened != "packages" {
		t.Error("langkah OK tidak membuka modul")
	}
	m.Update(testutil.Key("enter"))
	if opened != "disk" {
		t.Errorf("enter membuka temuan paling parah, dapat %q", opened)
	}
}
