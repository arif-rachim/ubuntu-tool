// Package postgres adalah layar modul PostgreSQL: kondisi cluster, database, role & hak akses,
// cadangan, akses dari jaringan, monitor, dan penyetelan.
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
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah dashboard PostgreSQL (daftar database).
type Model struct {
	env       shared.Env
	client    syspg.Client
	status    syspg.Status
	databases []syspg.Database
	table     ui.Table
	loaded    bool
	message   string
	selected  syspg.Database
}

type statusMsg struct {
	owner     *Model
	status    syspg.Status
	databases []syspg.Database
}

// New membuat dashboard PostgreSQL.
func New(env shared.Env) *Model { return newModel(env, syspg.NewClient()) }

func newModel(env shared.Env, c syspg.Client) *Model {
	return &Model{env: env, client: c, table: ui.Table{Columns: []ui.Column{
		{Title: "Database", Width: 20, Flex: 2},
		{Title: "Pemilik", Width: 14, Flex: 1},
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "Koneksi", Width: 8, Right: true},
		{Title: "Encoding", Width: 10},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "PostgreSQL" }

func b(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }

func (m *Model) Keys() []key.Binding {
	switch m.status.Avail {
	case syspg.NotInstalled:
		return []key.Binding{b("i", "install postgresql")}
	case syspg.NoCluster:
		return []key.Binding{b("i", "install ulang paket")}
	case syspg.Down:
		return []key.Binding{b("d", "jalankan cluster")}
	case syspg.NoPermission:
		return []key.Binding{b("s", "baca dengan sudo")}
	case syspg.Unknown:
		return nil
	}
	return []key.Binding{b("enter", "aksi database"), b("a", "buat database"), b("u", "role & akses"), b("m", "monitor"),
		b("b", "cadangan"), b("n", "akses jaringan"), b("t", "setelan")}
}

func (m *Model) HelpText() string {
	return "Satu server PostgreSQL disebut cluster: punya satu port, satu direktori data, dan berisi banyak database. " +
		"Role adalah user database (berbeda dari user Linux). ubt menjalankan semua perintah sebagai user sistem `postgres`, " +
		"pemilik cluster bawaan Ubuntu — itulah sebabnya beberapa perintah diawali sudo runuser -u postgres. " +
		"Konfigurasi ada di /etc/postgresql/VERSI/NAMA/, dan ubt tidak pernah mengubah postgresql.conf bawaan: " +
		"perubahan ditulis sebagai berkas terpisah di conf.d. " +
		"Command setara: pg_lsclusters, psql -l, psql -c '\\du'"
}

func (m *Model) Init() tea.Cmd {
	c, r, wanted := m.client, m.env.Runner, m.status.Selected().ID()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st := c.Read(ctx, r, wanted)
		msg := statusMsg{owner: m, status: st}
		if st.Avail == syspg.Ready {
			cc := c
			cc.Port = st.Selected().Port
			msg.databases, _ = cc.ReadDatabases(ctx, r)
		}
		return msg
	}
}

// clientFor mengembalikan client yang menunjuk ke cluster aktif.
func (m *Model) clientFor() syspg.Client {
	c := m.client
	c.Port = m.status.Selected().Port
	return c
}

func (m *Model) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		if msg.owner == m {
			m.loaded, m.status, m.databases = true, msg.status, msg.databases
			m.fill()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	case tea.KeyPressMsg:
		if m.status.Avail == syspg.Ready && m.table.HandleKey(msg.String()) {
			return m, nil
		}
		return m.key(msg.String())
	}
	return m, nil
}

