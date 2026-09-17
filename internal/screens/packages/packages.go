// Package packages adalah layar modul Paket (apt): update keamanan, cari & install, hapus,
// riwayat, repo, dan perbaikan paket rusak.
package packages

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspkg "github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah dashboard paket.
type Model struct {
	env     shared.Env
	paths   syspkg.Paths
	status  syspkg.Status
	loaded  bool
	message string
	offset  int
}

type statusMsg struct {
	owner  *Model
	status syspkg.Status
}

// New membuat dashboard paket.
func New(env shared.Env) *Model { return &Model{env: env, paths: syspkg.DefaultPaths} }

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Paket (apt)" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	keys := []key.Binding{b("u", "cek update"), b("s", "cari & install"), b("i", "terpasang"), b("h", "riwayat"), b("p", "repo")}
	if len(m.status.Upgradable) > 0 {
		keys = append([]key.Binding{b("g", "pasang update")}, keys...)
	}
	return keys
}

func (m *Model) HelpText() string {
	return "apt mengambil paket dari repo (server paket). \"apt update\" hanya memperbarui daftar versi; \"apt upgrade\" yang benar-benar memasang versi baru. " +
		"Update keamanan (dari repo *-security) sebaiknya dipasang secepatnya. remove menghapus program tetapi menyimpan konfigurasi; purge ikut menghapus konfigurasinya."
}

func (m *Model) Init() tea.Cmd {
	r, p, now := m.env.Runner, m.paths, m.env.Now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return statusMsg{owner: m, status: syspkg.ReadStatus(ctx, r, p, now())}
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		if msg.owner == m {
			m.status, m.loaded = msg.status, true
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			switch r.ID {
			case "search":
				return m, nav.Push(newSearch(m.env, r.Answers["kata"].Value()))
			}
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " selesai."
				} else {
					m.message = "✗ " + r.Plan.Title + " tidak selesai."
				}
			}
		}
		return m, m.Init()
	case tea.KeyPressMsg:
		m.message = ""
		s := m.status
		if s.LockHolder != nil && strings.Contains("ugfb", msg.String()) {
			m.message = fmt.Sprintf("apt sedang dipakai %s (PID %d). Tunggu sampai selesai; jangan hapus file lock.", s.LockHolder.Name, s.LockHolder.PID)
			return m, nil
		}
		switch msg.String() {
		case "u":
			return m, nav.Push(runflow.Confirm(syspkg.UpdatePlan(), m.env.Deps))
		case "g":
			if len(s.Upgradable) > 0 {
				return m, nav.Push(runflow.Confirm(syspkg.UpgradePlan(len(s.Upgradable), securityCount(s.Upgradable)), m.env.Deps))
			}
		case "f":
			if s.Broken != "" {
				return m, nav.Push(runflow.Confirm(syspkg.FixBrokenPlan(), m.env.Deps))
			}
		case "b":
			if s.RebootNeeded {
				return m, nav.Push(runflow.Confirm(syspkg.RebootPlan(), m.env.Deps))
			}
		case "a":
			if !s.Auto.Upgrade {
				return m, nav.Push(runflow.Confirm(syspkg.EnableAutoUpgradePlan(), m.env.Deps))
			}
		case "s":
			return m, nav.Push(ask.New(ask.Form{ID: "search", Title: "Cari paket", Questions: []ask.Question{{
				ID: "kata", Prompt: "Cari paket apa?", Kind: ask.Text, Placeholder: "nginx, htop, postgresql…",
				Help: "Pencarian di nama dan deskripsi paket (apt-cache search).",
				Validate: func(v string) error {
					if len(strings.TrimSpace(v)) < 2 {
						return errors.New("minimal 2 huruf")
					}
					return nil
				},
			}}}))
		case "i":
			return m, nav.Push(newInstalled(m.env))
		case "h":
			return m, nav.Push(newHistory(m.env, m.paths.History))
		case "p":
			return m, nav.Push(newRepos(m.env, m.paths.AptDir))
		case "down", "j":
			m.offset++
		case "up", "k":
			m.offset = max(m.offset-1, 0)
		}
	}
	return m, nil
}

