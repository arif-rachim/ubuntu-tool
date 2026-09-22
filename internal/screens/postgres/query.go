package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// maxSuggestionRows adalah banyak saran yang ditampilkan sekaligus.
const maxSuggestionRows = 7

// queryModel adalah editor SQL dengan saran otomatis: nama tabel, nama kolom beserta tipenya,
// kata kunci, dan — yang paling menolong pemula — bentuk nilai untuk kolom waktu.
type queryModel struct {
	env    shared.Env
	client syspg.Client
	db     string

	area    textarea.Model
	schema  syspg.Schema
	loading bool
	err     string

	comp    syspg.Completion
	sel     int
	hidden  bool // popup ditutup user dengan esc sampai ia mengetik lagi
	message string
	width   int
}

type schemaMsg struct {
	owner  *queryModel
	schema syspg.Schema
	err    error
}

// NewQuery membuka editor query untuk satu database.
func NewQuery(env shared.Env, c syspg.Client, db string) *queryModel {
	ta := textarea.New()
	ta.Placeholder = "SELECT * FROM … — tekan ctrl+n untuk melihat saran"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.SetHeight(6)
	ta.Focus()
	return &queryModel{env: env, client: c, db: db, area: ta, loading: true, width: 80}
}

func (m *queryModel) Title() string { return "Query " + m.db }

// Typing: seluruh ketikan diteruskan ke layar ini, termasuk huruf q dan r.
func (m *queryModel) Typing() bool { return true }

// HandlesBack: esc menutup daftar saran dulu, baru keluar layar.
func (m *queryModel) HandlesBack() bool { return !m.hidden && len(m.comp.Suggestions) > 0 }

func (m *queryModel) Init() tea.Cmd {
	c, r, db := m.client, m.env.Runner, m.db
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		s, err := c.ReadSchema(ctx, r, db)
		return schemaMsg{owner: m, schema: s, err: err}
	}
}

func (m *queryModel) Keys() []key.Binding {
	return []key.Binding{
		b("tab", "pakai saran"), b("ctrl+n/p", "pilih saran"), b("ctrl+r", "jalankan"),
		b("ctrl+l", "kosongkan"), b("esc", "tutup saran"),
	}
}

func (m *queryModel) HelpText() string {
	return "Editor ini tahu isi database " + m.db + ": nama tabel, nama kolom, dan tipe tiap kolom dibaca sekali saat layar dibuka. " +
		"Saran menyesuaikan posisi kursor — setelah FROM muncul nama tabel, setelah WHERE muncul kolom, " +
		"dan setelah kolom waktu muncul operator (>=, BETWEEN) beserta bentuk nilainya (now() - interval '7 days', date_trunc('month', now()), DATE '2026-01-31'). " +
		"Query yang diawali SELECT dijalankan dalam transaksi READ ONLY, jadi tidak mungkin mengubah data tanpa sengaja; " +
		"query yang mengubah data tetap bisa dijalankan tetapi ditandai berisiko dan butuh konfirmasi. " +
		"Command setara: sudo -u postgres psql -d " + m.db
}

