// Package docker adalah layar modul Docker: container, image, shell interaktif, wizard Dockerfile/compose,
// dan menjalankan modul Python di container.
package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

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

// Model adalah dashboard Docker (daftar container).
type Model struct {
	env      shared.Env
	client   sysdocker.Client
	status   sysdocker.Status
	loaded   bool
	table    ui.Table
	message  string
	selected sysdocker.Container
	pending  pendingFiles
	// sudoProbe menandai Plan "baca dengan sudo" yang sedang dikonfirmasi.
	sudoProbe bool
}

type statusMsg struct {
	owner  *Model
	status sysdocker.Status
}

// New membuat dashboard Docker.
func New(env shared.Env) *Model { return newModel(env, sysdocker.NewClient()) }

func newModel(env shared.Env, c sysdocker.Client) *Model {
	return &Model{env: env, client: c, table: ui.Table{Columns: []ui.Column{
		{Title: "Container", Width: 16, Flex: 2},
		{Title: "Image", Width: 16, Flex: 2},
		{Title: "Status", Width: 14, Flex: 1},
		{Title: "Port", Width: 14, Flex: 2},
		{Title: "Project", Width: 10, Flex: 1},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Docker" }

func b(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }

func (m *Model) Keys() []key.Binding {
	switch m.status.Avail {
	case sysdocker.NotInstalled:
		return []key.Binding{b("i", "install docker"), b("n", "buat Dockerfile/compose")}
	case sysdocker.DaemonDown:
		return []key.Binding{b("d", "nyalakan daemon"), b("n", "buat Dockerfile/compose")}
	case sysdocker.NoPermission:
		return []key.Binding{b("g", "izinkan tanpa sudo"), b("s", "pakai sudo"), b("n", "buat Dockerfile/compose")}
	}
	return []key.Binding{b("enter", "aksi container"), b("c", "container baru"), b("i", "image"), b("v", "volume & network"), b("u", "registry Nexus"),
		b("s", "statistik"), b("e", "shell"), b("l", "log"), b("n", "buat file"), b("y", "jalankan python"), b("x", "bersihkan")}
}

func (m *Model) HelpText() string {
	return "Image adalah cetakan (sistem file + program), container adalah image yang sedang/pernah dijalankan. " +
		"File yang dibuat di dalam container hilang saat container dihapus, kecuali disimpan di volume atau di-commit jadi image. " +
		"docker compose menjalankan beberapa container sekaligus dari file docker-compose.yml. " +
		"Command setara: docker ps -a, docker images, docker system df"
}

func (m *Model) Init() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return statusMsg{owner: m, status: c.Read(ctx, r)}
	}
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func (m *Model) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		if msg.owner == m {
			m.loaded, m.status = true, msg.status
			m.fill()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	case tea.KeyPressMsg:
		if m.status.Avail == sysdocker.Ready && m.table.HandleKey(msg.String()) {
			return m, nil
		}
		return m.key(msg.String())
	}
	return m, nil
}

func (m *Model) key(k string) (nav.Screen, tea.Cmd) {
	m.message = ""
	st := m.status
	if k == "n" {
		return m, nav.Push(ask.New(FilesForm(workDir())))
	}
	switch st.Avail {
	case sysdocker.NotInstalled:
		if k == "i" {
			return m.confirm(sysdocker.InstallPlan(currentUser()))
		}
		return m, nil
	case sysdocker.DaemonDown:
		if k == "d" {
			return m.confirm(sysdocker.StartDaemonPlan())
		}
		return m, nil
	case sysdocker.NoPermission:
		switch k {
		case "g":
			return m.confirm(run.Single(sysdocker.AddGroupCommand(currentUser())))
		case "s":
			m.sudoProbe = true
			c := m.client
			c.Sudo = true
			cmd := c.Command("info", "--format", "{{.ServerVersion}}")
			cmd.Title, cmd.Risk = "Akses Docker dengan sudo", risk.Safe
			cmd.Explain = []run.Line{{Token: "sudo docker info", Meaning: "cek daemon memakai hak root"}}
			cmd.Effect = "Hanya membaca. Setelah sudo diizinkan, ubt menjalankan command docker dengan sudo selama sesi ini."
			return m.confirm(run.Single(cmd))
		}
		return m, nil
	case sysdocker.Unknown:
		return m, nil
	}

	var ct *sysdocker.Container
	if m.table.Cursor < len(st.Containers) {
		ct = &st.Containers[m.table.Cursor]
	}
	switch k {
	case "enter":
		if ct != nil {
			m.selected = *ct
			return m, nav.Push(ask.New(ContainerActionForm(*ct)))
		}
	case "e":
		if ct != nil {
			if !ct.Running() {
				m.message = "Container " + ct.Name() + " tidak berjalan. Jalankan dulu (enter → start)."
				return m, nil
			}
			return m.confirm(m.client.ExecShellPlan(*ct))
		}
	case "l":
		if ct != nil {
			return m, nav.Push(newLogs(m.env, m.client, *ct))
		}
	case "i":
		return m, nav.Push(newImages(m.env, m.client, st.Images))
	case "c":
		return m, nav.Push(ask.New(RunForm(m.client, m.env.Runner, sysdocker.RunSpec{})))
	case "v":
		return m, nav.Push(NewVolumes(m.env, m.client, st.Containers))
	case "u":
		return m, nav.Push(NewRegistry(m.env, m.client))
	case "s":
		return m, nav.Push(NewStats(m.env, m.client))
	case "y":
		return m, nav.Push(ask.New(PythonForm(workDir())))
	case "x":
		return m, nav.Push(ask.New(PruneForm(st)))
	}
	return m, nil
}

func (m *Model) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		switch r.ID {
		case "ctr-action":
			return m.containerAction(r.Answers)
		case "python":
			a := r.Answers
			return m.confirm(m.client.PythonRunPlan(strings.TrimSpace(a["image"].Value()), expandDir(a["dir"].Value()), strings.TrimSpace(a["module"].Value()), m.env.UID, os.Getgid()))
		case "prune":
			a := r.Answers
			return m.confirm(m.client.PrunePlan(a["what"].Has("images"), a["what"].Has("volumes")))
		case "run":
			return m.runContainer(r.Answers)
		case "files":
			m.pending = pendingFromAnswers(r.Answers)
			return m.writeFiles(false)
		case "overwrite":
			if r.Answers["ok"].Yes() {
				return m.writeFiles(true)
			}
			m.message = "Dibatalkan; file yang sudah ada tidak diubah."
			return m, nil
		}
	case run.Outcome:
		if m.sudoProbe {
			m.sudoProbe = false
			if r.Approved && r.OK() {
				m.client.Sudo = true
			}
		}
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
				if m.pending.dir != "" && strings.HasSuffix(r.Plan.Title, m.pending.title) {
					m.message += " " + m.pending.next()
					m.pending = pendingFiles{}
				}
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
			}
		}
	}
	return m, m.Init()
}

