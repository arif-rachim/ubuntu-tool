package ports

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Berapa lama menunggu proses berhenti setelah SIGTERM sebelum menawarkan SIGKILL.
const (
	termPollInterval = 300 * time.Millisecond
	termPollCount    = 10
)

// DetailModel menampilkan satu port beserta prosesnya dan menawarkan aksi.
type DetailModel struct {
	env    shared.Env
	data   Data
	target Target
	parent *procs.Process
	offset int

	lastAction string
	polls      int
	status     string
	statusOK   bool
	gone       bool // proses sudah tidak ada
}

type pollMsg struct{ owner *DetailModel }

// maxRemoteRows membatasi daftar alamat asal supaya layar tetap terbaca.
const maxRemoteRows = 8

func newDetail(env shared.Env, d Data, l sysports.Listener) *DetailModel {
	m := &DetailModel{env: env, data: d, target: d.Target(l)}
	if p := m.target.Proc; p != nil && p.PPID > 1 {
		if pp, err := procs.Read(env.ProcRoot, p.PPID); err == nil {
			m.parent = &pp
		}
	}
	return m
}

var _ nav.Screen = (*DetailModel)(nil)

func (m *DetailModel) Title() string {
	return fmt.Sprintf("Port %d/%s", m.target.Listener.Local.Port(), m.target.Listener.Proto)
}

func (m *DetailModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("a", "aksi"), b("↑↓", "gulir")}
}

func (m *DetailModel) Init() tea.Cmd { return nil }

func (m *DetailModel) ctx() Context {
	return procact.CurrentContext(m.env.UID, m.env.IsRoot)
}

func (m *DetailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "a", "enter", "x":
			if m.gone {
				return m, nil
			}
			return m, nav.Push(ask.New(m.actionForm()))
		case "up", "k":
			m.offset = max(m.offset-1, 0)
		case "down", "j":
			m.offset++
		}
	case nav.ResumedMsg:
		return m, m.onResumed(msg.Result)
	case pollMsg:
		if msg.owner == m {
			return m, m.poll()
		}
	}
	return m, nil
}

// ownerUnknown menjelaskan kenapa pemilik port tidak terlihat. Alasannya berbeda saat ubt
// dijalankan sebagai root: bukan soal izin, melainkan prosesnya memang tidak ada di /proc ini.
func (m *DetailModel) ownerUnknown(l sysports.Listener) (reason, hint string) {
	if l.Process != "" {
		return "Nama proses menurut ss: " + l.Process + ".", ""
	}
	if name, how := m.data.Container(l, nil); name != "" {
		return "Port ini dipegang container Docker \"" + name + "\" (" + how + ").",
			"Buka modul Docker untuk melihat container dan lognya."
	}
	if m.env.IsRoot {
		return "Socket ini tidak punya proses pemilik yang terlihat di " + m.env.ProcRoot + ", padahal ubt sudah berjalan sebagai root. " +
				"Biasanya berarti prosesnya berada di namespace lain — mis. di dalam container, atau di host bila ubt dijalankan dari dalam container — " +
				"atau prosesnya sudah berhenti dan kernel belum menutup socketnya.",
			"Tekan a untuk memastikan lagi dengan ss."
	}
	return "Proses pemilik port ini tidak terlihat oleh user " + procs.Username(m.env.UID) + " karena milik user lain.",
		"Tekan a untuk mencari pemiliknya dengan sudo, atau jalankan sudo ubt."
}

// connectionLines merender ringkasan koneksi TCP yang sedang terbuka ke port ini.
func (m *DetailModel) connectionLines(width int) []string {
	t := ui.Current
	l := m.target.Listener
	if l.Proto != sysports.TCP {
		return nil
	}
	conns := m.data.Snap.ConnectionsTo(l)
	out := []string{ui.Section("Koneksi aktif")}
	if len(conns) == 0 {
		return append(out, ui.Wrap(t.Subtle.Render("Tidak ada koneksi yang sedang terbuka ke port ini."), width, " "))
	}
	lokal := 0
	for _, c := range conns {
		if c.LocalClient() {
			lokal++
		}
	}
	ringkas := fmt.Sprintf("%d koneksi terbuka", len(conns))
	if lokal > 0 {
		ringkas += fmt.Sprintf(" (%d dari server ini sendiri)", lokal)
	}
	out = append(out, ui.Wrap(t.Accent.Render(ringkas), width, " "), "")

	groups := sysports.GroupByRemote(conns)
	shown := groups
	if len(shown) > maxRemoteRows {
		shown = shown[:maxRemoteRows]
	}
	for _, g := range shown {
		asal := g.Addr.WithZone("").String()
		if g.Addr.IsLoopback() {
			asal += " (server ini)"
		}
		out = append(out, fmt.Sprintf("   %-42s %s", asal, t.Subtle.Render(fmt.Sprintf("%d koneksi", g.Count))))
	}
	if len(groups) > len(shown) {
		out = append(out, "   "+t.Muted.Render(fmt.Sprintf("… dan %d alamat lain", len(groups)-len(shown))))
	}
	return out
}

