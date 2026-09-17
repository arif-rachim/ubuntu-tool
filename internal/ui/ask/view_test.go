package ask

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

var update = flag.Bool("update", false, "tulis ulang file golden")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden %s belum ada; jalankan `go test ./internal/ui/ask -update`: %v", path, err)
	}
	if string(want) != got {
		t.Errorf("render %s berubah.\n--- ingin ---\n%s\n--- dapat ---\n%s", name, want, got)
	}
}

// plain membuang kode warna dan spasi di ujung baris supaya golden stabil.
func plain(s string) string {
	lines := strings.Split(ansi.Strip(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.Join(lines, "\n")
}

func renderForm() *Model {
	return newStarted(Form{Title: "Demo", Questions: []Question{
		{
			ID: "jadwal", Header: "Jadwal", Prompt: "Mau pakai jadwal jenis apa?", Kind: Single, Other: true,
			Options: []Option{
				{Label: "cron", Value: "cron", Description: "Lebih sederhana dan umum, tapi PATH minim dan log harus diatur sendiri"},
				{Label: "systemd timer", Value: "timer", Recommended: true, Description: "Log masuk journal, tetap jalan walau server sempat mati saat jadwalnya"},
				{Label: "at", Value: "at", Disabled: "paket at belum terinstall", Description: "Sekali jalan"},
			},
		},
		{
			ID: "bersih", Header: "Bersihkan", Prompt: "Bersihkan apa saja?", Kind: Multi,
			Options: []Option{
				{Label: "Cache apt", Value: "apt", Meta: "~1,2 GB", Description: "Aman dihapus, bisa diunduh ulang"},
				{Label: "Image Docker tak terpakai", Value: "docker", Meta: "~4,0 GB", Risk: risk.Caution, Description: "Container yang berhenti ikut terhapus"},
			},
			Summary: func(sel []Option) string { return "Dipilih: " + string(rune('0'+len(sel))) },
		},
		{
			ID: "nginx", Header: "Template", Prompt: "Pilih template config nginx?", Kind: Single,
			Options: []Option{
				{Label: "Reverse proxy", Value: "proxy", Recommended: true, Preview: "server {\n    listen 80;\n    location / {\n        proxy_pass http://127.0.0.1:3000;\n    }\n}"},
				{Label: "Situs statis", Value: "static", Preview: "server {\n    root /var/www/app;\n}"},
			},
		},
	}})
}

func TestGoldenRender(t *testing.T) {
	for _, width := range []int{60, 120} {
		w := width
		name := func(s string) string { return s + "-" + map[int]string{60: "60", 120: "120"}[w] }

		m := renderForm()
		golden(t, name("single"), plain(m.View(w, 22)))

		press(m, "1")
		press(m, "space")
		golden(t, name("multi"), plain(m.View(w, 22)))

		press(m, "enter")
		golden(t, name("preview"), plain(m.View(w, 22)))

		press(m, "enter")
		golden(t, name("review"), plain(m.View(w, 22)))
	}
}

func TestRenderTidakMelebihiLebar(t *testing.T) {
	m := renderForm()
	for _, w := range []int{60, 80, 120} {
		for i, line := range strings.Split(m.View(w, 22), "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("lebar %d: baris %d selebar %d: %q", w, i, got, ansi.Strip(line))
			}
		}
	}
}