func securityCount(ups []syspkg.Upgradable) int {
	n := 0
	for _, u := range ups {
		if u.Security {
			n++
		}
	}
	return n
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Memeriksa paket…")
	}
	s := m.status
	var lines []string
	lines = append(lines, "")
	if s.LockHolder != nil {
		lines = append(lines, ui.Wrap(t.Warning.Render(fmt.Sprintf("⏳ apt sedang dipakai %s (PID %d, %s). Tunggu sampai selesai. Jangan menghapus /var/lib/dpkg/lock*: bisa merusak database paket.", s.LockHolder.Name, s.LockHolder.PID, s.LockHolder.CommandLine())), width, " "), "")
	}
	if s.Broken != "" {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ Ada instalasi paket yang terputus atau rusak.")+t.Subtle.Render(" Tekan f untuk memperbaikinya (dpkg --configure -a lalu apt --fix-broken install)."), width, " "))
		for _, l := range strings.Split(s.Broken, "\n")[:min(4, len(strings.Split(s.Broken, "\n")))] {
			lines = append(lines, "     "+t.Muted.Render(l))
		}
		lines = append(lines, "")
	}
	if s.RebootNeeded {
		pkgs := ""
		if len(s.RebootPkgs) > 0 {
			pkgs = " karena " + strings.Join(s.RebootPkgs, ", ")
		}
		lines = append(lines, ui.Wrap(t.Warning.Render("⟳ Server perlu restart"+pkgs+".")+t.Subtle.Render(" Update kernel/library inti baru aktif setelah restart. Tekan b saat waktunya tepat."), width, " "), "")
	}

	sec := securityCount(s.Upgradable)
	lines = append(lines, " "+t.Title.Render("Update")+"   "+t.Muted.Render("command setara: apt list --upgradable"))
	age := ""
	if s.ListAge > 0 {
		age = " · daftar paket diperbarui " + shared.Ago(time.Now().Add(-s.ListAge), time.Now())
	}
	switch {
	case len(s.Upgradable) == 0:
		lines = append(lines, "   "+t.Success.Render("✓ semua paket versi terbaru")+t.Subtle.Render(age+" — tekan u untuk cek ulang"))
	default:
		summary := fmt.Sprintf("%d update tersedia", len(s.Upgradable))
		style := t.Warning
		if sec > 0 {
			summary += fmt.Sprintf(", %d di antaranya update keamanan", sec)
			style = t.Danger
		}
		lines = append(lines, "   "+style.Render(summary)+t.Subtle.Render(age+" — tekan g untuk memasang"))
		for i, u := range s.Upgradable {
			if i == 8 {
				lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf("+ %d lainnya", len(s.Upgradable)-8)))
				break
			}
			tag := ""
			if u.Security {
				tag = t.Danger.Render(" keamanan")
			}
			lines = append(lines, fmt.Sprintf("     %-32s %s → %s%s", u.Name, t.Muted.Render(u.Old), u.New, tag))
		}
	}
	if s.ListAge > 7*24*time.Hour {
		lines = append(lines, "   "+t.Warning.Render("⚠ daftar paket sudah lebih dari seminggu tidak diperbarui; update keamanan baru mungkin belum terlihat"))
	}

	lines = append(lines, "", " "+t.Title.Render("Update keamanan otomatis")+"   "+t.Muted.Render("file: /etc/apt/apt.conf.d/20auto-upgrades"))
	switch {
	case s.Auto.Upgrade:
		lines = append(lines, "   "+t.Success.Render("✓ aktif")+t.Subtle.Render(" — server memasang update keamanan sendiri setiap hari"))
	default:
		lines = append(lines, "   "+t.Warning.Render("⚠ tidak aktif")+t.Subtle.Render(" — tekan a untuk mengaktifkan (sangat disarankan untuk server)"))
	}
	if s.Snaps > 0 {
		lines = append(lines, "", " "+t.Title.Render("Snap")+"   "+t.Subtle.Render(fmt.Sprintf("%d snap terpasang (diperbarui otomatis oleh snapd) · command setara: snap list", s.Snaps)))
	}

	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", ui.Section("Aksi"),
		"   "+t.Accent.Render("u")+"  cek update (apt update)          "+t.Accent.Render("s")+"  cari & install paket",
		"   "+t.Accent.Render("i")+"  paket terpasang & hapus         "+t.Accent.Render("h")+"  riwayat install/upgrade (apt history)",
		"   "+t.Accent.Render("p")+"  repo & PPA",
	)
	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}