func (m *DetailModel) actionForm() ask.Form {
	port := m.target.Listener.Local.Port()
	return ask.Form{
		Title: "Aksi port " + strconv.Itoa(int(port)),
		Questions: []ask.Question{{
			ID:      "aksi",
			Prompt:  fmt.Sprintf("Apa yang ingin dilakukan dengan port %d?", port),
			Kind:    ask.Single,
			Options: Options(m.target, m.ctx()),
			Help:    "Menghentikan unit systemd atau container lebih rapi daripada membunuh prosesnya langsung, karena pengelolanya tahu proses itu sengaja dihentikan dan tidak menyalakannya lagi.",
		}},
	}
}

func (m *DetailModel) onResumed(result any) tea.Cmd {
	switch r := result.(type) {
	case ask.Result:
		if r.Cancelled {
			return nil
		}
		action := r.Answers["aksi"].Value()
		if r.ID == "kill-confirm" {
			if !r.Answers["kill"].Yes() {
				return nil
			}
			action = ActKill
		}
		p := Plan(action, m.target, m.ctx())
		if len(p.Steps) == 0 {
			return nil
		}
		m.lastAction = action
		return nav.Push(runflow.Confirm(p, m.env.Deps))
	case run.Outcome:
		if !r.Approved {
			return nil
		}
		if !r.OK() {
			m.status, m.statusOK = "Aksi tidak berhasil. Lihat output di layar sebelumnya untuk penyebabnya.", false
			return nil
		}
		switch m.lastAction {
		case ActTerm, ActStopUnit, ActDockerStop, ActKill:
			m.polls = 0
			m.status, m.statusOK = "Menunggu proses berhenti…", true
			return m.poll()
		case ActFindOwner:
			m.status, m.statusOK = "Pemilik port tampil di output ss. Jalankan sudo ubt untuk mengelolanya dari sini.", true
		}
	}
	return nil
}

// poll memeriksa apakah proses sudah berhenti; bila setelah beberapa saat masih hidup
// dan aksinya SIGTERM, tawarkan SIGKILL.
func (m *DetailModel) poll() tea.Cmd {
	p := m.target.Proc
	if p == nil {
		return nil
	}
	if !procs.Exists(m.env.ProcRoot, p.PID) {
		m.gone = true
		m.status, m.statusOK = fmt.Sprintf("✓ Proses %s (PID %d) sudah berhenti. Port %d kini kosong kecuali ada proses lain yang menyalakannya lagi.", p.Name, p.PID, m.target.Listener.Local.Port()), true
		return nil
	}
	m.polls++
	if m.polls < termPollCount {
		return tea.Tick(termPollInterval, func(time.Time) tea.Msg { return pollMsg{owner: m} })
	}
	if m.lastAction == ActTerm {
		m.status, m.statusOK = fmt.Sprintf("Proses masih berjalan %s setelah SIGTERM.", termPollInterval*termPollCount), false
		return nav.Push(ask.New(ask.Form{
			ID:    "kill-confirm",
			Title: "Paksa berhenti?",
			Questions: []ask.Question{{
				ID:     "kill",
				Prompt: fmt.Sprintf("%s (PID %d) belum berhenti. Paksa dengan SIGKILL?", p.Name, p.PID),
				Kind:   ask.Confirm,
				Options: []ask.Option{
					{Label: "Ya, paksa berhenti", Description: "Kernel langsung mematikan proses; data yang belum tersimpan bisa hilang.", Risk: risk.Dangerous},
					{Label: "Tidak, tunggu saja", Description: "Sebagian aplikasi butuh waktu lebih lama untuk menutup koneksi.", Recommended: true},
				},
			}},
		}))
	}
	m.status, m.statusOK = "Proses masih berjalan. Mungkin langsung dinyalakan lagi oleh pengelolanya; tekan esc lalu r untuk memuat ulang daftar.", false
	return nil
}

