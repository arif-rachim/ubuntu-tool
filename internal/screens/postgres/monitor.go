package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// monitorModel menampilkan koneksi yang sedang berjalan dan query paling berat.
type monitorModel struct {
	env        shared.Env
	client     syspg.Client
	status     syspg.Status
	dbs        []string
	activity   []syspg.Activity
	statements []syspg.Statement
	showStmt   bool
	table      ui.Table
	loaded     bool
	err        string
	message    string
	selected   syspg.Activity
}

type monitorMsg struct {
	owner      *monitorModel
	activity   []syspg.Activity
	statements []syspg.Statement
	err        error
}

// NewMonitor membuka layar koneksi & query paling berat.
func NewMonitor(env shared.Env, c syspg.Client, st syspg.Status, dbs []string) *monitorModel {
	return &monitorModel{env: env, client: c, status: st, dbs: dbs}
}

func (m *monitorModel) Title() string { return "Monitor" }

func (m *monitorModel) Init() tea.Cmd {
	c, r, stmt := m.client, m.env.Runner, m.status.HasStatements
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		act, err := c.ReadActivity(ctx, r)
		msg := monitorMsg{owner: m, activity: act, err: err}
		if stmt {
			msg.statements, _ = c.ReadStatements(ctx, r)
		}
		return msg
	}
}

func (m *monitorModel) Keys() []key.Binding {
	if m.showStmt {
		return []key.Binding{b("tab", "lihat koneksi"), b("r", "muat ulang")}
	}
	keys := []key.Binding{b("tab", "lihat query berat"), b("enter", "aksi koneksi"), b("r", "muat ulang")}
	if !m.status.HasStatements {
		keys = append(keys, b("a", "aktifkan pencatat query"))
	}
	return keys
}

func (m *monitorModel) HelpText() string {
	return "Koneksi bertuliskan \"idle in transaction\" lebih dari sebentar adalah masalah: transaksi yang dibuka tetapi tidak dipakai " +
		"menahan lock dan menghalangi autovacuum membersihkan baris mati. Biasanya penyebabnya aplikasi lupa commit/rollback. " +
		"Kolom \"menunggu\" berisi PID koneksi lain yang memegang lock — koneksi itulah yang perlu diurus, bukan yang menunggu. " +
		"Daftar query berat berasal dari pg_stat_statements: angkanya terkumpul sejak statistik terakhir direset, jadi query yang sering dipanggil " +
		"bisa menempati urutan atas walau masing-masing cepat. " +
		"Command setara: sudo -u postgres psql -c 'SELECT * FROM pg_stat_activity'"
}

func (m *monitorModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *monitorModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorMsg:
		if msg.owner == m {
			m.loaded, m.activity, m.statements = true, msg.activity, msg.statements
			m.err = ""
			if msg.err != nil {
				m.err = msg.err.Error()
			}
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
			m.showStmt = !m.showStmt
			m.table.Cursor = 0
			m.fill()
		case "a":
			if !m.status.HasStatements {
				return m.confirm(syspg.EnableStatementsPlan(m.status.Selected()))
			}
		case "enter":
			if !m.showStmt && m.table.Cursor < len(m.activity) {
				m.selected = m.activity[m.table.Cursor]
				return m, nav.Push(ask.New(ActivityActionForm(m.selected)))
			}
		}
	}
	return m, nil
}

func (m *monitorModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled || r.ID != "act-action" {
			return m, nil
		}
		switch r.Answers["action"].Value() {
		case "cancel":
			return m.confirm(m.client.CancelPlan(m.selected.PID))
		case "terminate":
			who := m.selected.User + "@" + m.selected.Database
			return m.confirm(m.client.TerminatePlan(m.selected.PID, who))
		}
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal."
			}
		}
		return m, m.Init()
	}
	return m, nil
}