func (m *Model) containerAction(a ask.Answers) (nav.Screen, tea.Cmd) {
	ct, c := m.selected, m.client
	switch a["action"].Value() {
	case "shell":
		return m.confirm(c.ExecShellPlan(ct))
	case "logs":
		return m, nav.Push(newLogs(m.env, c, ct))
	case "start":
		return m.confirm(c.StartPlan(ct))
	case "stop":
		return m.confirm(c.StopPlan(ct))
	case "restart":
		return m.confirm(c.RestartPlan(ct))
	case "commit":
		return m.confirm(c.CommitPlan(ct, strings.TrimSpace(a["image"].Value())))
	case "rm":
		return m.confirm(c.RemovePlan(ct))
	case "inspect":
		return m, nav.Push(NewDetail(m.env, c, ct.Name()))
	}
	return m, nil
}

// runContainer menjalankan container baru dari jawaban wizard, dan mencatat resepnya bila diminta.
func (m *Model) runContainer(a ask.Answers) (nav.Screen, tea.Cmd) {
	spec, err := SpecFromAnswers(a)
	if err != nil {
		m.message = "✗ " + err.Error()
		return m, nil
	}
	path := ""
	var recipes sysdocker.Recipes
	if a["save"].Yes() {
		if p, err := sysdocker.DefaultRecipePath(); err == nil {
			path = p
			recipes, _ = sysdocker.LoadRecipes(p)
		}
	}
	return m.confirm(m.client.RunPlan(spec, path, recipes))
}

func (m *Model) writeFiles(overwrite bool) (nav.Screen, tea.Cmd) {
	p := m.pending
	plan, existing, err := sysdocker.WriteFilesPlan(p.title, p.dir, p.files, p.order, overwrite)
	if errors.Is(err, sysdocker.ErrExists) {
		return m, nav.Push(ask.New(ask.Form{ID: "overwrite", Title: "File sudah ada", Questions: []ask.Question{{
			ID: "ok", Kind: ask.Confirm, Prompt: "File berikut sudah ada: " + strings.Join(existing, ", ") + ". Timpa?", Default: []string{ask.ValueNo},
			Options: []ask.Option{
				{Label: "Ya, timpa", Description: "Isi lama hilang. Pertimbangkan cadangkan dulu dengan cp FILE FILE.bak.", Risk: risk.Dangerous},
				{Label: "Tidak", Description: "Batalkan tanpa mengubah apa pun.", Recommended: true},
			},
		}}}))
	}
	return m.confirm(plan)
}