func (m *queryModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.SizeMsg:
		m.width = msg.Width
		m.area.SetWidth(max(msg.Width-4, 20))
		m.area.SetHeight(max(min(msg.Height/4, 8), 3))
	case schemaMsg:
		if msg.owner == m {
			m.loading, m.schema = false, msg.schema
			if msg.err != nil {
				m.err = msg.err.Error()
			}
			m.refresh()
		}
	case nav.RefreshMsg:
		m.loading = true
		return m, m.Init()
	case nav.ResumedMsg:
		if o, ok := msg.Result.(run.Outcome); ok && o.Approved {
			if o.OK() {
				m.message = "✓ Query selesai. Hasilnya ada di layar sebelumnya (esc untuk kembali ke sini kapan saja)."
			} else {
				m.message = "✗ Query gagal — pesan error PostgreSQL biasanya menyebut baris & kolom penyebabnya."
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	case tea.PasteMsg:
		var cmd tea.Cmd
		m.area, cmd = m.area.Update(msg)
		m.refresh()
		return m, cmd
	}
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	return m, cmd
}

func (m *queryModel) key(msg tea.KeyPressMsg) (nav.Screen, tea.Cmd) {
	switch msg.String() {
	case "tab":
		if m.visible() {
			m.accept()
		}
		return m, nil
	case "ctrl+n", "ctrl+@", "ctrl+ ":
		if !m.visible() {
			m.hidden = false
			m.refresh()
			return m, nil
		}
		m.sel = (m.sel + 1) % len(m.comp.Suggestions)
		return m, nil
	case "ctrl+p":
		if m.visible() {
			m.sel = (m.sel - 1 + len(m.comp.Suggestions)) % len(m.comp.Suggestions)
		}
		return m, nil
	case "esc":
		if m.visible() {
			m.hidden = true
			return m, nil
		}
		return m, nav.Pop(nil)
	case "ctrl+r":
		return m.run()
	case "ctrl+l":
		m.area.SetValue("")
		m.message = ""
		m.refresh()
		return m, nil
	}
	m.message = ""
	m.hidden = false
	var cmd tea.Cmd
	m.area, cmd = m.area.Update(msg)
	m.refresh()
	return m, cmd
}

// visible melaporkan apakah daftar saran sedang ditampilkan.
func (m *queryModel) visible() bool { return !m.hidden && len(m.comp.Suggestions) > 0 }

// cursorOffset mengubah posisi kursor textarea (baris + kolom) menjadi indeks rune pada teks utuh.
func (m *queryModel) cursorOffset() int {
	lines := strings.Split(m.area.Value(), "\n")
	row := m.area.Line()
	if row > len(lines)-1 {
		row = len(lines) - 1
	}
	info := m.area.LineInfo()
	off := 0
	for i := 0; i < row; i++ {
		off += len([]rune(lines[i])) + 1
	}
	col := info.StartColumn + info.ColumnOffset
	if row >= 0 && row < len(lines) && col > len([]rune(lines[row])) {
		col = len([]rune(lines[row]))
	}
	return off + col
}

// refresh menghitung ulang saran untuk posisi kursor sekarang.
func (m *queryModel) refresh() {
	m.comp = syspg.Complete(m.schema, m.area.Value(), m.cursorOffset())
	m.sel = 0
}

// accept menyisipkan saran terpilih menggantikan kata yang sedang diketik.
func (m *queryModel) accept() {
	s := m.comp.Suggestions[m.sel]
	for range []rune(m.comp.Prefix) {
		m.area, _ = m.area.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m.area.InsertString(s.Text)
	for i := len([]rune(s.Text)) - s.CursorAfterInsert(); i > 0; i-- {
		m.area, _ = m.area.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	}
	m.refresh()
}

// run menyerahkan query ke alur konfirmasi biasa: command persis ditampilkan lebih dulu.
func (m *queryModel) run() (nav.Screen, tea.Cmd) {
	sql := strings.TrimSpace(m.area.Value())
	if sql == "" {
		m.message = "Tulis query-nya dulu."
		return m, nil
	}
	m.hidden = true
	return m, nav.Push(runflow.Confirm(m.client.QueryPlan(m.db, sql), m.env.Deps))
}

func (m *queryModel) View(width, height int) string {
	t := ui.Current
	var lines []string
	add := func(s string) { lines = append(lines, ui.Wrap(s, width, " ")) }

	head := " " + t.Title.Render("Query di "+m.db)
	switch {
	case m.loading:
		head += "  " + t.Subtle.Render("membaca daftar tabel…")
	case m.err != "":
		head += "  " + t.Danger.Render("skema tidak terbaca: "+m.err)
	case m.schema.Empty():
		head += "  " + t.Warning.Render("database ini belum punya tabel")
	default:
		head += "  " + t.Subtle.Render(fmt.Sprintf("%d tabel · %d kolom dikenali", len(m.schema.Relations), m.schema.ColumnCount()))
	}
	lines = append(lines, "", head, "")
	lines = append(lines, m.area.View())

	kind, sql := syspg.ClassifyQuery(m.area.Value())
	if strings.TrimSpace(m.area.Value()) != "" {
		label := t.Success.Render("hanya membaca — dijalankan dalam transaksi READ ONLY")
		if kind != syspg.QueryRead {
			label = t.Warning.Render("query ini mengubah data — akan diminta konfirmasi")
		}
		hint := " " + label
		if sql != "" {
			hint += t.Danger.Render("  ⚠ " + sql)
		}
		add(hint)
	}

	if m.message != "" {
		add(" " + t.Accent.Render(m.message))
	}

	if !m.visible() {
		add("")
		add(" " + t.Subtle.Render("ctrl+n untuk saran · ctrl+r menjalankan · esc kembali"))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}

	lines = append(lines, "", " "+t.Subtle.Render("Saran ("+m.comp.Context+"):"))
	// Di terminal pendek, daftar saran mengalah supaya editor & penjelasan tetap terlihat.
	rows := max(min(height-len(lines)-4, maxSuggestionRows), 2)
	start := 0
	if m.sel >= rows {
		start = m.sel - rows + 1
	}
	end := min(start+rows, len(m.comp.Suggestions))
	for i := start; i < end; i++ {
		s := m.comp.Suggestions[i]
		text := s.Text
		if len(text) > 46 {
			text = text[:45] + "…"
		}
		row := fmt.Sprintf(" %-46s %s", text, t.Muted.Render(s.Kind+" · "+s.Detail))
		if i == m.sel {
			lines = append(lines, t.Selected.Render(" ❯"+row))
			continue
		}
		lines = append(lines, "  "+row)
	}
	if n := len(m.comp.Suggestions) - end; n > 0 {
		lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf("… %d saran lain (ctrl+n)", n)))
	}
	if doc := m.comp.Suggestions[m.sel].Doc; doc != "" {
		lines = append(lines, "", ui.Wrap(t.Subtle.Render(doc), width, "   "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}
