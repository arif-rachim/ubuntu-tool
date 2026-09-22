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

// rolesModel adalah layar daftar role (user database) dan hak aksesnya.
type rolesModel struct {
	env      shared.Env
	client   syspg.Client
	dbs      []string
	roles    []syspg.Role
	table    ui.Table
	loaded   bool
	err      string
	message  string
	selected syspg.Role
}

type rolesMsg struct {
	owner *rolesModel
	roles []syspg.Role
	err   error
}

// NewRoles membuka layar role & hak akses.
func NewRoles(env shared.Env, c syspg.Client, dbs []string) *rolesModel {
	return &rolesModel{env: env, client: c, dbs: dbs, table: ui.Table{Columns: []ui.Column{
		{Title: "Role", Width: 18, Flex: 2},
		{Title: "Jenis", Width: 10},
		{Title: "Login", Width: 7},
		{Title: "Password", Width: 9},
		{Title: "Hak khusus", Width: 20, Flex: 2},
		{Title: "Anggota dari", Width: 16, Flex: 1},
	}}}
}

func (m *rolesModel) Title() string { return "Role & akses" }

func (m *rolesModel) Init() tea.Cmd {
	c, r := m.client, m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		roles, err := c.ReadRoles(ctx, r)
		return rolesMsg{owner: m, roles: roles, err: err}
	}
}

func (m *rolesModel) Keys() []key.Binding {
	return []key.Binding{b("enter", "aksi role"), b("a", "buat role")}
}

func (m *rolesModel) HelpText() string {
	return "Role adalah user database — berbeda dari user Linux. Role yang boleh login disebut user; yang tidak boleh login " +
		"biasanya dipakai sebagai grup penampung hak akses, lalu role lain dijadikan anggotanya. " +
		"Hak akses selalu berlapis: boleh menyambung ke database (CONNECT), boleh memakai skema (USAGE), baru boleh membaca/menulis tabel. " +
		"Wizard \"beri akses\" melakukan ketiganya sekaligus dan menampilkan tiap perintah SQL-nya. " +
		"Command setara: sudo -u postgres psql -c '\\du'"
}

func (m *rolesModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *rolesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case rolesMsg:
		if msg.owner == m {
			m.loaded, m.roles = true, msg.roles
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
		case "a":
			return m, nav.Push(ask.New(RoleForm()))
		case "enter":
			if m.table.Cursor < len(m.roles) {
				m.selected = m.roles[m.table.Cursor]
				return m, nav.Push(ask.New(RoleActionForm(m.selected, m.dbs)))
			}
		}
	}
	return m, nil
}

func (m *rolesModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		a := r.Answers
		switch r.ID {
		case "role-add":
			name := strings.TrimSpace(a["name"].Value())
			kind := a["kind"].Value()
			opts := syspg.RoleOptions{Login: kind != "group", CreateDB: a["createdb"].Yes(), Superuser: kind == "admin"}
			return m.confirm(m.client.CreateRolePlan(name, opts, kind != "group" && a["password"].Yes()))
		case "role-action":
			switch a["action"].Value() {
			case "password":
				return m.confirm(m.client.SetPasswordPlan(m.selected.Name))
			case "grant":
				return m, nav.Push(ask.New(DatabasePickForm("role-grant", "Beri akses "+m.selected.Name, "Akses ke database mana?", m.dbs, true)))
			case "revoke":
				return m, nav.Push(ask.New(DatabasePickForm("role-revoke", "Cabut akses "+m.selected.Name, "Cabut akses dari database mana?", m.dbs, false)))
			case "drop":
				return m.confirm(m.client.DropRolePlan(m.selected.Name))
			}
		case "role-grant":
			return m.confirm(m.client.GrantPlan(strings.TrimSpace(a["db"].Value()), m.selected.Name, a["level"].Value()))
		case "role-revoke":
			return m.confirm(m.client.RevokePlan(strings.TrimSpace(a["db"].Value()), m.selected.Name))
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

func (m *rolesModel) fill() {
	rows := make([][]string, len(m.roles))
	for i, r := range m.roles {
		var priv []string
		if r.Superuser {
			priv = append(priv, "superuser")
		}
		if r.CreateDB {
			priv = append(priv, "buat database")
		}
		if r.CreateRole {
			priv = append(priv, "buat role")
		}
		if r.Replication {
			priv = append(priv, "replikasi")
		}
		login, pw := "—", "—"
		if r.Login {
			login = "ya"
		}
		if r.HasPassword {
			pw = "ada"
		}
		rows[i] = []string{r.Name, r.Kind(), login, pw, strings.Join(priv, ", "), strings.Join(r.MemberOf, ", ")}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if row >= len(m.roles) {
			return lipgloss.NewStyle()
		}
		t := ui.Current
		switch {
		case m.roles[row].Superuser && col == 4:
			return t.Warning
		case m.roles[row].Login && !m.roles[row].HasPassword && col == 3:
			return t.Muted
		}
		return lipgloss.NewStyle()
	}
}

func (m *rolesModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca role…")
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Role (%d)", len(m.roles))) + "   " + t.Muted.Render("command setara: sudo -u postgres psql -c '\\du'")}
	if m.err != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.roles) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada role", "", "tekan a untuk membuat role pertama", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}
