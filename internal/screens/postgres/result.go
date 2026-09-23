package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
)

// maxColWidth membatasi lebar satu kolom supaya satu kolom panjang (mis. catatan) tidak
// mendorong kolom lain keluar layar. Isi penuh tetap bisa dilihat di detail baris.
const maxColWidth = 28

// resultModel menampilkan hasil query sebagai tabel yang bisa digulir, disaring, dan dibuka
// per baris.
type resultModel struct {
	env    shared.Env
	client syspg.Client
	db     string
	sql    string

	res     syspg.Result
	rows    [][]string // hasil filter, sejajar dengan table.Rows
	table   ui.Table
	loading bool
	err     string

	filter textinput.Model
	typing bool
}

type resultMsg struct {
	owner *resultModel
	res   syspg.Result
	err   error
}

// NewResult membuka layar hasil query.
func NewResult(env shared.Env, c syspg.Client, db, sql string) *resultModel {
	fi := textinput.New()
	fi.Prompt = "Saring: "
	fi.Placeholder = "ketik untuk menyaring baris…"
	return &resultModel{env: env, client: c, db: db, sql: sql, loading: true, filter: fi}
}

func (m *resultModel) Title() string { return "Hasil" }
func (m *resultModel) Typing() bool  { return m.typing }

// HandlesBack: esc menutup mode saring dulu, baru kembali ke editor.
func (m *resultModel) HandlesBack() bool { return m.typing || m.filter.Value() != "" }

func (m *resultModel) Init() tea.Cmd {
	c, r, db, sql := m.client, m.env.Runner, m.db, m.sql
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := c.RunQuery(ctx, r, db, sql)
		return resultMsg{owner: m, res: res, err: err}
	}
}

func (m *resultModel) Keys() []key.Binding {
	return []key.Binding{b("↑↓", "gulir"), b("enter", "lihat baris"), b("/", "saring"), b("r", "jalankan ulang")}
}

func (m *resultModel) HelpText() string {
	return "Hasil diambil sebagai CSV lewat COPY … TO STDOUT, jadi nilai yang mengandung koma, tanda kutip, " +
		"atau baris baru tetap utuh di kolomnya. Sel bertanda NULL benar-benar kosong (bukan teks kosong). " +
		"Kolom yang isinya panjang dipotong di tabel — tekan enter untuk melihat satu baris apa adanya. " +
		"Query dijalankan dalam transaksi READ ONLY dengan batas waktu " + syspg.ResultTimeout + ". " +
		"Baris dibatasi " + fmt.Sprint(syspg.MaxResultRows) + "; bila terpotong, tambahkan LIMIT atau saring dengan WHERE. " +
		"Command setara: sudo -u postgres psql -d " + m.db + " -c \"COPY (…) TO STDOUT WITH CSV HEADER\""
}

func (m *resultModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case resultMsg:
		if msg.owner == m {
			m.loading = false
			m.err = ""
			if msg.err != nil {
				m.err = msg.err.Error()
			}
			m.res = msg.res
			m.build()
		}
	case nav.RefreshMsg:
		m.loading = true
		return m, m.Init()
	case tea.PasteMsg:
		if m.typing {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.apply()
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *resultModel) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	if m.typing {
		switch k {
		case "esc":
			m.typing = false
			m.filter.SetValue("")
			m.filter.Blur()
		case "enter":
			m.typing = false
			m.filter.Blur()
		case "up", "down":
			m.table.HandleKey(k)
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.apply()
			return cmd
		}
		m.apply()
		return nil
	}
	if m.table.HandleKey(k) {
		return nil
	}
	switch k {
	case "/":
		m.typing = true
		return m.filter.Focus()
	case "esc":
		m.filter.SetValue("")
		m.apply()
	case "r":
		m.loading = true
		return m.Init()
	case "enter", "right", "l":
		if m.table.Cursor < len(m.rows) {
			return nav.Push(newRowDetail(m.res.Columns, m.rows[m.table.Cursor], m.table.Cursor+1))
		}
	}
	return nil
}

// build menyiapkan kolom tabel dari hasil query: lebar mengikuti isi, kolom angka rata kanan.
func (m *resultModel) build() {
	cols := make([]ui.Column, len(m.res.Columns))
	for i, name := range m.res.Columns {
		w := m.res.Widest(i)
		flex := 0
		if w > maxColWidth {
			w, flex = maxColWidth, 2
		}
		cols[i] = ui.Column{Title: name, Width: max(w, 3), Flex: flex, Right: m.res.NumericColumn(i)}
	}
	m.table.Columns = cols
	m.apply()
}

