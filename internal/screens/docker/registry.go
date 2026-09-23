package docker

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// registryModel adalah layar profil registry (mis. Nexus): tempat login, push, dan pull image.
type registryModel struct {
	env      shared.Env
	client   sysdocker.Client
	path     string
	regs     sysdocker.Registries
	table    ui.Table
	selected sysdocker.Registry
	message  string
	err      string
}

// NewRegistry membuka layar profil registry image.
func NewRegistry(env shared.Env, c sysdocker.Client) *registryModel {
	m := &registryModel{env: env, client: c, table: ui.Table{Columns: []ui.Column{
		{Title: "Profil", Width: 12, Flex: 1},
		{Title: "Host", Width: 24, Flex: 3},
		{Title: "User", Width: 12, Flex: 1},
		{Title: "Repository", Width: 14, Flex: 2},
		{Title: "Koneksi", Width: 10},
	}}}
	path, err := sysdocker.DefaultRegistryPath()
	if err != nil {
		m.err = "tidak bisa menentukan lokasi file profil: " + err.Error()
		return m
	}
	m.path = path
	m.load()
	return m
}

func (m *registryModel) load() {
	regs, err := sysdocker.LoadRegistries(m.path)
	if err != nil {
		m.err = err.Error()
		return
	}
	m.err = ""
	m.regs = regs
	rows := make([][]string, len(regs.Items))
	for i, r := range regs.Items {
		conn := "HTTPS"
		if r.Insecure {
			conn = "HTTP"
		}
		user := r.User
		if user == "" {
			user = "—"
		}
		repo := r.Repo
		if repo == "" {
			repo = "—"
		}
		rows[i] = []string{r.Name, r.Host, user, repo, conn}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if col == 4 && row < len(regs.Items) && regs.Items[row].Insecure {
			return ui.Current.Warning
		}
		return lipgloss.NewStyle()
	}
}

func (m *registryModel) Title() string { return "Registry" }
func (m *registryModel) Init() tea.Cmd { return nil }

func (m *registryModel) Keys() []key.Binding {
	return []key.Binding{b("enter", "aksi registry"), b("a", "tambah profil")}
}

func (m *registryModel) HelpText() string {
	return "Registry adalah tempat menyimpan image supaya bisa dipakai server lain — Docker Hub, atau Nexus/Harbor milik kantor. " +
		"ubt hanya mencatat alamat & username di " + m.path + "; password tidak pernah disimpan ubt, melainkan dikirim langsung ke `docker login` " +
		"yang menyimpan tokennya di ~/.docker/config.json. " +
		"Nama image di registry selalu berbentuk host/repository/nama:tag — itulah sebabnya image perlu diberi tag sebelum di-push. " +
		"Command setara: docker login, docker tag, docker push, docker pull"
}

func (m *registryModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *registryModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.RefreshMsg:
		m.load()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "a":
			return m, nav.Push(ask.New(RegistryForm(sysdocker.Registry{})))
		case "enter":
			if m.table.Cursor < len(m.regs.Items) {
				m.selected = m.regs.Items[m.table.Cursor]
				return m, nav.Push(ask.New(RegistryActionForm(m.selected)))
			}
		}
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	}
	return m, nil
}

func (m *registryModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		a := r.Answers
		switch r.ID {
		case "reg-add":
			reg := sysdocker.Registry{
				Name:     strings.TrimSpace(a["name"].Value()),
				Host:     strings.ToLower(strings.TrimSpace(a["host"].Value())),
				User:     strings.TrimSpace(a["user"].Value()),
				Repo:     strings.Trim(strings.TrimSpace(a["repo"].Value()), "/"),
				Insecure: a["tls"].Value() == "http",
			}
			return m.confirm(sysdocker.SaveRegistriesPlan(m.path, m.regs.Upsert(reg), "Simpan profil registry "+reg.Name))
		case "reg-action":
			return m.action(a)
		case "reg-push":
			local := strings.TrimSpace(a["image"].Value())
			target := m.selected.Ref(strings.TrimSpace(a["target"].Value()))
			return m.confirm(m.client.PushPlan(local, target))
		case "reg-pull":
			return m.confirm(m.client.PullPlan(m.selected.Ref(strings.TrimSpace(a["image"].Value()))))
		case "reg-ca":
			return m.confirm(sysdocker.InstallCAPlan(m.selected.Host, strings.TrimSpace(a["file"].Value())))
		}
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal."
				if hint := sysdocker.RegistryHint(outputOf(r)); hint != "" {
					m.message += " " + hint
				}
			}
		}
		m.load()
	}
	return m, nil
}

