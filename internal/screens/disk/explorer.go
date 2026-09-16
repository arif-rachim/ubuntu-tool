package disk

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdisk "github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	sysres "github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// ExplorerModel menampilkan folder yang paling makan tempat dan bisa ditelusuri.
type ExplorerModel struct {
	env      shared.Env
	root     string
	prog     *sysdisk.Progress
	cancel   context.CancelFunc
	started  time.Time
	result   *sysdisk.Result
	err      error
	cur      *sysdisk.Node
	table    ui.Table
	bigFiles bool
}

type (
	scanDoneMsg struct {
		owner *ExplorerModel
		res   sysdisk.Result
		err   error
	}
	scanTickMsg struct{ owner *ExplorerModel }
)

func newExplorer(env shared.Env, root string) *ExplorerModel {
	return &ExplorerModel{
		env:  env,
		root: root,
		prog: &sysdisk.Progress{},
		table: ui.Table{Columns: []ui.Column{
			{Title: "Nama", Width: 16, Flex: 3},
			{Title: "Ukuran", Width: 10, Right: true},
			{Title: "Porsi", Width: 17},
			{Title: "File", Width: 8, Right: true},
		}},
	}
}

var _ nav.Screen = (*ExplorerModel)(nil)

func (m *ExplorerModel) Title() string { return "Apa yang makan tempat: " + m.root }

func (m *ExplorerModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	if m.result == nil {
		return []key.Binding{b("esc", "batalkan scan")}
	}
	return []key.Binding{b("enter", "masuk folder"), b("backspace", "folder induk"), b("f", "file terbesar")}
}

// HandlesBack: esc naik ke folder induk dulu, baru keluar di folder awal.
func (m *ExplorerModel) HandlesBack() bool { return true }

func (m *ExplorerModel) Init() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.started = cancel, m.env.Now()
	root, prog := m.root, m.prog
	return tea.Batch(
		func() tea.Msg {
			res, err := sysdisk.Scan(ctx, root, sysdisk.ScanOptions{MaxBigFiles: 30, MinBigFile: 50 << 20}, prog)
			return scanDoneMsg{owner: m, res: res, err: err}
		},
		m.tick(),
	)
}

func (m *ExplorerModel) tick() tea.Cmd {
	return tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return scanTickMsg{owner: m} })
}

func (m *ExplorerModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case scanTickMsg:
		if msg.owner == m && m.result == nil && m.err == nil {
			return m, m.tick()
		}
	case scanDoneMsg:
		if msg.owner != m {
			return m, nil
		}
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.result = &msg.res
		m.setNode(msg.res.Root)
	case tea.KeyPressMsg:
		k := msg.String()
		if m.result == nil {
			if k == "esc" {
				m.cancel()
				return m, nav.Pop(nil)
			}
			return m, nil
		}
		if !m.bigFiles && m.table.HandleKey(k) {
			return m, nil
		}
		switch k {
		case "enter", "right", "l":
			if !m.bigFiles && m.table.Cursor < len(m.cur.Children) {
				if child := m.cur.Children[m.table.Cursor]; len(child.Children) > 0 {
					m.setNode(child)
				}
			}
		case "backspace", "left", "h", "esc":
			switch {
			case m.bigFiles:
				m.bigFiles = false
			case m.cur.Parent != nil:
				prev := m.cur
				m.setNode(m.cur.Parent)
				for i, c := range m.cur.Children {
					if c == prev {
						m.table.Cursor = i
					}
				}
			case k == "esc":
				return m, nav.Pop(nil)
			}
		case "f":
			m.bigFiles = !m.bigFiles
		}
	}
	return m, nil
}