// apply menyaring baris sesuai teks saringan dan mengisi tabel.
func (m *resultModel) apply() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.rows = m.rows[:0]
	var cells [][]string
	for _, row := range m.res.Rows {
		shown := make([]string, len(row))
		for i, c := range row {
			shown[i] = syspg.Flatten(syspg.Display(c))
		}
		if q != "" && !strings.Contains(strings.ToLower(strings.Join(shown, " ")), q) {
			continue
		}
		m.rows = append(m.rows, row)
		cells = append(cells, shown)
	}
	m.table.SetRows(cells)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		if r < len(m.rows) && c < len(m.rows[r]) && syspg.IsNull(m.rows[r][c]) {
			return ui.Current.Muted
		}
		return lipgloss.NewStyle()
	}
}

func (m *resultModel) View(width, height int) string {
	t := ui.Current
	if m.loading {
		return "\n " + t.Subtle.Render("Menjalankan query…")
	}
	var lines []string
	add := func(s string) { lines = append(lines, ui.Wrap(s, width, " ")) }
	lines = append(lines, "")

	if m.err != "" {
		add(t.Danger.Render("✗ Query gagal"))
		add("")
		add(t.Muted.Render(m.err))
		add("")
		add(t.Subtle.Render("Tekan esc untuk kembali ke editor dan memperbaiki query-nya, atau r untuk mencoba lagi."))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}

	head := fmt.Sprintf("%d baris", len(m.res.Rows))
	if n := len(m.rows); n != len(m.res.Rows) {
		head = fmt.Sprintf("%d dari %d baris", n, len(m.res.Rows))
	}
	head += fmt.Sprintf(" · %d kolom · %d ms", len(m.res.Columns), m.res.Elapsed.Milliseconds())
	lines = append(lines, " "+t.Title.Render("Hasil")+"  "+t.Subtle.Render(head))
	add(t.Muted.Render(ringkasSQL(m.sql)))
	if m.res.Truncated {
		add(t.Warning.Render(fmt.Sprintf("⚠ Ditampilkan %d baris pertama saja. Tambahkan LIMIT atau persempit WHERE untuk melihat sisanya.", syspg.MaxResultRows)))
	}
	if m.typing || m.filter.Value() != "" {
		lines = append(lines, " "+m.filter.View())
	}

	top := strings.Join(lines, "\n")
	if m.res.Empty() {
		return top + "\n" + ui.EmptyState("Query tidak mengembalikan baris",
			"Query-nya berhasil dijalankan, hanya saja tidak ada data yang cocok dengan syaratnya.",
			"tekan esc untuk kembali ke editor", width)
	}
	if len(m.rows) == 0 {
		return top + "\n" + ui.EmptyState("Tidak ada baris yang cocok dengan saringan", "", "tekan esc untuk menghapus saringan", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ringkasSQL memadatkan query jadi satu baris untuk ditampilkan sebagai judul kecil.
func ringkasSQL(sql string) string {
	s := strings.Join(strings.Fields(sql), " ")
	if len(s) > 110 {
		s = s[:109] + "…"
	}
	return s
}

// --- satu baris ---------------------------------------------------------------------------------

// rowDetail menampilkan satu baris secara vertikal, dengan isi sel apa adanya.
type rowDetail struct {
	columns []string
	row     []string
	number  int
	viewer  ui.Viewer
}

func newRowDetail(columns, row []string, number int) *rowDetail {
	return &rowDetail{columns: columns, row: row, number: number}
}

func (m *rowDetail) Title() string       { return fmt.Sprintf("Baris %d", m.number) }
func (m *rowDetail) Init() tea.Cmd       { return nil }
func (m *rowDetail) Keys() []key.Binding { return []key.Binding{b("↑↓", "gulir")} }

func (m *rowDetail) HelpText() string {
	return "Isi sel ditampilkan apa adanya, termasuk yang panjang atau berisi baris baru — di tabel isinya dipotong agar kolom lain tetap terlihat."
}

func (m *rowDetail) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		m.viewer.HandleKey(k.String())
	}
	return m, nil
}

func (m *rowDetail) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Baris %d", m.number)), ""}
	label := 0
	for _, c := range m.columns {
		if n := len([]rune(c)); n > label {
			label = n
		}
	}
	indent := " " + strings.Repeat(" ", label) + "  "
	for i, name := range m.columns {
		value := ""
		if i < len(m.row) {
			value = m.row[i]
		}
		style := lipgloss.NewStyle()
		if syspg.IsNull(value) {
			style = t.Muted
		}
		head := " " + t.Subtle.Render(fmt.Sprintf("%-*s", label, name)) + "  "
		// Nilai ditampilkan utuh: baris barunya dipertahankan, dan teks panjang dilipat — bukan
		// dipotong seperti di tabel.
		for _, part := range strings.Split(syspg.Display(value), "\n") {
			part = strings.ReplaceAll(part, "\t", "    ")
			for _, line := range strings.Split(ui.Wrap(style.Render(part), width, indent), "\n") {
				if head != "" {
					lines = append(lines, head+strings.TrimPrefix(line, indent))
					head = ""
					continue
				}
				lines = append(lines, line)
			}
		}
	}
	m.viewer.SetLines(lines)
	return m.viewer.View(width, height, false)
}
