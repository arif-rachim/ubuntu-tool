// Package demo berisi layar tersembunyi `ubt --demo-ask` untuk mencoba semua jenis pertanyaan ui/ask.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// Model menampilkan hasil form demo dan bisa mengulanginya.
type Model struct {
	result  *ask.Result
	started bool
}

// New membuat layar demo.
func New() *Model { return &Model{} }

func (m *Model) Title() string { return i18n.DemoTitle }

func (m *Model) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "ulangi demo"))}
}

// Init langsung membuka form demo.
func (m *Model) Init() tea.Cmd {
	if m.started {
		return nil
	}
	m.started = true
	return nav.Push(ask.New(Form()))
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.ResumedMsg:
		if r, ok := msg.Result.(ask.Result); ok {
			m.result = &r
		}
	case tea.KeyPressMsg:
		if msg.String() == "enter" {
			return m, nav.Push(ask.New(Form()))
		}
	}
	return m, nil
}

func (m *Model) View(width, _ int) string {
	t := ui.Current
	if m.result == nil {
		return ""
	}
	if m.result.Cancelled {
		return ui.EmptyState(i18n.DemoResultTitle, i18n.DemoCancelled, "", width)
	}
	ids := make([]string, 0, len(m.result.Answers))
	for id := range m.result.Answers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("\n " + t.Title.Render(i18n.DemoResultTitle) + "\n\n")
	for _, id := range ids {
		a := m.result.Answers[id]
		fmt.Fprintf(&b, "   %s  values=%q other=%q text=%q\n", t.Accent.Render(id), a.Values, a.Other, a.Text)
	}
	return b.String()
}

// Form adalah form demo yang memakai semua jenis pertanyaan dan fitur ask.
func Form() ask.Form {
	return ask.Form{
		Title: "Reverse proxy baru (demo)",
		Questions: []ask.Question{
			{
				ID:          "domain",
				Header:      "Domain",
				Prompt:      "Domain untuk aplikasi ini?",
				Kind:        ask.Text,
				Placeholder: "app.contoh.com",
				Help:        "Domain adalah alamat yang diketik orang di browser. DNS domain harus mengarah ke IP server ini supaya HTTPS Let's Encrypt bisa diterbitkan.",
				Validate:    validateDomain,
			},
			{
				ID:     "port",
				Header: "Port",
				Prompt: "Aplikasimu berjalan di port berapa?",
				Kind:   ask.Single,
				Other:  true,
				Options: []ask.Option{
					{Label: "3000", Value: "3000", Description: "Umum untuk Node.js (Express, Next.js)", Meta: "node, PID 2211", Recommended: true},
					{Label: "8000", Value: "8000", Description: "Umum untuk Python (Django, FastAPI)"},
					{Label: "8080", Value: "8080", Description: "Umum untuk Java dan aplikasi Go"},
					{Label: "80", Value: "80", Description: "Port HTTP standar", Disabled: "sudah dipakai nginx sendiri"},
				},
				Validate: validatePort,
			},
			{
				ID:     "template",
				Header: "Template",
				Prompt: "Pilih template config nginx?",
				Kind:   ask.Single,
				Options: []ask.Option{
					{Label: "Reverse proxy biasa", Value: "proxy", Recommended: true, Description: "Cukup untuk website dan REST API", Preview: nginxPreview(false)},
					{Label: "Reverse proxy + WebSocket", Value: "proxy-ws", Description: "Perlu bila aplikasi memakai socket.io / live update", Preview: nginxPreview(true)},
					{Label: "Situs statis", Value: "static", Description: "Menyajikan file HTML dari folder, tanpa aplikasi", Preview: "server {\n    listen 80;\n    root /var/www/app;\n    index index.html;\n}"},
				},
			},
			{
				ID:     "https",
				Header: "HTTPS",
				Prompt: "Pasang HTTPS gratis dari Let's Encrypt?",
				Kind:   ask.Confirm,
				Options: []ask.Option{
					{Label: "Ya", Description: "Butuh DNS domain sudah mengarah ke server ini dan port 80 terbuka", Recommended: true},
					{Label: "Tidak", Description: "Situs hanya bisa diakses lewat http:// (tidak aman untuk login)"},
				},
			},
			{
				ID:          "email",
				Header:      "Email",
				Prompt:      "Email untuk pemberitahuan sertifikat kedaluwarsa?",
				Kind:        ask.Text,
				Placeholder: "admin@contoh.com",
				When:        func(a ask.Answers) bool { return a["https"].Yes() },
				Validate: func(s string) error {
					if !strings.Contains(s, "@") {
						return errors.New("format email belum benar")
					}
					return nil
				},
			},
			{
				ID:     "cleanup",
				Header: "Bersihkan",
				Prompt: "Bersihkan apa saja sebelum mulai?",
				Kind:   ask.Multi,
				Other:  true,
				Options: []ask.Option{
					{Label: "Cache apt", Value: "apt", Meta: "~1,2 GB", Description: "File .deb yang sudah terpasang; aman dihapus, bisa diunduh ulang"},
					{Label: "Journal lama (sisakan 200 MB)", Value: "journal", Meta: "~850 MB", Description: "Log sistem lama; log terbaru tetap ada"},
					{Label: "Paket yang tidak dipakai lagi", Value: "autoremove", Meta: "~310 MB", Description: "Dependensi yatim hasil uninstall sebelumnya"},
					{Label: "Image & container Docker tak terpakai", Value: "docker", Meta: "~4,0 GB", Risk: risk.Caution, Description: "Container yang berhenti ikut terhapus, periksa dulu"},
				},
				Summary: func(sel []ask.Option) string { return fmt.Sprintf("Dipilih: %d item", len(sel)) },
			},
			{
				ID:     "owner",
				Header: "User",
				Prompt: "Aplikasi dijalankan sebagai user siapa?",
				Kind:   ask.Single,
				Load:   loadUsers,
				Help:   "Menjalankan aplikasi sebagai root berbahaya: celah di aplikasi berarti penyerang langsung jadi root. Buat user khusus bila belum ada.",
			},
			{
				ID:          "key",
				Header:      "SSH key",
				Prompt:      "Paste public key untuk deploy (opsional)?",
				Kind:        ask.TextArea,
				Placeholder: "ssh-ed25519 AAAA… komentar",
				Optional:    true,
			},
		},
	}
}

