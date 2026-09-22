package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// volumesModel adalah layar volume & network: dua daftar yang bisa ditukar dengan tab.
type volumesModel struct {
	env        shared.Env
	client     sysdocker.Client
	containers []sysdocker.Container
	volumes    []sysdocker.Volume
	networks   []sysdocker.Network
	showNet    bool
	table      ui.Table
	loaded     bool
	message    string
	selVol     sysdocker.Volume
	selNet     sysdocker.Network
}

type volumesMsg struct {
	owner    *volumesModel
	volumes  []sysdocker.Volume
	networks []sysdocker.Network
}

// NewVolumes membuka layar volume & network.
func NewVolumes(env shared.Env, c sysdocker.Client, containers []sysdocker.Container) *volumesModel {
	return &volumesModel{env: env, client: c, containers: containers}
}

func (m *volumesModel) Title() string { return "Volume & Network" }

func (m *volumesModel) Init() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return volumesMsg{owner: m, volumes: c.ReadVolumes(ctx, r), networks: c.ReadNetworks(ctx, r)}
	}
}

func (m *volumesModel) Keys() []key.Binding {
	if m.showNet {
		return []key.Binding{b("tab", "lihat volume"), b("enter", "aksi network"), b("a", "buat network")}
	}
	return []key.Binding{b("tab", "lihat network"), b("enter", "aksi volume"), b("a", "buat volume")}
}

func (m *volumesModel) HelpText() string {
	return "Volume adalah penyimpanan yang umurnya terpisah dari container: container boleh dihapus dan dibuat ulang, isi volume tetap. " +
		"Semua data yang perlu bertahan (database, upload user) harus berada di volume. " +
		"Network adalah jaringan privat antar container: container di satu network saling memanggil lewat nama container, " +
		"tanpa perlu membuka port ke server. " +
		"Command setara: docker volume ls, docker network ls, docker system df -v"
}

func (m *volumesModel) fill() {
	if m.showNet {
		m.table.Columns = []ui.Column{
			{Title: "Network", Width: 20, Flex: 2},
			{Title: "Driver", Width: 10},
			{Title: "Dipakai container", Width: 24, Flex: 3},
		}
		rows := make([][]string, len(m.networks))
		for i, n := range m.networks {
			users := strings.Join(sysdocker.NetworkUsers(n.Name, m.containers), ", ")
			if n.Builtin() {
				users = "(bawaan docker) " + users
			}
			rows[i] = []string{n.Name, n.Driver, users}
		}
		m.table.SetRows(rows)
		m.table.CellStyle = nil
		return
	}
	m.table.Columns = []ui.Column{
		{Title: "Volume", Width: 22, Flex: 3},
		{Title: "Dipakai container", Width: 22, Flex: 3},
		{Title: "Status", Width: 12, Flex: 1},
	}
	rows := make([][]string, len(m.volumes))
	for i, v := range m.volumes {
		users := sysdocker.VolumeUsers(v.Name, m.containers)
		status := "dipakai"
		if len(users) == 0 {
			status = "menganggur"
		}
		rows[i] = []string{v.Name, strings.Join(users, ", "), status}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if col == 2 && row < len(m.volumes) && len(sysdocker.VolumeUsers(m.volumes[row].Name, m.containers)) == 0 {
			return ui.Current.Muted
		}
		return lipgloss.NewStyle()
	}
}

func (m *volumesModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *volumesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case volumesMsg:
		if msg.owner == m {
			m.loaded, m.volumes, m.networks = true, msg.volumes, msg.networks
			m.fill()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "tab":
			m.showNet = !m.showNet
			m.table.Cursor = 0
			m.fill()
		case "a":
			return m, nav.Push(ask.New(m.createForm()))
		case "enter":
			return m.openAction()
		}
	}
	return m, nil
}