// ---------- Cari paket ----------

type searchModel struct {
	env     shared.Env
	query   string
	results []syspkg.SearchResult
	loaded  bool
	err     error
	table   ui.Table
}

type searchMsg struct {
	owner *searchModel
	res   []syspkg.SearchResult
	err   error
}

func newSearch(env shared.Env, query string) *searchModel {
	return &searchModel{env: env, query: strings.TrimSpace(query), table: ui.Table{Columns: []ui.Column{
		{Title: "Paket", Width: 20, Flex: 1},
		{Title: "Deskripsi", Width: 30, Flex: 3},
	}}}
}

func (m *searchModel) Title() string { return "Cari: " + m.query }
func (m *searchModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail & install"))}
}

func (m *searchModel) Init() tea.Cmd {
	r, q := m.env.Runner, m.query
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, _, err := r.Capture(ctx, run.Command{Argv: []string{"apt-cache", "search", q}})
		res := syspkg.ParseSearch(out)
		// Paket yang namanya persis/diawali kata kunci lebih relevan.
		var exact, prefix, rest []syspkg.SearchResult
		for _, x := range res {
			switch {
			case x.Name == q:
				exact = append(exact, x)
			case strings.HasPrefix(x.Name, q):
				prefix = append(prefix, x)
			default:
				rest = append(rest, x)
			}
		}
		return searchMsg{owner: m, res: append(append(exact, prefix...), rest...), err: err}
	}
}

func (m *searchModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case searchMsg:
		if msg.owner == m {
			m.loaded, m.results, m.err = true, msg.res, msg.err
			rows := make([][]string, len(m.results))
			for i, r := range m.results {
				rows[i] = []string{r.Name, r.Description}
			}
			m.table.SetRows(rows)
		}
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "enter" && m.table.Cursor < len(m.results) {
			return m, nav.Push(newDetail(m.env, m.results[m.table.Cursor].Name))
		}
	}
	return m, nil
}

func (m *searchModel) View(width, height int) string {
	t := ui.Current
	top := "\n " + t.Muted.Render("command setara: apt-cache search "+run.QuoteShell(m.query)) + "\n"
	switch {
	case !m.loaded:
		return top + "\n " + t.Subtle.Render("Mencari…")
	case len(m.results) == 0:
		return top + ui.EmptyState("Tidak ditemukan", "Tidak ada paket yang cocok dengan "+m.query+".", "coba kata lain, atau jalankan cek update (u) bila daftar paket belum pernah diperbarui", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-3), width, height)
}

// ---------- Detail paket ----------

type detailModel struct {
	env    shared.Env
	name   string
	policy syspkg.Policy
	show   []string
	loaded bool
}

type detailMsg struct {
	owner  *detailModel
	policy syspkg.Policy
	show   []string
}

func newDetail(env shared.Env, name string) *detailModel { return &detailModel{env: env, name: name} }

func (m *detailModel) Title() string { return m.name }
func (m *detailModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "install / hapus"))}
}

func (m *detailModel) Init() tea.Cmd {
	r, name := m.env.Runner, m.name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pol, _, _ := r.Capture(ctx, run.Command{Argv: []string{"apt-cache", "policy", name}})
		show, _, _ := r.Capture(ctx, run.Command{Argv: []string{"apt-cache", "show", "--no-all-versions", name}})
		var keep []string
		for _, l := range strings.Split(show, "\n") {
			for _, k := range []string{"Section:", "Installed-Size:", "Depends:", "Homepage:", "Description:"} {
				if strings.HasPrefix(l, k) {
					keep = append(keep, l)
				}
			}
		}
		return detailMsg{owner: m, policy: syspkg.ParsePolicy(pol), show: keep}
	}
}