func (m *Model) fill() {
	cs := m.status.Containers
	rows := make([][]string, len(cs))
	for i, c := range cs {
		rows[i] = []string{c.Name(), c.Image, c.Status, shortPorts(c.Ports), c.ComposeProject()}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, col int) lipgloss.Style {
		if r >= len(cs) {
			return lipgloss.NewStyle()
		}
		t := ui.Current
		c := cs[r]
		switch {
		case !c.Running():
			return t.Muted
		case col == 2 && strings.Contains(c.Status, "unhealthy"), col == 2 && c.State == "restarting":
			return t.Danger
		case col == 2:
			return t.Success
		}
		return lipgloss.NewStyle()
	}
}

// shortPorts meringkas "0.0.0.0:5432->5432/tcp, [::]:5432->5432/tcp" menjadi "5432→5432".
func shortPorts(s string) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ", ") {
		if p == "" {
			continue
		}
		host, ctr, ok := strings.Cut(p, "->")
		v := strings.TrimSuffix(p, "/tcp")
		if ok {
			if i := strings.LastIndex(host, ":"); i >= 0 {
				host = host[i+1:]
			}
			v = host + "→" + strings.TrimSuffix(ctr, "/tcp")
			if strings.HasPrefix(p, "127.0.0.1:") {
				v = "lokal:" + v
			}
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return strings.Join(out, " ")
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Memeriksa Docker…")
	}
	st := m.status
	var lines []string
	add := func(s string) { lines = append(lines, ui.Wrap(s, width, " ")) }
	lines = append(lines, "")
	switch st.Avail {
	case sysdocker.NotInstalled:
		add(t.Warning.Render("Docker belum terpasang."))
		add(t.Subtle.Render("Tekan i untuk memasang docker.io + plugin compose dari repository Ubuntu, sekaligus mengizinkan user " + currentUser() + " memakai docker tanpa sudo."))
	case sysdocker.DaemonDown:
		add(t.Warning.Render("Docker terpasang, tetapi daemon-nya tidak berjalan."))
		add(t.Muted.Render(st.Error))
		add(t.Subtle.Render("Tekan d untuk menyalakan (systemctl enable --now docker). Bila tetap gagal, lihat log service docker di modul Log."))
	case sysdocker.NoPermission:
		add(t.Warning.Render("Daemon Docker berjalan, tetapi user " + currentUser() + " tidak punya izin memakainya."))
		add(t.Muted.Render(st.Error))
		add(t.Subtle.Render("Pilihan: g memasukkan kamu ke grup docker (berlaku setelah logout-login; bila sudah dilakukan tetapi masih gagal, berarti sesi ini dibuka sebelum grup ditambahkan), atau s untuk memakai sudo selama sesi ini."))
	case sysdocker.Unknown:
		add(t.Danger.Render("✗ Docker tidak bisa diakses: " + st.Error))
	}
	if st.Avail != sysdocker.Ready {
		if m.message != "" {
			lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
		}
		lines = append(lines, "", ui.Wrap(t.Subtle.Render("Wizard Dockerfile/compose (n) tetap bisa dipakai tanpa Docker."), width, " "))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}

	head := " " + t.Title.Render("Docker "+st.Version)
	if m.client.Sudo {
		head += "  " + t.Warning.Render("(lewat sudo)")
	}
	if st.Compose {
		head += "  " + t.Success.Render("✓ compose")
	} else {
		head += "  " + t.Muted.Render("compose belum terpasang (apt install docker-compose-v2)")
	}
	lines = append(lines, head)
	var disk []string
	for _, d := range st.Disk {
		part := fmt.Sprintf("%s %s", strings.ToLower(d.Type), d.Size)
		// docker melaporkan "reclaimable 100%" untuk image yang semuanya dipakai; abaikan bila semua aktif.
		if d.Reclaimable != "" && !strings.HasPrefix(d.Reclaimable, "0B") && !(d.Active == d.TotalCount && d.Type != "Build Cache") {
			part += " (bisa dibebaskan " + d.Reclaimable + ")"
		}
		disk = append(disk, part)
	}
	if len(disk) > 0 {
		lines = append(lines, ui.Wrap(t.Subtle.Render("Disk: "+strings.Join(disk, " · ")), width, " "))
	}
	running := 0
	for _, c := range st.Containers {
		if c.Running() {
			running++
		}
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", " "+t.Title.Render(fmt.Sprintf("Container (%d berjalan dari %d)", running, len(st.Containers)))+"   "+t.Muted.Render("command setara: docker ps -a"))
	top := strings.Join(lines, "\n")
	if len(st.Containers) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada container", "", "tekan i untuk melihat image dan membuka shell dari image, atau n untuk membuat compose", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ContainerActionForm menawarkan aksi untuk satu container.
func ContainerActionForm(ct sysdocker.Container) ask.Form {
	running := ct.Running()
	notRunning := ""
	if !running {
		notRunning = "container tidak berjalan"
	}
	isRunning := ""
	if running {
		isRunning = "sudah berjalan"
	}
	opts := []ask.Option{
		{Value: "shell", Label: "Buka shell di dalamnya", Description: "Masuk ke container untuk memeriksa file atau menjalankan command (docker exec -it).", Disabled: notRunning, Recommended: running},
		{Value: "logs", Label: "Lihat log", Description: "Output program di container (docker logs)."},
		{Value: "start", Label: "Jalankan (start)", Description: "Nyalakan lagi container yang berhenti.", Disabled: isRunning, Recommended: !running},
		{Value: "restart", Label: "Restart", Description: "Hentikan lalu jalankan lagi; berguna setelah mengubah konfigurasi.", Disabled: notRunning},
		{Value: "stop", Label: "Hentikan (stop)", Description: "Data tetap ada; bisa dijalankan lagi.", Disabled: notRunning, Risk: risk.Caution},
		{Value: "commit", Label: "Simpan jadi image (commit)", Description: "Potret isi container sekarang menjadi image baru, mis. setelah install paket di dalamnya."},
		{Value: "inspect", Label: "Periksa & diagnosa", Description: "Kenapa restart terus / kenapa berhenti, setting apa yang dipakai, dan buat ulang dengan image terbaru."},
		{Value: "rm", Label: "Hapus container", Description: "File di dalam container (di luar volume) hilang permanen.", Risk: risk.Dangerous},
	}
	if p := ct.ComposeProject(); p != "" {
		opts[4].Description += " Bagian project compose \"" + p + "\"."
	}
	return ask.Form{ID: "ctr-action", Title: ct.Name(), SkipReview: true, Questions: []ask.Question{
		{ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan " + ct.Name() + " (" + ct.Image + ")?", Options: opts},
		{ID: "image", Header: "Image", Kind: ask.Text, Prompt: "Nama image baru?", Placeholder: "namaku/" + ct.Name() + ":v1",
			Help:     "Format NAMA:TAG dengan huruf kecil. Tag membantu membedakan versi, mis. :v1, :2026-09-16.",
			Validate: sysdocker.ValidImage, When: func(a ask.Answers) bool { return a["action"].Value() == "commit" }},
	}}
}

// PruneForm menanyakan seberapa agresif membersihkan.
func PruneForm(st sysdocker.Status) ask.Form {
	stopped := 0
	for _, c := range st.Containers {
		if !c.Running() {
			stopped++
		}
	}
	return ask.Form{ID: "prune", Title: "Bersihkan Docker", Questions: []ask.Question{{
		ID: "what", Header: "Tambahan", Kind: ask.Multi, Optional: true,
		Prompt: fmt.Sprintf("Selalu dihapus: %d container yang berhenti, network tak terpakai, image tanpa nama, build cache. Hapus juga:", stopped),
		Options: []ask.Option{
			{Value: "images", Label: "Semua image yang tidak dipakai", Description: "Harus diunduh ulang saat dibutuhkan. Image buatan sendiri yang tidak dipakai container ikut hilang.", Risk: risk.Caution},
			{Value: "volumes", Label: "Volume yang tidak dipakai", Description: "Volume menyimpan data (mis. database). Data yang terhapus tidak bisa dikembalikan.", Risk: risk.Dangerous},
		},
	}}}
}

func workDir() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return os.Getenv("HOME")
}

func expandDir(s string) string {
	s = strings.TrimSpace(s)
	if s == "~" || strings.HasPrefix(s, "~/") {
		s = filepath.Join(os.Getenv("HOME"), s[1:])
	}
	if abs, err := filepath.Abs(s); err == nil {
		return abs
	}
	return s
}

func validDir(s string) error {
	fi, err := os.Stat(expandDir(s))
	if err != nil || !fi.IsDir() {
		return errors.New("direktori tidak ditemukan")
	}
	return nil
}

// PythonForm menanyakan cara menjalankan modul Python di container.
func PythonForm(dir string) ask.Form {
	return ask.Form{ID: "python", Title: "Jalankan Python di container", Questions: []ask.Question{
		{ID: "dir", Header: "Direktori", Kind: ask.Text, Prompt: "Direktori proyek yang berisi kode?", Default: []string{dir}, Validate: validDir,
			Help: "Direktori ini dipasang ke /app di dalam container, jadi program bisa membaca dan menulis file proyek."},
		{ID: "module", Header: "Modul", Kind: ask.Text, Prompt: "Nama modul yang dijalankan (python -m …)?", Placeholder: "app.main",
			Help: "Untuk file app/main.py tulis app.main; untuk main.py di akar proyek tulis main.", Validate: sysdocker.ValidModule},
		{ID: "image", Header: "Image", Kind: ask.Single, Prompt: "Versi Python?", Other: true, Validate: sysdocker.ValidImage, Options: []ask.Option{
			{Value: "python:3.12-slim", Label: "python:3.12-slim", Description: "Kecil (~50 MB), cukup untuk kebanyakan aplikasi.", Recommended: true},
			{Value: "python:3.13-slim", Label: "python:3.13-slim", Description: "Versi terbaru."},
			{Value: "python:3.12", Label: "python:3.12", Description: "Lengkap dengan compiler; perlu bila paket harus dikompilasi (mis. psycopg2 tanpa -binary)."},
		}},
	}}
}

// FilesForm adalah wizard pembuat Dockerfile / docker-compose.yml.
func FilesForm(dir string) ask.Form {
	isCompose := func(a ask.Answers) bool { return a["kind"].Value() == "compose" }
	isDockerfile := func(a ask.Answers) bool { return a["kind"].Value() == "dockerfile" }
	listValidator := func(v func(string) error) func(string) error {
		return func(s string) error {
			for _, item := range sysdocker.SplitList(s) {
				if err := v(item); err != nil {
					return fmt.Errorf("%q: %w", item, err)
				}
			}
			return nil
		}
	}
	return ask.Form{ID: "files", Title: "Buat Dockerfile / compose", Questions: []ask.Question{
		{ID: "kind", Header: "Jenis", Kind: ask.Single, Prompt: "File apa yang ingin dibuat?", Options: []ask.Option{
			{Value: "compose", Label: "docker-compose.yml", Description: "Menjalankan image jadi (database, redis, aplikasi) dengan port, volume, dan environment tercatat di satu file.", Recommended: true},
			{Value: "dockerfile", Label: "Dockerfile", Description: "Membungkus kode aplikasimu (Python, Node, situs statis) menjadi image sendiri."},
		}},
		{ID: "dir", Header: "Direktori", Kind: ask.Text, Prompt: "Simpan di direktori mana?", Default: []string{dir}, Validate: validDir,
			Help: "File yang sudah ada tidak akan ditimpa tanpa persetujuanmu."},

		{ID: "service", Header: "Service", Kind: ask.Text, Prompt: "Nama service?", Placeholder: "db", Validate: sysdocker.ValidName, When: isCompose,
			Help: "Nama pendek untuk service ini; juga dipakai sebagai hostname oleh service lain di file yang sama."},
		{ID: "source", Header: "Sumber", Kind: ask.Single, Prompt: "Container dibuat dari mana?", When: isCompose, Options: []ask.Option{
			{Value: "image", Label: "Image jadi", Description: "Mis. postgres:17, redis:7, nginx:alpine dari Docker Hub.", Recommended: true},
			{Value: "build", Label: "Build dari Dockerfile di direktori ini", Description: "Untuk aplikasimu sendiri; buat Dockerfile-nya dulu dengan wizard ini."},
		}},
		{ID: "image", Header: "Image", Kind: ask.Single, Prompt: "Image yang dipakai?", Other: true, Validate: sysdocker.ValidImage,
			When: func(a ask.Answers) bool { return isCompose(a) && a["source"].Value() == "image" }, Options: []ask.Option{
				{Value: "postgres:17", Label: "postgres:17", Description: "Database PostgreSQL. Wajib env POSTGRES_PASSWORD; volume ke /var/lib/postgresql/data."},
				{Value: "mysql:8.4", Label: "mysql:8.4", Description: "Database MySQL. Wajib env MYSQL_ROOT_PASSWORD; volume ke /var/lib/mysql."},
				{Value: "redis:7-alpine", Label: "redis:7-alpine", Description: "Cache/antrian Redis; port 6379."},
				{Value: "nginx:alpine", Label: "nginx:alpine", Description: "Web server; isi situs di /usr/share/nginx/html."},
			}},
		{ID: "ports", Header: "Port", Kind: ask.Text, Optional: true, When: isCompose, Validate: listValidator(sysdocker.ValidPortMapping),
			Prompt: "Port yang dibuka (HOST:CONTAINER, pisahkan koma)?", Placeholder: "127.0.0.1:5432:5432",
			Help: "Awali dengan 127.0.0.1: supaya port hanya bisa diakses dari server ini. Docker melewati ufw, jadi port tanpa 127.0.0.1 langsung terbuka ke internet."},
		{ID: "volumes", Header: "Volume", Kind: ask.Text, Optional: true, When: isCompose, Validate: listValidator(sysdocker.ValidVolume),
			Prompt: "Volume (SUMBER:/path/di/container, pisahkan koma)?", Placeholder: "dbdata:/var/lib/postgresql/data",
			Help: "Sumber berupa nama (dbdata) = volume dikelola Docker; berupa path (./data) = direktori di server. Tanpa volume, data hilang saat container dihapus."},
		{ID: "env", Header: "Env", Kind: ask.Text, Optional: true, When: isCompose, Validate: listValidator(sysdocker.ValidEnv),
			Prompt: "Environment (NAMA=nilai, pisahkan koma)?", Placeholder: "POSTGRES_PASSWORD=ganti-ini",
			Help: "Jangan commit file berisi password ke git; pertimbangkan file .env terpisah."},
		{ID: "restart", Header: "Restart", Kind: ask.Single, Prompt: "Kapan container dijalankan ulang otomatis?", When: isCompose, Options: []ask.Option{
			{Value: "unless-stopped", Label: "unless-stopped", Description: "Hidup lagi setelah crash dan reboot, kecuali kamu menghentikannya sendiri.", Recommended: true},
			{Value: "always", Label: "always", Description: "Selalu hidup lagi, termasuk setelah dihentikan manual lalu daemon restart."},
			{Value: "no", Label: "no", Description: "Tidak pernah otomatis."},
		}},

		{ID: "app", Header: "Aplikasi", Kind: ask.Single, Prompt: "Jenis aplikasi?", When: isDockerfile, Options: []ask.Option{
			{Value: "python", Label: "Python", Description: "python:3.12-slim, install requirements.txt, jalankan python -m app.", Recommended: true},
			{Value: "node", Label: "Node.js", Description: "node:22-alpine, npm ci, jalankan node server.js."},
			{Value: "static", Label: "Situs statis", Description: "nginx:alpine menyajikan file HTML/CSS/JS di direktori ini."},
		}},
		{ID: "cmd", Header: "Command", Kind: ask.Text, Optional: true, Placeholder: "kosongkan untuk bawaan",
			When:   func(a ask.Answers) bool { return isDockerfile(a) && a["app"].Value() != "static" },
			Prompt: "Command untuk menjalankan aplikasi?",
			Help:   "Bawaan Python: python -m app. Bawaan Node: node server.js. Untuk web Python biasanya: gunicorn -b 0.0.0.0:8000 app:app"},
		{ID: "port", Header: "Port", Kind: ask.Text, Optional: true, Placeholder: "kosongkan untuk bawaan", When: isDockerfile,
			Prompt: "Port yang didengarkan aplikasi di dalam container?",
			Help:   "Hanya dokumentasi (EXPOSE); port dibuka ke host lewat -p atau ports di compose. Bawaan: Python 8000, Node 3000, statis 80."},
	}}
}

// pendingFiles menyimpan hasil wizard sampai file ditulis.
type pendingFiles struct {
	title string
	dir   string
	files map[string]string
	order []string
	hint  string
}

func (p pendingFiles) next() string { return p.hint }

func pendingFromAnswers(a ask.Answers) pendingFiles {
	dir := expandDir(a["dir"].Value())
	if a["kind"].Value() == "compose" {
		spec := ComposeSpecFromAnswers(a)
		return pendingFiles{title: "Buat docker-compose.yml", dir: dir,
			files: map[string]string{"docker-compose.yml": sysdocker.ComposeFile(spec)}, order: []string{"docker-compose.yml"},
			hint: "Jalankan: cd " + run.JoinShell([]string{dir}) + " && docker compose up -d"}
	}
	spec := sysdocker.DockerfileKinds[a["app"].Value()]
	if c := strings.TrimSpace(a["cmd"].Value()); c != "" {
		spec.Cmd = c
	}
	if p := strings.TrimSpace(a["port"].Value()); p != "" {
		spec.Port = p
	}
	depsFile := map[string]string{"python": "requirements.txt", "node": "package.json"}[spec.Kind]
	if _, err := os.Stat(filepath.Join(dir, depsFile)); depsFile != "" && err == nil {
		spec.Deps = true
	}
	return pendingFiles{title: "Buat Dockerfile", dir: dir,
		files: map[string]string{"Dockerfile": sysdocker.Dockerfile(spec), ".dockerignore": sysdocker.DockerIgnore}, order: []string{"Dockerfile", ".dockerignore"},
		hint: "Build: cd " + run.JoinShell([]string{dir}) + " && docker build -t nama-app ."}
}

// ComposeSpecFromAnswers membaca jawaban wizard compose.
func ComposeSpecFromAnswers(a ask.Answers) sysdocker.ComposeSpec {
	return sysdocker.ComposeSpec{
		Service: strings.TrimSpace(a["service"].Value()),
		Image:   strings.TrimSpace(a["image"].Value()),
		Build:   a["source"].Value() == "build",
		Ports:   sysdocker.SplitList(a["ports"].Value()),
		Volumes: sysdocker.SplitList(a["volumes"].Value()),
		Env:     sysdocker.SplitList(a["env"].Value()),
		Restart: a["restart"].Value(),
	}
}

// --- image -------------------------------------------------------------------------------------------

type imagesModel struct {
	env      shared.Env
	client   sysdocker.Client
	images   []sysdocker.Image
	table    ui.Table
	selected sysdocker.Image
	message  string
}

func newImages(env shared.Env, c sysdocker.Client, images []sysdocker.Image) *imagesModel {
	m := &imagesModel{env: env, client: c, table: ui.Table{Columns: []ui.Column{
		{Title: "Image", Width: 24, Flex: 3},
		{Title: "Ukuran", Width: 8},
		{Title: "Dibuat", Width: 14},
		{Title: "Dipakai", Width: 7},
	}}}
	m.setImages(images)
	return m
}

func (m *imagesModel) setImages(images []sysdocker.Image) {
	m.images = images
	rows := make([][]string, len(images))
	for i, img := range images {
		used := img.Containers
		if used == "N/A" || used == "0" {
			used = ""
		}
		rows[i] = []string{img.Ref(), img.Size, img.CreatedSince, used}
	}
	m.table.SetRows(rows)
}

func (m *imagesModel) Title() string { return "Image" }
func (m *imagesModel) Init() tea.Cmd { return nil }
func (m *imagesModel) Keys() []key.Binding {
	return []key.Binding{b("enter", "aksi image"), b("p", "unduh image"), b("b", "build dari Dockerfile"), b("o", "muat dari berkas")}
}

func (m *imagesModel) HelpText() string {
	return "Image adalah cetakan berisi sistem file + program; container adalah image yang sedang dijalankan. " +
		"Image bisa didapat dengan tiga cara: diunduh dari registry (pull), dibangun dari Dockerfile (build), atau dimuat dari berkas arsip (load). " +
		"Sebaliknya, image bisa diunggah ke registry (push) atau disimpan jadi berkas untuk dipindahkan lewat USB/scp (save). " +
		"Command setara: docker images, docker pull, docker build, docker load, docker save"
}

type imagesMsg struct {
	owner  *imagesModel
	images []sysdocker.Image
}

func (m *imagesModel) reload() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, _, _ := r.Capture(ctx, c.Command("images", "--format", "{{json .}}"))
		return imagesMsg{owner: m, images: sysdocker.ParseImages(out)}
	}
}