func (m *ExplorerModel) setNode(n *sysdisk.Node) {
	m.cur = n
	rows := make([][]string, len(n.Children))
	for i, c := range n.Children {
		pct := 0.0
		if n.Size > 0 {
			pct = 100 * float64(c.Size) / float64(n.Size)
		}
		name := c.Name + "/"
		if len(c.Children) == 0 {
			name = c.Name + "/ ·"
		}
		rows[i] = []string{name, shared.Bytes(c.Size), sysres.Bar(pct, 10) + fmt.Sprintf(" %3.0f%%", pct), strconv.FormatInt(c.Files, 10)}
	}
	m.table.Cursor = 0
	m.table.SetRows(rows)
}

func (m *ExplorerModel) View(width, height int) string {
	t := ui.Current
	if m.err != nil {
		return ui.ErrorState(m.err, "", width)
	}
	if m.result == nil {
		elapsed := m.env.Now().Sub(m.started).Round(time.Second)
		return fmt.Sprintf("\n %s\n\n   %s file · %s · %s folder · %s\n\n %s",
			t.Title.Render("Menghitung isi "+m.root+"…"),
			strconv.FormatInt(m.prog.Files.Load(), 10), shared.Bytes(m.prog.Bytes.Load()), strconv.FormatInt(m.prog.Dirs.Load(), 10), elapsed,
			t.Muted.Render("Folder besar bisa butuh beberapa menit. Tekan esc untuk membatalkan."))
	}

	var top []string
	top = append(top, "")
	if m.bigFiles {
		top = append(top, " "+t.Title.Render(fmt.Sprintf("File terbesar di %s (≥ 50 MiB)", m.root))+"   "+
			t.Muted.Render(fmt.Sprintf("command setara: sudo find %s -xdev -type f -size +50M", run.QuoteShell(m.root))))
		top = append(top, "")
		var lines []string
		if len(m.result.BigFiles) == 0 {
			lines = append(lines, "   "+t.Subtle.Render("Tidak ada file ≥ 50 MiB."))
		}
		for _, f := range m.result.BigFiles {
			lines = append(lines, fmt.Sprintf("   %10s  %s", shared.Bytes(f.Size), f.Path))
		}
		lines = append(lines, "", ui.Wrap(t.Muted.Render("ubt tidak menghapus file bebas. Periksa isinya dulu; bila yakin, hapus sendiri dengan rm dan pastikan tidak sedang dibuka proses."), width, " "))
		return ui.FitHeight(strings.Join(append(top, lines...), "\n"), width, height)
	}

	top = append(top, " "+t.Title.Render(m.cur.Path)+"  "+t.Subtle.Render(fmt.Sprintf("%s · %d file", shared.Bytes(m.cur.Size), m.cur.Files))+
		"   "+t.Muted.Render("command setara: sudo du -xh -d1 "+run.QuoteShell(m.cur.Path)+" | sort -h"))
	if m.cur.Denied > 0 {
		hint := " Jalankan sudo ubt untuk menghitung semuanya."
		if m.env.IsRoot {
			hint = ""
		}
		top = append(top, ui.Wrap(t.Warning.Render(fmt.Sprintf("⚠ %d folder tidak bisa dibaca, jadi ukuran sebenarnya lebih besar.%s", m.cur.Denied, hint)), width, " "))
	}
	direct := m.cur.Size
	for _, c := range m.cur.Children {
		direct -= c.Size
	}
	top = append(top, " "+t.Muted.Render(fmt.Sprintf("File langsung di folder ini: %s · folder bertanda · tidak punya subfolder · mount lain tidak dihitung", shared.Bytes(max(direct, 0)))), "")
	topStr := strings.Join(top, "\n")
	if len(m.cur.Children) == 0 {
		return ui.FitHeight(topStr+"\n   "+t.Subtle.Render("Tidak ada subfolder."), width, height)
	}
	return ui.FitHeight(topStr+"\n"+m.table.View(width, height-lipgloss.Height(topStr)), width, height)
}

// DeletedModel menampilkan file terhapus yang masih dibuka dan menawarkan restart pemiliknya.
type DeletedModel struct {
	env    shared.Env
	del    sysdisk.DeletedOpen
	table  ui.Table
	target *sysdisk.DeletedFile
	proc   *procs.Process
}