func (m *detailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detailMsg:
		if msg.owner == m {
			m.loaded, m.policy, m.show = true, msg.policy, msg.show
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			var p run.Plan
			switch r.Answers["aksi"].Value() {
			case "install", "upgrade":
				p = syspkg.InstallPlan(m.name)
			case "remove":
				p = syspkg.RemovePlan(m.name, false)
			case "purge":
				p = syspkg.RemovePlan(m.name, true)
			}
			if len(p.Steps) > 0 {
				return m, nav.Push(runflow.Confirm(p, m.env.Deps))
			}
		case run.Outcome:
			return m, m.Init()
		}
	case tea.KeyPressMsg:
		if msg.String() == "a" && m.loaded {
			return m, nav.Push(ask.New(actionForm(m.name, m.policy)))
		}
	}
	return m, nil
}

func actionForm(name string, p syspkg.Policy) ask.Form {
	var opts []ask.Option
	switch {
	case p.Installed == "" && p.Candidate != "":
		opts = append(opts, ask.Option{Value: "install", Label: "Install " + name, Recommended: true, Description: "Pasang versi " + p.Candidate + " beserta dependensinya."})
	case p.Installed != "":
		if p.Candidate != "" && p.Candidate != p.Installed {
			opts = append(opts, ask.Option{Value: "upgrade", Label: "Upgrade ke " + p.Candidate, Recommended: true, Description: "Terpasang " + p.Installed + "."})
		}
		opts = append(opts,
			ask.Option{Value: "remove", Label: "Hapus (remove)", Description: "Hapus program, simpan file konfigurasi di /etc. Bisa dipasang lagi dengan setelan lama.", Risk: 1},
			ask.Option{Value: "purge", Label: "Hapus total (purge)", Description: "Hapus program beserta konfigurasinya. Data aplikasi (mis. database di /var/lib) bisa ikut terhapus.", Risk: 2},
		)
	}
	if len(opts) == 0 {
		opts = append(opts, ask.Option{Value: "none", Label: "Tidak ada aksi", Disabled: "paket tidak ada di repo mana pun; jalankan cek update dulu"})
	}
	return ask.Form{ID: "pkg-action", Title: "Aksi " + name, Questions: []ask.Question{{ID: "aksi", Prompt: "Apa yang ingin dilakukan dengan " + name + "?", Kind: ask.Single, Options: opts}}}
}

func (m *detailModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca "+m.name+"…")
	}
	p := m.policy
	installed := p.Installed
	if installed == "" {
		installed = t.Muted.Render("belum terpasang")
	}
	candidate := p.Candidate
	if candidate == "" {
		candidate = t.Warning.Render("tidak ada di repo")
	}
	pairs := []ui.Pair{
		{Key: "Paket", Value: m.name},
		{Key: "Terpasang", Value: installed},
		{Key: "Versi terbaru", Value: candidate},
		{Key: "Dari repo", Value: strings.Join(p.Sources, "\n")},
	}
	for _, l := range m.show {
		k, v, _ := strings.Cut(l, ": ")
		label := map[string]string{"Section": "Kategori", "Installed-Size": "Ukuran", "Depends": "Butuh", "Homepage": "Situs", "Description": "Deskripsi"}[k]
		if k == "Installed-Size" {
			v += " KiB setelah terpasang"
		}
		pairs = append(pairs, ui.Pair{Key: label, Value: v})
	}
	lines := []string{"", ui.Detail(pairs, width), "", ui.Section("Cara cek sendiri")}
	for _, c := range []string{"apt-cache policy " + m.name, "apt show " + m.name, "dpkg -L " + m.name + "   (file apa saja yang dipasang)"} {
		lines = append(lines, "   "+t.Muted.Render("$ ")+t.Code.Render(" "+c+" "))
	}
	lines = append(lines, "", " "+t.Accent.Render("Tekan a untuk install atau hapus."))
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}