func (m *monitorModel) fill() {
	if m.showStmt {
		m.table.Columns = []ui.Column{
			{Title: "Panggilan", Width: 10, Right: true},
			{Title: "Total", Width: 10, Right: true},
			{Title: "Rata-rata", Width: 10, Right: true},
			{Title: "Query", Width: 40, Flex: 4},
		}
		rows := make([][]string, len(m.statements))
		for i, s := range m.statements {
			rows[i] = []string{fmt.Sprint(s.Calls), millis(s.TotalMs), millis(s.MeanMs), s.Query}
		}
		m.table.SetRows(rows)
		m.table.CellStyle = func(row, col int) lipgloss.Style {
			if col == 2 && row < len(m.statements) && m.statements[row].MeanMs > 500 {
				return ui.Current.Warning
			}
			return lipgloss.NewStyle()
		}
		return
	}
	m.table.Columns = []ui.Column{
		{Title: "PID", Width: 7, Right: true},
		{Title: "Role", Width: 12, Flex: 1},
		{Title: "Database", Width: 12, Flex: 1},
		{Title: "Status", Width: 18, Flex: 1},
		{Title: "Lama", Width: 8, Right: true},
		{Title: "Query", Width: 30, Flex: 3},
	}
	rows := make([][]string, len(m.activity))
	for i, a := range m.activity {
		state := a.State
		if len(a.BlockedBy) > 0 {
			state = fmt.Sprintf("menunggu PID %v", a.BlockedBy)
		}
		rows[i] = []string{fmt.Sprint(a.PID), a.User, a.Database, state, secs(a.QuerySecs), a.Query}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if row >= len(m.activity) {
			return lipgloss.NewStyle()
		}
		t := ui.Current
		if m.activity[row].Problem() != "" && (col == 3 || col == 4) {
			return t.Warning
		}
		return lipgloss.NewStyle()
	}
}

func millis(ms float64) string {
	switch {
	case ms >= 60000:
		return fmt.Sprintf("%.1f mnt", ms/60000)
	case ms >= 1000:
		return fmt.Sprintf("%.1f dtk", ms/1000)
	}
	return fmt.Sprintf("%.0f ms", ms)
}

func secs(s int) string {
	if s <= 0 {
		return "—"
	}
	return shared.Duration(time.Duration(s) * time.Second)
}

func (m *monitorModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca kondisi server…")
	}
	title, count, hint := "Koneksi", len(m.activity), "command setara: sudo -u postgres psql -c 'SELECT * FROM pg_stat_activity'"
	if m.showStmt {
		title, count, hint = "Query paling berat", len(m.statements), "command setara: sudo -u postgres psql -c 'SELECT * FROM pg_stat_statements ORDER BY total_exec_time DESC'"
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("%s (%d)", title, count))}
	lines = append(lines, " "+t.Muted.Render(hint))
	if m.err != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	// Ringkasan temuan: koneksi yang perlu diperhatikan.
	if !m.showStmt {
		var notes []string
		for _, a := range m.activity {
			if p := a.Problem(); p != "" {
				notes = append(notes, fmt.Sprintf("PID %d %s", a.PID, p))
			}
		}
		if len(notes) > 0 {
			lines = append(lines, ui.Wrap(t.Warning.Render("⚠ "+strings.Join(notes, " · ")), width, " "))
		}
	}
	top := strings.Join(lines, "\n")
	if count == 0 {
		if m.showStmt && !m.status.HasStatements {
			return top + "\n" + ui.EmptyState("pg_stat_statements belum aktif",
				"Ekstensi ini merekam berapa kali tiap query dipanggil dan berapa lama totalnya — cara paling cepat menemukan query yang memberatkan server. "+
					"Mengaktifkannya perlu restart PostgreSQL sekali.", "tekan tab lalu a untuk mengaktifkan", width)
		}
		return top + "\n" + ui.EmptyState("Tidak ada "+strings.ToLower(title), "", "tekan r untuk memuat ulang", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ActivityActionForm menawarkan aksi untuk satu koneksi.
func ActivityActionForm(a syspg.Activity) ask.Form {
	notRunning := ""
	if a.State != "active" {
		notRunning = "koneksi ini tidak sedang menjalankan query"
	}
	desc := a.User + "@" + a.Database + " dari " + a.Client
	if p := a.Problem(); p != "" {
		desc += " — " + p
	}
	return ask.Form{ID: "act-action", Title: fmt.Sprintf("PID %d", a.PID), SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang dilakukan pada koneksi " + desc + "?",
		Options: []ask.Option{
			{Value: "cancel", Label: "Batalkan query-nya", Description: "Query dihentikan, koneksi tetap terbuka. Aplikasi menerima error dan biasanya mencoba lagi.", Disabled: notRunning, Recommended: a.State == "active"},
			{Value: "terminate", Label: "Putuskan koneksinya", Description: "Transaksi yang belum di-commit dibatalkan. Dipakai untuk koneksi \"idle in transaction\" yang menahan lock.", Risk: risk.Dangerous},
		},
	}}}
}