func (m *imagesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	confirm := func(p run.Plan) (nav.Screen, tea.Cmd) { return m, nav.Push(runflow.Confirm(p, m.env.Deps)) }
	switch msg := msg.(type) {
	case imagesMsg:
		if msg.owner == m {
			m.setImages(msg.images)
		}
	case nav.RefreshMsg:
		return m, m.reload()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "enter":
			if m.table.Cursor < len(m.images) {
				m.selected = m.images[m.table.Cursor]
				return m, nav.Push(ask.New(ImageActionForm(m.selected)))
			}
		case "p":
			return m, nav.Push(ask.New(ask.Form{ID: "pull", Title: "Unduh image", Questions: []ask.Question{{
				ID: "image", Kind: ask.Text, Prompt: "Image yang diunduh?", Placeholder: "alpine:latest", Validate: sysdocker.ValidImage,
				Help: "Tanpa tag, docker memakai :latest. Cari nama image di hub.docker.com.",
			}}}))
		case "b":
			return m, nav.Push(ask.New(BuildForm(workDir())))
		case "o":
			return m, nav.Push(ask.New(LoadImageForm(workDir())))
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			a := r.Answers
			switch r.ID {
			case "pull":
				return confirm(m.client.PullPlan(strings.TrimSpace(a["image"].Value())))
			case "build":
				return confirm(m.client.BuildPlan(BuildSpecFromAnswers(a)))
			case "img-load":
				return confirm(m.client.LoadImagePlan(expandDir(a["file"].Value())))
			case "img-save":
				return confirm(m.client.SaveImagePlan([]string{m.selected.Ref()}, expandDir(a["file"].Value())))
			case "img-push":
				target := strings.TrimSpace(a["target"].Value())
				if prefix := strings.TrimSpace(a["registry"].Value()); prefix != "" && !strings.HasPrefix(target, prefix+"/") {
					target = prefix + "/" + target
				}
				return confirm(m.client.PushPlan(m.selected.Ref(), target))
			case "img-action":
				ref := m.selected.Ref()
				switch a["action"].Value() {
				case "shell":
					return confirm(m.client.RunShellPlan(ref, false, ""))
				case "shell-keep":
					return confirm(m.client.RunShellPlan(ref, true, strings.TrimSpace(a["name"].Value())))
				case "run":
					start := sysdocker.RunSpec{Image: ref}
					return m, nav.Push(ask.New(RunForm(m.client, m.env.Runner, start)))
				case "save":
					return m, nav.Push(ask.New(SaveImageForm(m.selected, workDir())))
				case "push":
					return m, nav.Push(ask.New(PushImageForm(m.selected)))
				case "rmi":
					return confirm(m.client.RemoveImagePlan(m.selected))
				}
			case "run":
				spec, err := SpecFromAnswers(a)
				if err != nil {
					m.message = "✗ " + err.Error()
					return m, nil
				}
				path := ""
				var recipes sysdocker.Recipes
				if a["save"].Yes() {
					if p, err := sysdocker.DefaultRecipePath(); err == nil {
						path = p
						recipes, _ = sysdocker.LoadRecipes(p)
					}
				}
				return confirm(m.client.RunPlan(spec, path, recipes))
			}
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " selesai."
				} else {
					m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
				}
			}
		}
		return m, m.reload()
	}
	return m, nil
}