// ---------- Paket terpasang ----------

type installedModel struct {
	env    shared.Env
	all    []syspkg.Installed
	rows   []syspkg.Installed
	filter ui.Filter
	table  ui.Table
	loaded bool
	err    error
}

type installedMsg struct {
	owner *installedModel
	pkgs  []syspkg.Installed
	err   error
}

func newInstalled(env shared.Env) *installedModel {
	return &installedModel{env: env, filter: ui.NewFilter("nama paket…"), table: ui.Table{Columns: []ui.Column{
		{Title: "Paket", Width: 24, Flex: 2},
		{Title: "Versi", Width: 16, Flex: 1},
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "Status", Width: 12},
	}}}
}

func (m *installedModel) Title() string     { return "Paket terpasang" }
func (m *installedModel) Typing() bool      { return m.filter.Typing() }
func (m *installedModel) HandlesBack() bool { return m.filter.Active() }
func (m *installedModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")), key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail & hapus"))}
}

func (m *installedModel) Init() tea.Cmd {
	r := m.env.Runner
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		out, _, err := r.Capture(ctx, run.Command{Argv: syspkg.InstalledArgs})
		return installedMsg{owner: m, pkgs: syspkg.ParseInstalled(out), err: err}
	}
}

func (m *installedModel) apply() {
	m.rows = m.rows[:0]
	var cells [][]string
	for _, p := range m.all {
		if !m.filter.Match(p.Name) {
			continue
		}
		status := map[string]string{"ii": "terpasang", "rc": "sisa config", "iU": "belum dikonfigurasi", "iF": "gagal"}[strings.TrimSpace(p.Status)]
		if status == "" {
			status = p.Status
		}
		m.rows = append(m.rows, p)
		cells = append(cells, []string{p.Name, p.Version, shared.Bytes(p.SizeKB * 1024), status})
	}
	m.table.SetRows(cells)
}

func (m *installedModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if handled, changed, cmd := m.filter.Update(msg); handled {
		if changed {
			m.apply()
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case installedMsg:
		if msg.owner == m {
			m.loaded, m.all, m.err = true, msg.pkgs, msg.err
			m.apply()
		}
	case nav.ResumedMsg, nav.RefreshMsg:
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		if msg.String() == "enter" && m.table.Cursor < len(m.rows) {
			return m, nav.Push(newDetail(m.env, m.rows[m.table.Cursor].Name))
		}
	}
	return m, nil
}

func (m *installedModel) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca paket terpasang…")
	}
	var total int64
	for _, p := range m.all {
		total += p.SizeKB
	}
	top := "\n " + t.Title.Render(fmt.Sprintf("%d paket, total %s", len(m.all), shared.Bytes(total*1024))) + "   " + t.Muted.Render("urut terbesar · command setara: dpkg-query -W") + "\n"
	if fv := m.filter.View(); fv != "" {
		top += fv + "\n"
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)-1), width, height)
}

// ---------- Riwayat apt ----------

type historyModel struct {
	env     shared.Env
	path    string
	entries []syspkg.HistoryEntry
	err     error
	viewer  ui.Viewer
}

func newHistory(env shared.Env, path string) *historyModel {
	m := &historyModel{env: env, path: path}
	m.entries, m.err = syspkg.ReadHistory(path, 200)
	t := ui.Current
	var lines []string
	for _, h := range m.entries {
		who := ""
		if h.RequestedBy != "" {
			who = t.Subtle.Render(" oleh " + h.RequestedBy)
		}
		lines = append(lines, t.Accent.Render(h.Start.Format("2006-01-02 15:04"))+"  "+t.Title.Render(h.Summary())+who)
		lines = append(lines, "   "+t.Muted.Render("$ "+h.Commandline))
		for _, x := range []struct {
			label string
			pkgs  []string
		}{{"install", h.Install}, {"upgrade", h.Upgrade}, {"hapus", h.Remove}, {"purge", h.Purge}} {
			if len(x.pkgs) > 0 {
				lines = append(lines, "   "+x.label+": "+strings.Join(x.pkgs, ", "))
			}
		}
		lines = append(lines, "")
	}
	m.viewer = ui.Viewer{Lines: lines}
	return m
}