func (m *volumesModel) createForm() ask.Form {
	if m.showNet {
		return ask.Form{ID: "net-add", Title: "Buat network", SkipReview: true, Questions: []ask.Question{{
			ID: "name", Kind: ask.Text, Prompt: "Nama network baru?", Placeholder: "app-net", Validate: sysdocker.ValidName,
			Help: "Setelah dibuat, jalankan container dengan --network " + "app-net" + " (tersedia di setting lanjutan wizard container). Container di dalamnya saling memanggil lewat nama container sebagai hostname.",
		}}}
	}
	return ask.Form{ID: "vol-add", Title: "Buat volume", SkipReview: true, Questions: []ask.Question{{
		ID: "name", Kind: ask.Text, Prompt: "Nama volume baru?", Placeholder: "dbdata", Validate: sysdocker.ValidName,
		Help: "Pakai nama yang menjelaskan isinya, mis. dbdata atau uploads. Volume dipasang ke container dengan -v NAMA:/path/di/container.",
	}}}
}

func (m *volumesModel) openAction() (nav.Screen, tea.Cmd) {
	if m.showNet {
		if m.table.Cursor >= len(m.networks) {
			return m, nil
		}
		m.selNet = m.networks[m.table.Cursor]
		users := sysdocker.NetworkUsers(m.selNet.Name, m.containers)
		return m, nav.Push(ask.New(NetworkActionForm(m.selNet, users)))
	}
	if m.table.Cursor >= len(m.volumes) {
		return m, nil
	}
	m.selVol = m.volumes[m.table.Cursor]
	users := sysdocker.VolumeUsers(m.selVol.Name, m.containers)
	return m, nav.Push(ask.New(VolumeActionForm(m.selVol, users)))
}

func (m *volumesModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		a := r.Answers
		switch r.ID {
		case "vol-add":
			return m.confirm(m.client.CreateVolumePlan(strings.TrimSpace(a["name"].Value())))
		case "net-add":
			return m.confirm(m.client.CreateNetworkPlan(strings.TrimSpace(a["name"].Value())))
		case "vol-action":
			switch a["action"].Value() {
			case "backup":
				return m, nav.Push(ask.New(BackupVolumeForm(m.selVol.Name, workDir())))
			case "restore":
				return m, nav.Push(ask.New(RestoreVolumeForm(m.selVol.Name, workDir())))
			case "rm":
				return m.confirm(m.client.RemoveVolumePlan(m.selVol, sysdocker.VolumeUsers(m.selVol.Name, m.containers)))
			case "prune":
				return m.confirm(m.client.PruneVolumesPlan())
			}
		case "net-action":
			if a["action"].Value() == "rm" {
				return m.confirm(m.client.RemoveNetworkPlan(m.selNet, sysdocker.NetworkUsers(m.selNet.Name, m.containers)))
			}
		case "vol-backup":
			dir := expandDir(a["dir"].Value())
			file := strings.TrimSpace(a["file"].Value())
			return m.confirm(m.client.BackupVolumePlan(m.selVol.Name, dir, file))
		case "vol-restore":
			path := expandDir(a["file"].Value())
			return m.confirm(m.client.RestoreVolumePlan(m.selVol.Name, filepath.Dir(path), filepath.Base(path)))
		}
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
			}
		}
		return m, m.Init()
	}
	return m, nil
}

