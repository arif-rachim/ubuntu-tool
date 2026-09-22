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
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// tablesModel menampilkan tabel terbesar di satu database, dan index yang jarang dipakai.
type tablesModel struct {
	env     shared.Env
	client  syspg.Client
	db      string
	tables  []syspg.Table
	indexes []syspg.Index
	showIdx bool
	table   ui.Table
	loaded  bool
	err     string
}

type tablesMsg struct {
	owner   *tablesModel
	tables  []syspg.Table
	indexes []syspg.Index
	err     error
}

// NewTables membuka layar tabel & index satu database.
func NewTables(env shared.Env, c syspg.Client, db string) *tablesModel {
	return &tablesModel{env: env, client: c, db: db}
}

func (m *tablesModel) Title() string { return "Tabel " + m.db }

func (m *tablesModel) Init() tea.Cmd {
	c, r, db := m.client, m.env.Runner, m.db
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ts, err := c.ReadTables(ctx, r, db)
		idx, _ := c.ReadUnusedIndexes(ctx, r, db)
		return tablesMsg{owner: m, tables: ts, indexes: idx, err: err}
	}
}

func (m *tablesModel) Keys() []key.Binding {
	if m.showIdx {
		return []key.Binding{b("tab", "lihat tabel"), b("r", "muat ulang")}
	}
	return []key.Binding{b("tab", "lihat index jarang dipakai"), b("r", "muat ulang")}
}

func (m *tablesModel) HelpText() string {
	return "\"Ukuran\" adalah total tabel + seluruh index-nya. \"Sampah\" adalah baris mati: sisa UPDATE/DELETE yang masih memakan tempat " +
		"sampai dibersihkan autovacuum. Sampah di atas 20% pada tabel besar biasanya berarti autovacuum tidak sempat mengejar — " +
		"sering karena ada transaksi lama yang menggantung (lihat menu monitor). " +
		"Index yang hampir tidak pernah dipakai tetap memperlambat setiap INSERT/UPDATE dan memakan disk; menghapusnya perlu dipastikan dulu, " +
		"karena statistik ini terkumpul sejak terakhir di-reset dan bisa melewatkan laporan bulanan. " +
		"Command setara: sudo -u postgres psql -d " + m.db + " -c '\\dt+'"
}

func (m *tablesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tablesMsg:
		if msg.owner == m {
			m.loaded, m.tables, m.indexes = true, msg.tables, msg.indexes
			m.err = ""
			if msg.err != nil {
				m.err = msg.err.Error()
			}
			m.fill()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "tab" {
			m.showIdx = !m.showIdx
			m.table.Cursor = 0
			m.fill()
		}
	}
	return m, nil
}

func (m *tablesModel) fill() {
	if m.showIdx {
		m.table.Columns = []ui.Column{
			{Title: "Index", Width: 30, Flex: 3},
			{Title: "Tabel", Width: 18, Flex: 2},
			{Title: "Dipakai", Width: 9, Right: true},
			{Title: "Ukuran", Width: 10, Right: true},
			{Title: "Catatan", Width: 14, Flex: 1},
		}
		rows := make([][]string, len(m.indexes))
		for i, x := range m.indexes {
			note := ""
			if x.Unique {
				note = "unique — menjaga keunikan, jangan dihapus"
			}
			rows[i] = []string{x.Name, x.Table, fmt.Sprint(x.Scans), shared.Bytes(x.Bytes), note}
		}
		m.table.SetRows(rows)
		m.table.CellStyle = nil
		return
	}
	m.table.Columns = []ui.Column{
		{Title: "Tabel", Width: 26, Flex: 3},
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "Index", Width: 10, Right: true},
		{Title: "Baris", Width: 12, Right: true},
		{Title: "Sampah", Width: 8, Right: true},
		{Title: "Vacuum terakhir", Width: 16, Flex: 1},
	}
	rows := make([][]string, len(m.tables))
	for i, t := range m.tables {
		bloat := "—"
		if t.Bloat() >= 1 {
			bloat = fmt.Sprintf("%.0f%%", t.Bloat())
		}
		rows[i] = []string{t.Name, shared.Bytes(t.TotalBytes), shared.Bytes(t.IndexBytes), fmt.Sprint(t.LiveRows), bloat, t.LastVacuum}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(row, col int) lipgloss.Style {
		if row >= len(m.tables) {
			return lipgloss.NewStyle()
		}
		if col == 4 && m.tables[row].Bloat() > 20 {
			return ui.Current.Warning
		}
		return lipgloss.NewStyle()
	}
}

func (m *tablesModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca ukuran tabel…")
	}
	title, count := "Tabel di "+m.db, len(m.tables)
	if m.showIdx {
		title, count = "Index jarang dipakai di "+m.db, len(m.indexes)
	}
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("%s (%d)", title, count))}
	if m.err != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ "+m.err), width, " "))
	}
	top := strings.Join(lines, "\n")
	if count == 0 {
		body := "Database ini belum punya tabel, atau tabelnya berada di skema selain yang dipantau statistik pengguna."
		if m.showIdx {
			body = "Semua index di database ini terpakai — tidak ada yang perlu ditinjau."
		}
		return top + "\n" + ui.EmptyState("Tidak ada data", body, "tekan tab untuk berpindah daftar", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}