func (m *imagesModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Image (%d)", len(m.images))) + "   " + t.Muted.Render("command setara: docker images")}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.images) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada image", "", "tekan p untuk mengunduh, mis. alpine", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ImageActionForm menawarkan aksi untuk satu image.
func ImageActionForm(img sysdocker.Image) ask.Form {
	inUse := ""
	if img.Containers != "" && img.Containers != "0" && img.Containers != "N/A" {
		inUse = "masih dipakai " + img.Containers + " container; hapus container-nya dulu"
	}
	return ask.Form{ID: "img-action", Title: img.Ref(), Questions: []ask.Question{
		{ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan " + img.Ref() + "?", Options: []ask.Option{
			{Value: "shell", Label: "Coba shell di image ini", Description: "Container sementara, dihapus saat keluar. Cocok untuk melihat-lihat isi image.", Recommended: true},
			{Value: "shell-keep", Label: "Shell, simpan container setelah keluar", Description: "Untuk install sesuatu lalu commit jadi image baru dari daftar container."},
			{Value: "run", Label: "Jalankan sebagai container", Description: "Wizard bertahap: port, volume, environment, lalu setting lanjutan bila perlu."},
			{Value: "push", Label: "Unggah ke registry", Description: "Beri tag alamat registry (mis. Nexus) lalu unggah dengan docker push."},
			{Value: "save", Label: "Simpan ke berkas arsip", Description: "Satu berkas .tar berisi image lengkap, untuk dipindahkan ke server lain tanpa registry."},
			{Value: "rmi", Label: "Hapus image", Description: "Membebaskan " + img.Size + ".", Disabled: inUse, Risk: risk.Caution},
		}},
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama container?", Placeholder: "eksperimen", Validate: sysdocker.ValidName,
			When: func(a ask.Answers) bool { return a["action"].Value() == "shell-keep" }},
	}}
}