// outputOf menggabungkan output semua langkah untuk dicari pesan errornya.
func outputOf(o run.Outcome) string {
	var b strings.Builder
	for _, res := range o.Results {
		for _, l := range res.Output {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *registryModel) action(a ask.Answers) (nav.Screen, tea.Cmd) {
	reg := m.selected
	switch a["action"].Value() {
	case "login":
		return m.confirm(m.client.LoginPlan(reg))
	case "logout":
		return m.confirm(m.client.LogoutPlan(reg.Host))
	case "push":
		return m, nav.Push(ask.New(PushForm(reg, imageOptions(m.client, m.env.Runner))))
	case "pull":
		return m, nav.Push(ask.New(ask.Form{ID: "reg-pull", Title: "Unduh dari " + reg.Name, SkipReview: true, Questions: []ask.Question{{
			ID: "image", Kind: ask.Text, Prompt: "Nama image di registry (tanpa " + reg.Prefix() + ")?", Placeholder: "app:1.0",
			Validate: sysdocker.ValidImage,
			Help:     "ubt menambahkan awalan " + reg.Prefix() + "/ secara otomatis. Daftar image & tag bisa dilihat di " + reg.URL() + ".",
		}}}))
	case "ca":
		return m, nav.Push(ask.New(ask.Form{ID: "reg-ca", Title: "Sertifikat CA " + reg.Name, SkipReview: true, Questions: []ask.Question{{
			ID: "file", Kind: ask.Text, Prompt: "Path berkas sertifikat CA (format PEM)?", Placeholder: "/home/kamu/ca-internal.crt",
			Validate: validFile,
			Help: "Minta berkas CA ke tim infrastruktur (bukan sertifikat servernya). ubt menyalinnya ke " + sysdocker.CertPath(reg.Host) +
				", tempat docker mencari CA khusus per registry. Periksa dulu isinya: openssl x509 -in BERKAS -noout -subject -fingerprint -sha256",
		}}}))
	case "insecure":
		cfg, err := sysdocker.ReadDaemonJSON(sysdocker.DaemonJSONPath)
		if err != nil {
			m.message = "✗ " + err.Error()
			return m, nil
		}
		return m.confirm(sysdocker.InsecureRegistryPlan(reg.Host, cfg, len(cfg) > 0))
	case "remove":
		return m.confirm(sysdocker.SaveRegistriesPlan(m.path, m.regs.Remove(reg.Name), "Hapus profil registry "+reg.Name))
	}
	return m, nil
}

func (m *registryModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Profil registry (%d)", len(m.regs.Items))) + "   " + t.Muted.Render(m.path)}
	if m.err != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.regs.Items) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada profil registry",
			"Profil menyimpan alamat & username registry (mis. Nexus kantor) supaya tidak perlu mengetik ulang saat push/pull. Password tidak disimpan ubt.",
			"tekan a untuk menambah profil", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// RegistryForm adalah wizard menambah/mengubah profil registry.
func RegistryForm(cur sysdocker.Registry) ask.Form {
	return ask.Form{ID: "reg-add", Title: "Tambah profil registry", Questions: []ask.Question{
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama pendek untuk profil ini?", Placeholder: "nexus", Default: def(cur.Name),
			Validate: sysdocker.ValidRegistryName,
			Help:     "Hanya label di ubt supaya mudah dipilih; boleh apa saja, mis. nexus atau nexus-staging."},
		{ID: "host", Header: "Host", Kind: ask.Text, Prompt: "Alamat registry (host dan port, tanpa https://)?",
			Placeholder: "nexus.contoh.com:8082", Default: def(cur.Host), Validate: sysdocker.ValidRegistryHost,
			Help: "Di Nexus, ini alamat \"docker repository connector\" — biasanya port terpisah dari antarmuka webnya (mis. web di 8081, registry di 8082). Tanya tim infrastruktur bila ragu."},
		{ID: "repo", Header: "Repository", Kind: ask.Text, Optional: true, Prompt: "Awalan repository di dalam registry?",
			Placeholder: "kosongkan bila tidak ada", Default: def(cur.Repo), Validate: sysdocker.ValidRegistryRepo,
			Help: "Beberapa registry mengelompokkan image per tim/proyek, mis. tim-a. Bila diisi, nama lengkap image jadi host/tim-a/nama:tag."},
		{ID: "user", Header: "User", Kind: ask.Text, Optional: true, Prompt: "Username untuk login?",
			Placeholder: "kosongkan bila registry bisa diakses tanpa login", Default: def(cur.User), Validate: sysdocker.ValidRegistryUser,
			Help: "Password TIDAK ditanyakan di sini. Nanti saat login, docker sendiri yang memintanya di terminal."},
		{ID: "tls", Header: "Koneksi", Kind: ask.Single, Prompt: "Registry ini diakses lewat apa?", Options: []ask.Option{
			{Value: "https", Label: "HTTPS", Description: "Termasuk bila sertifikatnya dari CA internal — nanti pasang sertifikatnya lewat menu aksi registry.", Recommended: true},
			{Value: "http", Label: "HTTP polos", Description: "Perlu menambahkan registry ini ke daftar insecure-registries dan me-restart docker. Password dan isi image lewat jaringan tanpa perlindungan.", Risk: risk.Dangerous},
		}},
	}}
}

// RegistryActionForm menawarkan aksi untuk satu profil registry.
func RegistryActionForm(r sysdocker.Registry) ask.Form {
	insecure := ""
	if !r.Insecure {
		insecure = "profil ini memakai HTTPS"
	}
	ca := ""
	if r.Insecure {
		ca = "profil ini memakai HTTP polos, bukan HTTPS"
	}
	return ask.Form{ID: "reg-action", Title: r.Name, SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan " + r.Name + " (" + r.Host + ")?",
		Options: []ask.Option{
			{Value: "login", Label: "Login", Description: "Simpan kredensial supaya push & pull tidak ditolak. Password diketik langsung ke docker.", Recommended: true},
			{Value: "push", Label: "Unggah image (push)", Description: "Beri tag " + r.Prefix() + "/… pada image lokal lalu unggah."},
			{Value: "pull", Label: "Unduh image (pull)", Description: "Ambil image dari registry ini ke server."},
			{Value: "ca", Label: "Pasang sertifikat CA internal", Description: "Untuk registry HTTPS dengan sertifikat yang ditandatangani CA kantor (error \"x509: certificate signed by unknown authority\").", Disabled: ca},
			{Value: "insecure", Label: "Izinkan HTTP polos", Description: "Menulis insecure-registries di /etc/docker/daemon.json lalu me-restart docker — semua container berhenti sebentar.", Disabled: insecure, Risk: risk.Dangerous},
			{Value: "logout", Label: "Logout", Description: "Hapus kredensial registry ini dari ~/.docker/config.json."},
			{Value: "remove", Label: "Hapus profil", Description: "Menghapus catatan di ubt saja; kredensial docker dan image tidak tersentuh.", Risk: risk.Caution},
		},
	}}}
}

// PushForm menanyakan image lokal mana yang diunggah dan dengan nama apa di registry.
func PushForm(r sysdocker.Registry, load func(context.Context) ([]ask.Option, error)) ask.Form {
	return ask.Form{ID: "reg-push", Title: "Unggah ke " + r.Name, Questions: []ask.Question{
		{ID: "image", Header: "Image", Kind: ask.Single, Prompt: "Image lokal mana yang diunggah?", Other: true, Load: load, Validate: sysdocker.ValidImage,
			Help: "Image hasil build atau commit di server ini. Belum ada? Build dulu lewat menu image (tombol b)."},
		{ID: "target", Header: "Nama", Kind: ask.Text, Prompt: "Disimpan di registry dengan nama & tag apa?", Placeholder: "app:1.0", Validate: sysdocker.ValidImage,
			Help: "ubt menambahkan awalan " + r.Prefix() + "/ otomatis, jadi tulis nama pendeknya saja. Pakai tag versi (mis. 1.0) supaya bisa kembali ke versi sebelumnya; :latest saja membuat riwayat versi hilang."},
	}}
}
