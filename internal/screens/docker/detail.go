package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdocker "github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// --- statistik ----------------------------------------------------------------------------------

// statsModel menampilkan pemakaian CPU & RAM tiap container yang berjalan.
type statsModel struct {
	env    shared.Env
	client sysdocker.Client
	stats  []sysdocker.Stat
	table  ui.Table
	loaded bool
	err    string
}

type statsMsg struct {
	owner *statsModel
	stats []sysdocker.Stat
	err   error
}

// NewStats membuka layar pemakaian CPU & RAM per container.
func NewStats(env shared.Env, c sysdocker.Client) *statsModel {
	return &statsModel{env: env, client: c, table: ui.Table{Columns: []ui.Column{
		{Title: "Container", Width: 18, Flex: 2},
		{Title: "CPU", Width: 8, Right: true},
		{Title: "RAM", Width: 18, Flex: 1},
		{Title: "RAM %", Width: 7, Right: true},
		{Title: "Jaringan", Width: 16, Flex: 1},
		{Title: "Disk I/O", Width: 16, Flex: 1},
	}}}
}

func (m *statsModel) Title() string { return "Statistik" }

func (m *statsModel) Init() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ss, err := c.ReadStats(ctx, r)
		return statsMsg{owner: m, stats: ss, err: err}
	}
}

func (m *statsModel) Keys() []key.Binding {
	return []key.Binding{b("enter", "periksa container"), b("r", "muat ulang")}
}

func (m *statsModel) HelpText() string {
	return "Angka ini diambil sekali saat layar dibuka (docker stats --no-stream), bukan terus-menerus — tekan r untuk memperbarui. " +
		"CPU dihitung terhadap satu inti: 100% berarti satu inti penuh terpakai, dan bisa lebih dari 100% bila container memakai beberapa inti. " +
		"RAM % dihitung terhadap batas memori container (bila di-set dengan -m) atau terhadap RAM server. " +
		"Command setara: docker stats"
}

func (m *statsModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statsMsg:
		if msg.owner == m {
			m.loaded, m.stats = true, msg.stats
			m.err = ""
			if msg.err != nil {
				m.err = msg.err.Error()
			}
			rows := make([][]string, len(m.stats))
			for i, s := range m.stats {
				rows[i] = []string{s.Name, s.CPUPerc, s.MemUsage, s.MemPerc, s.NetIO, s.BlockIO}
			}
			m.table.SetRows(rows)
			m.table.CellStyle = func(row, col int) lipgloss.Style {
				if row >= len(m.stats) {
					return lipgloss.NewStyle()
				}
				t := ui.Current
				switch {
				case col == 1 && sysdocker.Percent(m.stats[row].CPUPerc) > 80:
					return t.Warning
				case col == 3 && sysdocker.Percent(m.stats[row].MemPerc) > 85:
					return t.Danger
				}
				return lipgloss.NewStyle()
			}
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "enter" && m.table.Cursor < len(m.stats) {
			return m, nav.Push(NewDetail(m.env, m.client, m.stats[m.table.Cursor].Name))
		}
	}
	return m, nil
}

func (m *statsModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Mengukur pemakaian container…")
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Pemakaian container (%d berjalan)", len(m.stats))) + "   " + t.Muted.Render("command setara: docker stats")}
	if m.err != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.stats) == 0 {
		return top + "\n" + ui.EmptyState("Tidak ada container yang berjalan", "docker stats hanya menampilkan container yang sedang hidup.", "", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// --- detail & diagnosa --------------------------------------------------------------------------

// detailModel menampilkan isi `docker inspect` dalam bahasa manusia beserta temuan diagnosa.
type detailModel struct {
	env      shared.Env
	client   sysdocker.Client
	name     string
	info     sysdocker.Inspect
	findings []sysdocker.Finding
	loaded   bool
	err      string
	message  string
	viewer   ui.Viewer
	recipes  sysdocker.Recipes
	path     string
}

type detailMsg struct {
	owner    *detailModel
	info     sysdocker.Inspect
	findings []sysdocker.Finding
	err      error
}

// NewDetail membuka layar periksa & diagnosa satu container.
func NewDetail(env shared.Env, c sysdocker.Client, name string) *detailModel {
	m := &detailModel{env: env, client: c, name: name}
	if p, err := sysdocker.DefaultRecipePath(); err == nil {
		m.path = p
		m.recipes, _ = sysdocker.LoadRecipes(p)
	}
	return m
}

func (m *detailModel) Title() string { return "Periksa " + m.name }

func (m *detailModel) now() time.Time {
	if m.env.Now != nil {
		return m.env.Now()
	}
	return time.Now()
}

func (m *detailModel) Init() tea.Cmd {
	c, r, name := m.client, m.env.Runner, m.name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := c.ReadInspect(ctx, r, name)
		if err != nil {
			return detailMsg{owner: m, err: err}
		}
		logs, _ := c.Logs(ctx, r, name, 50)
		return detailMsg{owner: m, info: info, findings: sysdocker.Troubleshoot(info, logs)}
	}
}

func (m *detailModel) Keys() []key.Binding {
	return []key.Binding{b("c", "buat ulang container"), b("s", "simpan resep"), b("↑↓", "gulir")}
}

func (m *detailModel) HelpText() string {
	return "Layar ini membaca `docker inspect` — catatan lengkap docker tentang container: dari image apa, port & volume apa yang dipasang, " +
		"berapa kali sudah restart, dan kenapa terakhir berhenti. " +
		"\"Buat ulang\" berguna setelah image diperbarui: setting yang sama dipakai lagi untuk container baru, dan isi volume tetap aman. " +
		"Command setara: docker inspect " + m.name
}

func (m *detailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detailMsg:
		if msg.owner == m {
			m.loaded = true
			if msg.err != nil {
				m.err = msg.err.Error()
			} else {
				m.info, m.findings = msg.info, msg.findings
			}
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	case tea.KeyPressMsg:
		if m.viewer.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "c":
			if !m.loaded || m.err != "" {
				return m, nil
			}
			return m, nav.Push(ask.New(RunForm(m.client, m.env.Runner, sysdocker.SpecFromInspect(m.info))))
		case "s":
			if !m.loaded || m.err != "" || m.path == "" {
				return m, nil
			}
			spec := sysdocker.SpecFromInspect(m.info)
			spec.Updated = time.Now()
			return m, nav.Push(runflow.Confirm(
				run.Plan{Title: "Simpan resep container " + spec.Name, Steps: []run.Command{sysdocker.SaveRecipesCommand(m.path, m.recipes.Upsert(spec))}},
				m.env.Deps))
		}
	}
	return m, nil
}

