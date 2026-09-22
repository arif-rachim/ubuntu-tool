package postgres

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	sysres "github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// tuningModel menampilkan parameter yang sedang berlaku dan usulan nilai baru dari kalkulator.
type tuningModel struct {
	env      shared.Env
	client   syspg.Client
	cluster  syspg.Cluster
	settings []syspg.Setting
	tuned    []syspg.Tuned
	notes    []string
	server   syspg.Server
	loaded   bool
	err      string
	message  string
	viewer   ui.Viewer
}

type tuningMsg struct {
	owner    *tuningModel
	settings []syspg.Setting
	err      error
}

// NewTuning membuka layar parameter & penyetelan server.
func NewTuning(env shared.Env, c syspg.Client, cl syspg.Cluster) *tuningModel {
	return &tuningModel{env: env, client: c, cluster: cl,
		server: syspg.Server{RAMBytes: totalRAM(env.ProcRoot), CPUs: runtime.NumCPU(), SSD: true,
			Workload: syspg.WorkloadWeb, Major: cl.Major()}}
}

// totalRAM membaca total RAM server dari /proc/meminfo.
func totalRAM(procRoot string) int64 {
	if procRoot == "" {
		procRoot = "/proc"
	}
	b, err := os.ReadFile(procRoot + "/meminfo")
	if err != nil {
		return 0
	}
	return sysres.ParseMeminfo(string(b)).TotalKB * 1024
}

func (m *tuningModel) Title() string { return "Setelan" }

func (m *tuningModel) Init() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ss, err := c.ReadSettings(ctx, r)
		return tuningMsg{owner: m, settings: ss, err: err}
	}
}

func (m *tuningModel) Keys() []key.Binding {
	return []key.Binding{b("h", "hitung usulan"), b("enter", "terapkan usulan"), b("↑↓", "gulir")}
}

func (m *tuningModel) HelpText() string {
	return "PostgreSQL bawaan Ubuntu disetel supaya bisa jalan di mesin sekecil apa pun, bukan supaya cepat di mesinmu. " +
		"Kalkulator ini memakai pedoman umum: sekitar seperempat RAM untuk shared_buffers (cache milik PostgreSQL), " +
		"sisanya dibagi menurut jumlah koneksi. Hasilnya titik awal yang waras, bukan pengganti pengukuran beban aslimu. " +
		"ubt menulisnya ke berkas terpisah di conf.d — postgresql.conf bawaan tidak pernah diubah, jadi membatalkan cukup dengan menghapus berkas itu lalu restart. " +
		"Kolom \"asal\" menunjukkan dari mana nilai sekarang berasal: default (bawaan), configuration file, atau override. " +
		"Command setara: sudo -u postgres psql -c 'SELECT name, setting, source FROM pg_settings'"
}

func (m *tuningModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *tuningModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tuningMsg:
		if msg.owner == m {
			m.loaded, m.settings = true, msg.settings
			m.err = ""
			if msg.err != nil {
				m.err = msg.err.Error()
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
		case "h":
			return m, nav.Push(ask.New(TuningForm(m.server)))
		case "enter":
			if len(m.tuned) == 0 {
				m.message = "Tekan h dulu untuk menghitung usulan sesuai mesin & beban kerjamu."
				return m, nil
			}
			return m.confirm(syspg.ApplySettingsPlan(m.cluster, m.tuned))
		}
	}
	return m, nil
}