func (m *historyModel) Title() string { return "Riwayat apt" }
func (m *historyModel) Init() tea.Cmd { return nil }
func (m *historyModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "gulir"))}
}
func (m *historyModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		m.viewer.HandleKey(k.String())
	}
	return m, nil
}

func (m *historyModel) View(width, height int) string {
	t := ui.Current
	top := "\n " + t.Subtle.Render("Berguna saat sesuatu tiba-tiba rusak: lihat paket apa yang terakhir dipasang atau di-upgrade.") + "   " + t.Muted.Render("file: "+m.path) + "\n"
	switch {
	case m.err != nil:
		return top + ui.ErrorState(m.err, "", width)
	case len(m.entries) == 0:
		return top + ui.EmptyState("Belum ada riwayat", "", "", width)
	}
	return top + "\n" + m.viewer.View(width, height-3, false)
}

// ---------- Repo ----------

var ppaRe = regexp.MustCompile(`^ppa:[a-z0-9][a-z0-9.+-]*/[a-z0-9][a-z0-9.+-]*$`)

type reposModel struct {
	env     shared.Env
	dir     string
	sources []syspkg.Source
	message string
}

func newRepos(env shared.Env, dir string) *reposModel {
	return &reposModel{env: env, dir: dir, sources: syspkg.ReadSources(dir)}
}

func (m *reposModel) Title() string { return "Repo & PPA" }
func (m *reposModel) Init() tea.Cmd { return nil }
func (m *reposModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "tambah PPA"))}
}

func (m *reposModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "a" {
			return m, nav.Push(ask.New(ask.Form{ID: "ppa", Title: "Tambah PPA", Questions: []ask.Question{{
				ID: "ppa", Prompt: "PPA apa yang ingin ditambahkan?", Kind: ask.Text, Placeholder: "ppa:nama-pemilik/nama-ppa",
				Help: "PPA adalah repo pribadi di Launchpad. Pemiliknya bisa memasang apa pun di server ini lewat update, jadi tambahkan hanya PPA dari sumber tepercaya (biasanya disebut di dokumentasi resmi aplikasinya).",
				Validate: func(v string) error {
					if !ppaRe.MatchString(strings.TrimSpace(v)) {
						return errors.New("format harus ppa:pemilik/nama, huruf kecil")
					}
					return nil
				},
			}}}))
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if !r.Cancelled {
				return m, nav.Push(runflow.Confirm(syspkg.AddPPAPlan(strings.TrimSpace(r.Answers["ppa"].Value())), m.env.Deps))
			}
		case run.Outcome:
			m.sources = syspkg.ReadSources(m.dir)
			if r.Approved && r.OK() {
				m.message = "✓ PPA ditambahkan. Jalankan cek update (u) sebelum memasang paket darinya."
			}
		}
	}
	return m, nil
}

func (m *reposModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Muted.Render("command setara: ls /etc/apt/sources.list.d/ · Ubuntu 24.04 memakai format .sources (deb822)"), ""}
	for _, s := range m.sources {
		state := t.Success.Render("aktif")
		if !s.Enabled {
			state = t.Muted.Render("nonaktif")
		}
		official := strings.Contains(s.URIs, "ubuntu.com")
		label := s.URIs
		if !official {
			label = t.Warning.Render(s.URIs) + t.Subtle.Render(" (pihak ketiga)")
		}
		lines = append(lines, fmt.Sprintf("   %s  %s  %s", state, label, t.Subtle.Render(s.Suites)))
		lines = append(lines, "      "+t.Muted.Render(strings.TrimPrefix(s.File, m.dir+"/")))
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", " "+t.Accent.Render("Tekan a untuk menambah PPA."))
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}