// --- log ----------------------------------------------------------------------------------------------

type logsModel struct {
	env    shared.Env
	client sysdocker.Client
	ct     sysdocker.Container
	viewer ui.Viewer
	err    error
	loaded bool
}

type logsMsg struct {
	owner *logsModel
	out   string
	err   error
}

const logTail = 500

// shortTimestamp mengganti timestamp RFC3339Nano dari `docker logs -t` dengan waktu lokal ringkas.
func shortTimestamp(line string) string {
	ts, rest, ok := strings.Cut(line, " ")
	if !ok {
		ts, rest = line, ""
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return line
	}
	return ui.Current.Muted.Render(t.Local().Format("01-02 15:04:05")) + " " + rest
}

func newLogs(env shared.Env, c sysdocker.Client, ct sysdocker.Container) *logsModel {
	return &logsModel{env: env, client: c, ct: ct, viewer: ui.Viewer{Follow: true}}
}

func (m *logsModel) Title() string { return "Log " + m.ct.Name() }
func (m *logsModel) Keys() []key.Binding {
	return []key.Binding{b("↑↓", "gulir"), b("r", "muat ulang")}
}

func (m *logsModel) Init() tea.Cmd {
	c, r, id := m.client, m.env.Runner, m.ct.Name()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := c.Logs(ctx, r, id, logTail)
		return logsMsg{owner: m, out: out, err: err}
	}
}