func (m *DetailModel) View(width, height int) string {
	t := ui.Current
	l := m.target.Listener
	var lines []string
	lines = append(lines, "")

	lines = append(lines, ui.Section("Port"))
	proto := strings.ToUpper(string(l.Proto))
	if l.IPv6 {
		proto += " (IPv6)"
	}
	lines = append(lines, ui.Detail([]ui.Pair{
		{Key: "Port", Value: strconv.Itoa(int(l.Local.Port())) + "/" + string(l.Proto)},
		{Key: "Protokol", Value: proto},
		{Key: "Alamat", Value: addrLabel(l), Hint: ScopeText(l)},
		{Key: "Pemilik socket", Value: procs.Username(l.UID)},
	}, width), "")

	p := m.target.Proc
	if p == nil {
		lines = append(lines, ui.Section("Proses"))
		reason, hint := m.ownerUnknown(l)
		lines = append(lines, ui.Wrap(t.Subtle.Render(reason), width, " "))
		if hint != "" {
			lines = append(lines, ui.Wrap(t.Accent.Render(hint), width, " "))
		}
	} else {
		lines = append(lines, ui.Section("Proses"))
		exe := p.Exe
		if exe == "" && p.Denied {
			exe = t.Muted.Render("(tidak terbaca tanpa root)")
		}
		cwd := p.Cwd
		if cwd == "" && p.Denied {
			cwd = t.Muted.Render("(tidak terbaca tanpa root)")
		}
		started := "—"
		if !p.Started.IsZero() {
			started = p.Started.Format("2006-01-02 15:04:05") + " (" + shared.Ago(p.Started, m.env.Now()) + ")"
		}
		parent := strconv.Itoa(p.PPID)
		if m.parent != nil {
			parent += " (" + m.parent.Name + ")"
		}
		pairs := []ui.Pair{
			{Key: "PID", Value: strconv.Itoa(p.PID)},
			{Key: "Nama", Value: p.Name},
			{Key: "User", Value: p.User},
			{Key: "Perintah", Value: p.CommandLine()},
			{Key: "Program", Value: exe},
			{Key: "Folder kerja", Value: cwd, Hint: "folder tempat proses dijalankan (cwd)"},
			{Key: "Mulai", Value: started},
			{Key: "Induk", Value: parent},
			{Key: "Memori", Value: shared.Bytes(p.RSSKB * 1024)},
		}
		if m.target.Shared > 1 {
			pairs = append(pairs, ui.Pair{Key: "Berbagi port", Value: fmt.Sprintf("%d proses (PID %s)", m.target.Shared, joinInts(l.PIDs)), Hint: "umum pada nginx/apache: satu proses induk dan beberapa worker"})
		}
		if name, how := m.data.Container(l, p); name != "" {
			pairs = append(pairs, ui.Pair{Key: "Container", Value: name, Hint: how})
		}
		switch {
		case p.Cgroup.Container != "":
			pairs = append(pairs, ui.Pair{Key: "Dikelola", Value: "container Docker " + shortID(p.Cgroup.Container)})
		case p.Cgroup.Service():
			scope := "unit sistem"
			if p.Cgroup.UserUnit {
				scope = "unit milik user"
			}
			pairs = append(pairs, ui.Pair{Key: "Dikelola", Value: p.Cgroup.Unit + " (" + scope + ")", Hint: "systemd bisa menyalakannya lagi bila dihentikan paksa"})
		case p.Cgroup.Unit != "":
			pairs = append(pairs, ui.Pair{Key: "Dikelola", Value: p.Cgroup.Unit, Hint: "scope: dijalankan dari sesi login/terminal, bukan service"})
		}
		lines = append(lines, ui.Detail(pairs, width), "")

		lines = append(lines, ui.Section("Cara cek sendiri"))
		cmds := []string{
			fmt.Sprintf("sudo ss -%slpn 'sport = :%d'", map[sysports.Proto]string{sysports.TCP: "t", sysports.UDP: "u"}[l.Proto], l.Local.Port()),
			fmt.Sprintf("ps -o pid,user,lstart,cmd -p %d", p.PID),
			fmt.Sprintf("sudo readlink /proc/%d/cwd", p.PID),
		}
		if p.Cgroup.Service() {
			user := ""
			if p.Cgroup.UserUnit {
				user = "--user "
			}
			cmds = append(cmds, "systemctl "+user+"status "+p.Cgroup.Unit)
		}
		for _, c := range cmds {
			lines = append(lines, "   "+t.Muted.Render("$ ")+t.Code.Render(" "+c+" "))
		}
	}

	lines = append(lines, "")
	lines = append(lines, m.connectionLines(width)...)

	if m.status != "" {
		style := t.Warning
		if m.statusOK {
			style = t.Success
		}
		lines = append(lines, "", ui.Wrap(style.Render(m.status), width, " "))
	}
	if !m.gone {
		lines = append(lines, "", " "+t.Accent.Render("Tekan a untuk menghentikan atau mencari pemilik port ini."))
	}

	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}

func joinInts(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ", ")
}