func validateDomain(s string) error {
	s = strings.TrimSpace(s)
	if strings.ContainsAny(s, " /:") || !strings.Contains(s, ".") {
		return errors.New("domain tidak valid, contoh yang benar: app.contoh.com")
	}
	if strings.HasSuffix(s, ".local") || strings.HasSuffix(s, ".test") {
		return ask.Warn("Domain ini tidak bisa mendapat sertifikat Let's Encrypt. Tekan enter lagi untuk tetap lanjut.")
	}
	return nil
}

func validatePort(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return errors.New("port harus angka 1-65535")
	}
	return nil
}

func nginxPreview(ws bool) string {
	extra := ""
	if ws {
		extra = "        proxy_http_version 1.1;\n        proxy_set_header Upgrade $http_upgrade;\n        proxy_set_header Connection \"upgrade\";\n"
	}
	return "server {\n    listen 80;\n    server_name app.contoh.com;\n\n    location / {\n        proxy_pass http://127.0.0.1:3000;\n        proxy_set_header Host $host;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n" + extra + "    }\n}"
}

// loadUsers mensimulasikan opsi dinamis yang dimuat dari sistem.
func loadUsers(ctx context.Context) ([]ask.Option, error) {
	select {
	case <-time.After(700 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	current := "developer"
	if u, err := user.Current(); err == nil {
		current = u.Username
	}
	return []ask.Option{
		{Label: "www-data", Value: "www-data", Description: "User bawaan web server, hak akses minim", Recommended: true},
		{Label: current, Value: current, Description: "User yang sedang login"},
		{Label: "root", Value: "root", Description: "Hak akses penuh ke seluruh sistem", Risk: risk.Dangerous},
	}, nil
}