func (m *detailModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled || r.ID != "run" {
			return m, nil
		}
		spec, err := SpecFromAnswers(r.Answers)
		if err != nil {
			m.message = "✗ " + err.Error()
			return m, nil
		}
		return m, nav.Push(runflow.Confirm(m.client.RecreatePlan(spec, true, true), m.env.Deps))
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
				if p, err := sysdocker.LoadRecipes(m.path); err == nil {
					m.recipes = p
				}
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
			}
		}
		return m, m.Init()
	}
	return m, nil
}

func (m *detailModel) lines(width int) []string {
	t := ui.Current
	i := m.info
	var out []string
	add := func(s string) { out = append(out, ui.Wrap(s, width, " ")) }
	field := func(label, value string) {
		if value != "" {
			add(t.Subtle.Render(fmt.Sprintf("%-14s", label)) + value)
		}
	}

	state := i.State.Status
	style := t.Success
	if !i.State.Running {
		state = fmt.Sprintf("%s (exit %d — %s)", i.State.Status, i.State.ExitCode, sysdocker.ExitMeaning(i.State.ExitCode))
		style = t.Warning
	}
	add(" " + t.Title.Render(i.ShortName()) + "  " + style.Render(state))
	add("")
	field("Image", i.Config.Image)
	field("Dibuat", sysdocker.Age(i.Created, m.now())+" lalu")
	if i.State.Running {
		field("Berjalan", sysdocker.Age(i.State.StartedAt, m.now()))
	}
	if i.RestartCount > 0 {
		field("Restart", fmt.Sprintf("%d kali", i.RestartCount))
	}
	policy := i.HostConfig.RestartPolicy.Name
	if policy == "" {
		policy = "no — tidak dijalankan ulang otomatis"
	}
	field("Restart otomatis", policy)
	if h := i.State.Health; h != nil {
		field("Healthcheck", h.Status)
	}

	var ports []string
	for ctrPort, binds := range i.HostConfig.PortBindings {
		for _, bind := range binds {
			host := bind.HostIP
			if host == "" || host == "0.0.0.0" {
				host = "semua alamat"
			}
			ports = append(ports, host+":"+bind.HostPort+" → "+ctrPort)
		}
	}
	if len(ports) > 0 {
		field("Port", strings.Join(ports, ", "))
	}
	for _, mnt := range i.Mounts {
		src := mnt.Source
		kind := "direktori server"
		if mnt.Type == "volume" {
			src, kind = mnt.Name, "volume"
		}
		mode := ""
		if !mnt.RW {
			mode = " (hanya baca)"
		}
		field("Data", kind+" "+src+" → "+mnt.Destination+mode)
	}
	if i.HostConfig.Memory > 0 {
		field("Batas RAM", fmt.Sprintf("%d MB", i.HostConfig.Memory/(1<<20)))
	}
	if i.Config.WorkingDir != "" {
		field("Workdir", i.Config.WorkingDir)
	}
	if len(i.Config.Cmd) > 0 {
		field("Command", strings.Join(i.Config.Cmd, " "))
	}
	if n := i.HostConfig.NetworkMode; n != "" {
		field("Network", n)
	}

	out = append(out, "")
	if len(m.findings) == 0 {
		add(" " + t.Success.Render("✓ Tidak ada masalah yang terdeteksi dari inspect & 50 baris log terakhir."))
		return out
	}
	add(" " + t.Title.Render("Temuan"))
	for _, f := range m.findings {
		add("")
		add(" " + t.Warning.Render("• "+f.Symptom))
		add(ui.Wrap(t.Subtle.Render(f.Why), width, "   "))
		add(ui.Wrap(t.Accent.Render("→ "+f.Fix), width, "   "))
	}
	return out
}

func (m *detailModel) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Memeriksa container…")
	case m.err != "":
		return "\n" + ui.Wrap(t.Danger.Render(" ✗ "+m.err), width, " ")
	}
	lines := m.lines(width)
	if m.message != "" {
		lines = append([]string{ui.Wrap(t.Accent.Render(m.message), width, " ")}, lines...)
	}
	m.viewer.SetLines(lines)
	return m.viewer.View(width, height, false)
}