func (m *tuningModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled || r.ID != "pg-tune" {
			return m, nil
		}
		a := r.Answers
		m.server.Workload = a["workload"].Value()
		m.server.SSD = a["disk"].Value() == "ssd"
		if v, err := strconv.Atoi(strings.TrimSpace(a["maxconn"].Value())); err == nil {
			m.server.MaxConn = v
		}
		m.tuned = syspg.Tune(m.server)
		m.notes = syspg.Notes(m.server)
		m.applyCurrent()
		m.message = "Usulan dihitung. Tekan enter untuk menerapkannya."
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

// applyCurrent mengisi nilai yang sedang berlaku ke daftar usulan.
func (m *tuningModel) applyCurrent() {
	cur := map[string]syspg.Setting{}
	for _, s := range m.settings {
		cur[s.Name] = s
	}
	for i, t := range m.tuned {
		if s, ok := cur[t.Name]; ok {
			m.tuned[i].Current = settingValue(s)
		}
	}
}

// settingValue menggabungkan nilai & satuan pg_settings menjadi bentuk yang dipakai postgresql.conf.
func settingValue(s syspg.Setting) string {
	switch s.Unit {
	case "8kB":
		if n, err := strconv.ParseInt(s.Value, 10, 64); err == nil {
			return strconv.FormatInt(n*8/1024, 10) + "MB"
		}
	case "kB":
		if n, err := strconv.ParseInt(s.Value, 10, 64); err == nil {
			return strconv.FormatInt(n, 10) + "kB"
		}
	case "MB":
		return s.Value + "MB"
	}
	return s.Value
}

func (m *tuningModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca parameter server…")
	}
	var lines []string
	add := func(s string) { lines = append(lines, ui.Wrap(s, width, " ")) }
	ramGB := float64(m.server.RAMBytes) / float64(1<<30)
	add("")
	add(" " + t.Title.Render("Mesin ini") + "  " + t.Subtle.Render(fmt.Sprintf("RAM %.1f GB · %d inti CPU · PostgreSQL %s", ramGB, m.server.CPUs, m.cluster.ID())))
	if m.err != "" {
		add(t.Danger.Render(" ✗ " + m.err))
	}
	if m.message != "" {
		add(t.Accent.Render(" " + m.message))
	}
	add("")

	if len(m.tuned) == 0 {
		add(" " + t.Title.Render("Parameter yang berlaku sekarang"))
		add("")
		for _, s := range m.settings {
			add(fmt.Sprintf(" %-34s %-16s %s", s.Name, s.Display(), t.Muted.Render("asal: "+s.Source)))
			if s.PendingRestart {
				add("   " + t.Warning.Render("nilai di berkas konfigurasi sudah berubah tetapi belum berlaku — butuh restart"))
			}
		}
		add("")
		add(" " + t.Subtle.Render("Tekan h untuk menghitung usulan nilai sesuai mesin & jenis beban kerjamu."))
		m.viewer.SetLines(lines)
		return m.viewer.View(width, height, false)
	}

	add(" " + t.Title.Render("Usulan penyetelan") + "  " + t.Subtle.Render("beban: "+workloadLabel(m.server.Workload)))
	add("")
	add(" " + t.Subtle.Render(fmt.Sprintf(" %-32s %-14s %-14s", "PARAMETER", "SEKARANG", "USULAN")))
	for _, s := range m.tuned {
		cur := s.Current
		if cur == "" {
			cur = "—"
		}
		mark, style := "  ", t.Muted
		if s.Changed() {
			mark, style = "→ ", t.Accent
		}
		add(" " + mark + style.Render(fmt.Sprintf("%-32s %-14s %-14s", s.Name, cur, s.Value)))
		add("   " + t.Subtle.Render(s.Why))
	}
	add("")
	for _, n := range m.notes {
		add(" " + t.Warning.Render("• ") + t.Subtle.Render(n))
	}
	add("")
	add(" " + t.Subtle.Render("Tekan enter untuk menulis nilai ini ke "+m.cluster.DropIn(syspg.DropInTuning)+" (postgresql.conf bawaan tidak diubah)."))
	m.viewer.SetLines(lines)
	return m.viewer.View(width, height, false)
}

func workloadLabel(w string) string {
	switch w {
	case syspg.WorkloadDW:
		return "laporan & analitik"
	case syspg.WorkloadMixed:
		return "campuran"
	}
	return "aplikasi web/API"
}

// TuningForm menanyakan jenis beban kerja dan jenis disk.
func TuningForm(s syspg.Server) ask.Form {
	return ask.Form{ID: "pg-tune", Title: "Hitung penyetelan", Questions: []ask.Question{
		{ID: "workload", Header: "Beban", Kind: ask.Single, Prompt: "Database ini dipakai untuk apa?", Options: []ask.Option{
			{Value: syspg.WorkloadWeb, Label: "Aplikasi web / API", Description: "Banyak koneksi, query pendek, sering diulang.", Recommended: true},
			{Value: syspg.WorkloadMixed, Label: "Campuran", Description: "Aplikasi sehari-hari plus laporan sesekali."},
			{Value: syspg.WorkloadDW, Label: "Laporan & analitik", Description: "Koneksi sedikit, query besar yang memindai banyak baris."},
		}},
		{ID: "disk", Header: "Disk", Kind: ask.Single, Prompt: "Data disimpan di disk jenis apa?", Options: []ask.Option{
			{Value: "ssd", Label: "SSD / NVMe", Description: "Hampir semua VPS dan server baru. Membaca acak hampir secepat berurutan.", Recommended: true},
			{Value: "hdd", Label: "Hard disk berputar", Description: "Membaca acak jauh lebih mahal; perencana query dibuat lebih hati-hati memakai index."},
		}},
		{ID: "maxconn", Header: "Koneksi", Kind: ask.Text, Optional: true, Prompt: "Berapa koneksi bersamaan yang perlu dilayani?",
			Placeholder: "kosongkan untuk saran ubt", Validate: validMaxConn,
			Help: "Tiap koneksi memakai memori sendiri, jadi angka besar justru memperlambat. Bila aplikasimu memakai connection pool " +
				"(PgBouncer, pool bawaan framework), angka 100 biasanya lebih dari cukup."},
	}}
}

func validMaxConn(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 5 || n > 10000 {
		return queryError("isi angka 5–10000, mis. 100")
	}
	if n > 500 {
		return ask.Warn("Lebih dari 500 koneksi langsung ke PostgreSQL hampir selalu lebih lambat daripada memakai connection pool. Tekan enter lagi bila yakin.")
	}
	return nil
}