func (m *volumesModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca volume & network…")
	}
	title, cmdHint, count := "Volume", "docker volume ls", len(m.volumes)
	if m.showNet {
		title, cmdHint, count = "Network", "docker network ls", len(m.networks)
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("%s (%d)", title, count)) + "   " + t.Muted.Render("command setara: "+cmdHint)}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if count == 0 {
		return top + "\n" + ui.EmptyState("Belum ada "+strings.ToLower(title), "", "tekan a untuk membuat, tab untuk berpindah daftar", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// VolumeActionForm menawarkan aksi untuk satu volume.
func VolumeActionForm(v sysdocker.Volume, users []string) ask.Form {
	inUse := ""
	if len(users) > 0 {
		inUse = "masih dipakai " + strings.Join(users, ", ") + "; hentikan & hapus containernya dulu"
	}
	return ask.Form{ID: "vol-action", Title: v.Name, SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan volume " + v.Name + "?",
		Options: []ask.Option{
			{Value: "backup", Label: "Cadangkan isinya jadi arsip", Description: "Menyalin seluruh isi volume ke satu berkas .tar.gz di server ini.", Recommended: true},
			{Value: "restore", Label: "Pulihkan dari arsip", Description: "Menuangkan isi arsip ke dalam volume; berkas dengan nama sama ditimpa.", Risk: risk.Dangerous},
			{Value: "rm", Label: "Hapus volume", Description: "Seluruh data di dalamnya hilang permanen.", Disabled: inUse, Risk: risk.Dangerous},
			{Value: "prune", Label: "Hapus semua volume menganggur", Description: "Menghapus SEMUA volume yang tidak sedang dipakai container mana pun.", Risk: risk.Dangerous},
		},
	}}}
}

// NetworkActionForm menawarkan aksi untuk satu network.
func NetworkActionForm(n sysdocker.Network, users []string) ask.Form {
	reason := ""
	switch {
	case n.Builtin():
		reason = "network bawaan docker tidak bisa dihapus"
	case len(users) > 0:
		reason = "masih dipakai " + strings.Join(users, ", ")
	}
	return ask.Form{ID: "net-action", Title: n.Name, SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan network " + n.Name + "?",
		Options: []ask.Option{
			{Value: "rm", Label: "Hapus network", Description: "Container yang memakainya kehilangan jalur komunikasi antar-nama.", Disabled: reason, Risk: risk.Caution},
		},
	}}}
}

// BackupVolumeForm menanyakan tujuan arsip cadangan volume.
func BackupVolumeForm(name, dir string) ask.Form {
	file := name + "-" + time.Now().Format("2006-01-02-1504") + ".tar.gz"
	return ask.Form{ID: "vol-backup", Title: "Cadangkan volume " + name, Questions: []ask.Question{
		{ID: "dir", Header: "Direktori", Kind: ask.Text, Prompt: "Simpan arsip di direktori mana?", Default: []string{dir}, Validate: validDir,
			Help: "Direktori ini dipasang ke container sementara yang melakukan pengarsipan. Pastikan ruang disknya cukup — arsip bisa sebesar isi volume."},
		{ID: "file", Header: "Berkas", Kind: ask.Text, Prompt: "Nama berkas arsip?", Default: []string{file}, Validate: validFileName,
			Help: "Menyertakan tanggal membantu membedakan cadangan. Salin berkasnya ke server lain supaya tetap aman bila server ini rusak."},
	}}
}

// RestoreVolumeForm menanyakan arsip yang akan dituangkan ke volume.
func RestoreVolumeForm(name, dir string) ask.Form {
	return ask.Form{ID: "vol-restore", Title: "Pulihkan volume " + name, SkipReview: true, Questions: []ask.Question{
		{ID: "file", Header: "Arsip", Kind: ask.Text, Prompt: "Path arsip .tar.gz yang dipulihkan?", Default: []string{dir + "/"}, Validate: validFile,
			Help: "Arsip hasil fitur cadangkan volume. Hentikan dulu container yang memakai volume ini supaya tidak menulis bersamaan."},
	}}
}

// validFile memeriksa berkas yang harus ada.
func validFile(s string) error {
	s = expandDir(s)
	fi, err := os.Stat(s)
	if err != nil {
		return errors.New("berkas tidak ditemukan: " + s)
	}
	if fi.IsDir() {
		return errors.New(s + " adalah direktori, bukan berkas")
	}
	return nil
}

// validFileName memeriksa nama berkas (tanpa direktori).
func validFileName(s string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return errors.New("nama berkas wajib diisi")
	case strings.ContainsAny(s, "/ \t"):
		return errors.New("tulis nama berkasnya saja, tanpa direktori dan tanpa spasi")
	}
	return nil
}