func (m *Model) key(k string) (nav.Screen, tea.Cmd) {
	m.message = ""
	switch m.status.Avail {
	case syspg.NotInstalled, syspg.NoCluster:
		if k == "i" {
			return m.confirm(syspg.InstallPlan())
		}
		return m, nil
	case syspg.Down:
		if k == "d" {
			return m.confirm(syspg.StartClusterPlan(m.status.Selected()))
		}
		return m, nil
	case syspg.NoPermission:
		if k == "s" {
			probe := m.clientFor().QueryCommand("postgres", "SELECT 1 AS ok")
			probe.Title = "Baca PostgreSQL dengan hak root"
			probe.Effect = "Hanya membaca. Setelah sudo diizinkan, ubt bisa membaca kondisi database selama sesi ini."
			return m.confirm(run.Single(probe))
		}
		return m, nil
	case syspg.Unknown:
		return m, nil
	}

	c := m.clientFor()
	switch k {
	case "a":
		return m, nav.Push(ask.New(DatabaseForm(m.roleNames())))
	case "u":
		return m, nav.Push(NewRoles(m.env, c, m.dbNames()))
	case "m":
		return m, nav.Push(NewMonitor(m.env, c, m.status, m.dbNames()))
	case "b":
		return m, nav.Push(NewBackup(m.env, c, m.dbNames()))
	case "n":
		return m, nav.Push(ask.New(RemoteAccessForm(m.dbNames(), m.roleNames())))
	case "t":
		return m, nav.Push(NewTuning(m.env, c, m.status.Selected()))
	case "enter":
		if m.table.Cursor < len(m.databases) {
			m.selected = m.databases[m.table.Cursor]
			return m, nav.Push(ask.New(DatabaseActionForm(m.selected, m.roleNames())))
		}
	}
	return m, nil
}

func (m *Model) dbNames() []string {
	out := make([]string, 0, len(m.databases))
	for _, d := range m.databases {
		out = append(out, d.Name)
	}
	return out
}

// roleNames dibaca saat dibutuhkan wizard; daftar kosong bukan masalah (pertanyaan tetap bisa diisi manual).
func (m *Model) roleNames() []string {
	if m.env.Runner == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	roles, err := m.clientFor().ReadRoles(ctx, m.env.Runner)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range roles {
		if r.Login {
			out = append(out, r.Name)
		}
	}
	return out
}