func newDeleted(env shared.Env, del sysdisk.DeletedOpen) *DeletedModel {
	m := &DeletedModel{env: env, del: del, table: ui.Table{Columns: []ui.Column{
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "PID", Width: 7, Right: true},
		{Title: "Proses", Width: 12, Flex: 1},
		{Title: "File (sudah dihapus)", Width: 20, Flex: 4},
	}}}
	rows := make([][]string, len(del.Files))
	for i, f := range del.Files {
		name := "?"
		if p, err := procs.Read(env.ProcRoot, f.PID); err == nil {
			name = p.Name
		}
		rows[i] = []string{shared.Bytes(f.Size), strconv.Itoa(f.PID), name, f.Path}
	}
	m.table.SetRows(rows)
	return m
}

var _ nav.Screen = (*DeletedModel)(nil)

func (m *DeletedModel) Title() string { return "File terhapus tapi masih dibuka" }
func (m *DeletedModel) Init() tea.Cmd { return nil }
func (m *DeletedModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "bebaskan ruangnya"))}
}

func (m *DeletedModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "enter" && m.table.Cursor < len(m.del.Files) {
			f := m.del.Files[m.table.Cursor]
			p, err := procs.Read(m.env.ProcRoot, f.PID)
			if err != nil {
				return m, nil
			}
			m.target, m.proc = &f, &p
			ctx := procact.CurrentContext(m.env.UID, m.env.IsRoot)
			var opts []ask.Option
			if p.Cgroup.Service() {
				opts = append(opts, ask.Option{
					Value: "restart", Label: "Restart unit " + p.Cgroup.Unit, Recommended: true,
					Description: "Proses baru tidak memegang file lama, sehingga ruang disk langsung kembali. Layanan terputus sebentar.",
				})
			}
			opts = append(opts, procact.Options(p, procact.Subject{}, ctx)...)
			return m, nav.Push(ask.New(ask.Form{
				ID:    "deleted",
				Title: "Bebaskan " + shared.Bytes(f.Size),
				Questions: []ask.Question{{
					ID:      "aksi",
					Prompt:  fmt.Sprintf("%s (PID %d) masih membuka %s. Apa yang ingin dilakukan?", p.Name, p.PID, f.Path),
					Kind:    ask.Single,
					Options: opts,
					Help:    "Di Linux, file yang dihapus baru benar-benar hilang setelah tidak ada proses yang membukanya. Ini sering terjadi pada log yang dihapus manual saat aplikasinya masih menulis. Lain kali, kosongkan isi log dengan: truncate -s 0 FILE",
				}},
			}))
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled || m.proc == nil {
				return m, nil
			}
			action := r.Answers["aksi"].Value()
			plan := procact.Plan(action, *m.proc, procact.Subject{}, procact.CurrentContext(m.env.UID, m.env.IsRoot))
			if action == "restart" {
				plan = restartPlan(m.proc.Cgroup.Unit, m.proc.Cgroup.UserUnit, *m.target)
			}
			if len(plan.Steps) > 0 {
				return m, nav.Push(runflow.Confirm(plan, m.env.Deps))
			}
		case run.Outcome:
			if r.Approved {
				return m, nav.Pop(r)
			}
		}
	}
	return m, nil
}

func (m *DeletedModel) View(width, height int) string {
	t := ui.Current
	if len(m.del.Files) == 0 {
		return ui.EmptyState("Tidak ada file terhapus yang masih dibuka", "Semua ruang dari file yang dihapus sudah kembali.", "", width)
	}
	top := strings.Join([]string{
		"",
		ui.Wrap(t.Subtle.Render(fmt.Sprintf("%d file sudah dihapus, tetapi total %s ruang disk belum kembali karena proses di bawah masih membukanya. df menghitungnya terpakai, du tidak menemukannya.", len(m.del.Files), shared.Bytes(m.del.Total()))), width, " "),
		"",
	}, "\n")
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}