func (m *logsModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case logsMsg:
		if msg.owner == m {
			m.loaded, m.err = true, msg.err
			var lines []string
			if msg.out != "" {
				for _, l := range strings.Split(msg.out, "\n") {
					lines = append(lines, shortTimestamp(l))
				}
			}
			m.viewer.SetLines(lines)
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case tea.KeyPressMsg:
		m.viewer.HandleKey(msg.String())
	}
	return m, nil
}

func (m *logsModel) View(width, height int) string {
	t := ui.Current
	top := []string{"", " " + t.Muted.Render(fmt.Sprintf("command setara: docker logs --tail %d -t %s   (ikuti langsung: docker logs -f %s)", logTail, m.ct.Name(), m.ct.Name()))}
	if m.err != nil {
		top = append(top, ui.Wrap(t.Danger.Render("✗ "+m.err.Error()), width, " "))
	}
	top = append(top, "")
	s := strings.Join(top, "\n")
	switch {
	case !m.loaded:
		return s + "\n " + t.Subtle.Render("Membaca log…")
	case len(m.viewer.Lines) == 0:
		return s + "\n" + ui.EmptyState("Log kosong", "", "program di container belum menulis output", width)
	}
	return s + "\n" + m.viewer.View(width, height-lipgloss.Height(s), false)
}