func (m *Model) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		a := r.Answers
		c := m.clientFor()
		switch r.ID {
		case "db-add":
			return m.confirm(c.CreateDatabasePlan(strings.TrimSpace(a["name"].Value()), strings.TrimSpace(a["owner"].Value())))
		case "db-action":
			return m.databaseAction(a)
		case "db-grant":
			return m.confirm(c.GrantPlan(m.selected.Name, strings.TrimSpace(a["role"].Value()), a["level"].Value()))
		case "db-query":
			return m.confirm(c.QueryPlan(m.selected.Name, strings.TrimSpace(a["sql"].Value())))
		case "db-ext":
			return m.confirm(c.CreateExtensionPlan(m.selected.Name, strings.TrimSpace(a["ext"].Value())))
		case "pg-remote":
			return m.confirm(syspg.RemoteAccessPlan(m.status.Selected(), a["mode"].Value(),
				strings.TrimSpace(a["db"].Value()), strings.TrimSpace(a["role"].Value()), strings.TrimSpace(a["cidr"].Value())))
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

func (m *Model) databaseAction(a ask.Answers) (nav.Screen, tea.Cmd) {
	c, db := m.clientFor(), m.selected
	switch a["action"].Value() {
	case "psql":
		return m.confirm(c.PsqlPlan(db.Name))
	case "query":
		return m, nav.Push(ask.New(QueryForm(db.Name)))
	case "grant":
		return m, nav.Push(ask.New(GrantForm(db.Name, m.roleNames())))
	case "tables":
		return m, nav.Push(NewTables(m.env, c, db.Name))
	case "backup":
		return m, nav.Push(NewBackup(m.env, c, m.dbNames()))
	case "ext":
		return m, nav.Push(ask.New(ExtensionForm(db.Name)))
	case "drop":
		return m.confirm(c.DropDatabasePlan(db.Name))
	}
	return m, nil
}

func (m *Model) fill() {
	rows := make([][]string, len(m.databases))
	for i, d := range m.databases {
		conn := "—"
		if d.Connections > 0 {
			conn = fmt.Sprint(d.Connections)
		}
		rows[i] = []string{d.Name, d.Owner, shared.Bytes(d.Size), conn, d.Encoding}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if row < len(m.databases) && m.databases[row].Name == "postgres" {
			return ui.Current.Muted
		}
		return lipgloss.NewStyle()
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Memeriksa PostgreSQL…")
	}
	var lines []string
	add := func(s string) { lines = append(lines, ui.Wrap(s, width, " ")) }
	lines = append(lines, "")
	st := m.status

	switch st.Avail {
	case syspg.NotInstalled:
		add(t.Warning.Render("PostgreSQL belum terpasang di server ini."))
		add(t.Subtle.Render("Tekan i untuk memasang paket postgresql + postgresql-contrib dari repository Ubuntu. " +
			"Satu cluster bernama \"main\" langsung dibuat di port 5432 dan hanya menerima koneksi dari server ini."))
	case syspg.NoCluster:
		add(t.Warning.Render("Paket PostgreSQL ada, tetapi belum ada cluster."))
		add(t.Subtle.Render("Buat cluster dengan: sudo pg_createcluster 17 main --start — atau tekan i untuk memasang ulang paketnya."))
	case syspg.Down:
		cl := st.Selected()
		add(t.Warning.Render("Cluster " + cl.ID() + " (port " + fmt.Sprint(cl.Port) + ") tidak berjalan."))
		add(t.Subtle.Render("Tekan d untuk menjalankannya. Bila gagal, alasannya ada di " + cl.LogFile + " — buka lewat modul Log."))
	case syspg.NoPermission:
		add(t.Warning.Render("ubt perlu izin root untuk membaca PostgreSQL."))
		add(t.Subtle.Render("Pembacaan dilakukan sebagai user sistem postgres (runuser -u postgres -- psql), dan itu butuh hak root. " +
			"Tekan s untuk menyetujui satu perintah baca; setelah sudo diizinkan, layar ini terisi."))
	case syspg.Unknown:
		add(t.Danger.Render("✗ PostgreSQL tidak bisa dibaca: " + st.Error))
	}
	if st.Avail != syspg.Ready {
		if m.message != "" {
			lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
		}
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}

	cl, sum := st.Selected(), st.Summary
	head := " " + t.Title.Render("PostgreSQL "+sum.Version) + "  " + t.Subtle.Render("cluster "+cl.ID()+" · port "+fmt.Sprint(cl.Port))
	if len(st.Clusters) > 1 {
		head += t.Muted.Render(fmt.Sprintf(" · %d cluster di server ini", len(st.Clusters)))
	}
	lines = append(lines, head)

	connStyle := t.Subtle
	if sum.MaxConn > 0 && sum.Connections*100/max(sum.MaxConn, 1) > 80 {
		connStyle = t.Warning
	}
	info := fmt.Sprintf("Koneksi %d/%d (%d aktif)", sum.Connections, sum.MaxConn, sum.Active)
	if sum.Idle > 0 {
		info += fmt.Sprintf(" · %d idle in transaction", sum.Idle)
	}
	info += " · data " + shared.Bytes(sum.DataSize) + " · hidup " + shared.Duration(time.Duration(sum.Uptime)*time.Second)
	lines = append(lines, " "+connStyle.Render(info))
	if !st.HasStatements {
		lines = append(lines, " "+t.Muted.Render("pg_stat_statements belum aktif — aktifkan di menu monitor untuk melihat query paling berat"))
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", " "+t.Title.Render(fmt.Sprintf("Database (%d)", len(m.databases)))+"   "+t.Muted.Render("command setara: sudo -u postgres psql -l"))
	top := strings.Join(lines, "\n")
	if len(m.databases) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada database", "", "tekan a untuk membuat database pertama", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}
