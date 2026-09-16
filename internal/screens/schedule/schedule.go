// Package schedule adalah layar modul Penjadwalan: cron & systemd timer dalam satu tabel,
// penjelasan jadwal, dan wizard membuat jadwal baru.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	screenlogs "github.com/arif-rachim/ubuntu-tool/internal/screens/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	syssched "github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah daftar semua jadwal.
type Model struct {
	env      shared.Env
	sources  syssched.Sources
	username string
	jobs     []syssched.Job
	rows     []syssched.Job
	loaded   bool
	err      error
	table    ui.Table
	filter   ui.Filter
	message  string
}

type jobsMsg struct {
	owner *Model
	jobs  []syssched.Job
	err   error
}

// New membuat layar Penjadwalan.
func New(env shared.Env) *Model {
	name := "user"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return &Model{env: env, sources: syssched.DefaultSources, username: name, filter: ui.NewFilter("nama, command, user…"),
		table: ui.Table{Columns: []ui.Column{
			{Title: "Jenis", Width: 7},
			{Title: "Nama", Width: 16, Flex: 2},
			{Title: "Jadwal", Width: 20, Flex: 3},
			{Title: "Berikutnya", Width: 16},
			{Title: "User", Width: 8},
		}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string     { return "Penjadwalan" }
func (m *Model) Typing() bool      { return m.filter.Typing() }
func (m *Model) HandlesBack() bool { return m.filter.Active() }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("n", "buat jadwal baru"), b("enter", "detail & aksi"), b("/", "filter"), b("r", "muat ulang")}
}

func (m *Model) HelpText() string {
	return "Ada dua cara menjadwalkan tugas di Ubuntu. cron: baris \"menit jam tanggal bulan hari command\" di crontab atau /etc/cron.d; sederhana dan umum. " +
		"systemd timer: pasangan file .timer + .service; log masuk journal, bisa mengejar jadwal yang terlewat saat server mati (Persistent), dan statusnya terlihat di systemctl. " +
		"Command setara: crontab -l, ls /etc/cron.d, systemctl list-timers --all"
}

func (m *Model) Init() tea.Cmd {
	r, src, name, now := m.env.Runner, m.sources, m.username, m.env.Now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		jobs, err := syssched.ReadAll(ctx, r, src, name, now())
		return jobsMsg{owner: m, jobs: jobs, err: err}
	}
}

func (m *Model) apply() {
	m.rows = m.rows[:0]
	var cells [][]string
	now := m.env.Now()
	for _, j := range m.jobs {
		if !m.filter.Match(j.Name + " " + j.Command + " " + j.User + " " + j.Human) {
			continue
		}
		next := "—"
		if !j.Next.IsZero() {
			next = relative(j.Next, now)
		}
		kind := map[syssched.Kind]string{syssched.KindTimer: "timer", syssched.KindCron: "cron", syssched.KindCronDir: "cron.*", syssched.KindUserCron: "crontab"}[j.Kind]
		human := j.Human
		if j.Problem != "" {
			human = "⚠ " + j.Problem
		}
		m.rows = append(m.rows, j)
		cells = append(cells, []string{kind, j.Name, human, next, j.User})
	}
	m.table.SetRows(cells)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if r >= len(m.rows) {
			return lipgloss.NewStyle()
		}
		switch {
		case c == 2 && m.rows[r].Problem != "":
			return t.Warning
		case c == 1 && m.rows[r].Managed:
			return t.Accent
		case c == 0 || c == 4:
			return t.Subtle
		}
		return lipgloss.NewStyle()
	}
}

func relative(t, now time.Time) string {
	d := t.Sub(now)
	switch {
	case d < 0:
		return "terlewat"
	case d < time.Hour:
		return fmt.Sprintf("%d menit lagi", int(d.Minutes())+1)
	case d < 24*time.Hour:
		return "pukul " + t.Format("15:04")
	case d < 7*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("2006-01-02")
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if handled, changed, cmd := m.filter.Update(msg); handled {
		if changed {
			m.apply()
		}
		return m, cmd
	}
	switch msg := msg.(type) {
	case jobsMsg:
		if msg.owner == m {
			m.loaded, m.jobs, m.err = true, msg.jobs, msg.err
			m.apply()
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled || r.ID != "new" {
				return m, nil
			}
			plan, err := PlanFromAnswers(r.Answers)
			if err != nil {
				m.message = "✗ " + err.Error()
				return m, nil
			}
			return m, nav.Push(runflow.Confirm(plan, m.env.Deps))
		case run.Outcome:
			if r.Approved && r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			}
		}
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		switch msg.String() {
		case "n":
			m.message = ""
			return m, nav.Push(ask.New(NewForm(m.username)))
		case "enter":
			if m.table.Cursor < len(m.rows) {
				return m, nav.Push(newDetail(m.env, m.rows[m.table.Cursor]))
			}
		}
	}
	return m, nil
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Membaca jadwal…")
	}
	problems := 0
	for _, j := range m.jobs {
		if j.Problem != "" {
			problems++
		}
	}
	summary := fmt.Sprintf("%d jadwal", len(m.jobs))
	if problems > 0 {
		summary += t.Warning.Render(fmt.Sprintf(" · %d bermasalah", problems))
	}
	top := "\n " + t.Title.Render(summary) + "   " + t.Muted.Render("command setara: crontab -l · ls /etc/cron.d · systemctl list-timers --all") + "\n"
	if fv := m.filter.View(); fv != "" {
		top += fv + "\n"
	}
	if m.message != "" {
		top += ui.Wrap(t.Accent.Render(m.message), width, " ") + "\n"
	}
	bottom := "\n " + t.Accent.Render("n") + t.Subtle.Render(" buat jadwal baru, mis. backup setiap jam 2 pagi")
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)-lipgloss.Height(bottom)-1), width, height-lipgloss.Height(bottom)) + bottom
}

// NewForm adalah wizard membuat jadwal baru.
func NewForm(username string) ask.Form {
	users := []ask.Option{{Value: username, Label: username, Description: "user yang sedang login", Recommended: username != "root"}}
	if username != "root" {
		users = append(users, ask.Option{Value: "root", Label: "root", Description: "Hak akses penuh. Pakai hanya bila tugasnya memang butuh (mis. backup /etc).", Risk: 1})
	}
	isFreq := func(kinds ...string) func(ask.Answers) bool {
		return func(a ask.Answers) bool {
			v := a["freq"].Value()
			for _, k := range kinds {
				if v == k {
					return true
				}
			}
			return false
		}
	}
	return ask.Form{
		ID:    "new",
		Title: "Jadwal baru",
		Questions: []ask.Question{
			{ID: "desc", Header: "Nama", Kind: ask.Text, Prompt: "Apa yang dijadwalkan?", Placeholder: "Backup database harian",
				Help: "Nama singkat untuk mengenali jadwal ini. Dipakai juga sebagai nama file (diawali ubt-).",
				Validate: func(s string) error {
					_, err := syssched.SlugName(s)
					return err
				}},
			{ID: "command", Header: "Command", Kind: ask.Text, Prompt: "Command apa yang dijalankan?", Placeholder: "/usr/local/bin/backup.sh",
				Help:     "Tulis path lengkap program (mis. /usr/bin/pg_dump). Jadwal berjalan tanpa terminal dan dengan PATH minim, jadi alias dan variabel dari .bashrc tidak tersedia.",
				Validate: validateCommand},
			{ID: "freq", Header: "Frekuensi", Kind: ask.Single, Prompt: "Seberapa sering?", Options: []ask.Option{
				{Value: "5", Label: "Setiap 5 menit"},
				{Value: "15", Label: "Setiap 15 menit"},
				{Value: "hourly", Label: "Setiap jam"},
				{Value: "daily", Label: "Setiap hari", Recommended: true, Description: "pada jam tertentu"},
				{Value: "weekly", Label: "Setiap minggu", Description: "pada hari & jam tertentu"},
				{Value: "custom", Label: "Ekspresi cron sendiri", Description: "menit jam tanggal bulan hari, mis. 0 9 * * 1-5"},
			}},
			{ID: "weekday", Header: "Hari", Kind: ask.Single, Prompt: "Hari apa?", When: isFreq("weekly"), Options: []ask.Option{
				{Value: "1", Label: "Senin"}, {Value: "2", Label: "Selasa"}, {Value: "3", Label: "Rabu"}, {Value: "4", Label: "Kamis"},
				{Value: "5", Label: "Jumat"}, {Value: "6", Label: "Sabtu"}, {Value: "0", Label: "Minggu", Recommended: true},
			}},
			{ID: "time", Header: "Jam", Kind: ask.Text, Prompt: "Pukul berapa? (HH:MM, 24 jam)", Default: []string{"02:00"}, When: isFreq("daily", "weekly"),
				Help: "Tugas berat seperti backup sebaiknya dijadwalkan saat server sepi, mis. dini hari.", Validate: validateTime},
			{ID: "cron", Header: "Ekspresi", Kind: ask.Text, Prompt: "Ekspresi cron (5 kolom)?", Placeholder: "0 9 * * 1-5", When: isFreq("custom"),
				Help: "Urutan kolom: menit (0-59) jam (0-23) tanggal (1-31) bulan (1-12) hari (0-7, 0 dan 7 = Minggu). * berarti semua, */15 berarti setiap 15, 1-5 berarti rentang.",
				Validate: func(s string) error {
					e, err := syssched.ParseCron(s)
					if err != nil {
						return err
					}
					if e.Special == "@reboot" {
						return errors.New("@reboot tidak didukung wizard; gunakan unit systemd biasa")
					}
					return ask.Warn("Artinya: " + e.Describe() + ". Tekan enter lagi bila sudah benar.")
				}},
			{ID: "user", Header: "User", Kind: ask.Single, Prompt: "Dijalankan sebagai user siapa?", Options: users, Other: true,
				Validate: func(s string) error {
					if _, err := user.Lookup(strings.TrimSpace(s)); err != nil {
						return fmt.Errorf("user %q tidak ada", s)
					}
					return nil
				}},
			{ID: "kind", Header: "Jenis", Kind: ask.Single, Prompt: "Pakai systemd timer atau cron?", Options: []ask.Option{
				{Value: "timer", Label: "systemd timer", Recommended: true, Description: "Log otomatis masuk journal, status terlihat di modul Service, dan tetap dijalankan setelah server menyala bila jadwalnya terlewat."},
				{Value: "cron", Label: "cron (/etc/cron.d)", Description: "Satu file sederhana, familiar bagi banyak admin. ubt meneruskan output ke journal lewat logger."},
			}},
		},
	}
}

func validateCommand(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("command tidak boleh kosong")
	}
	if strings.ContainsAny(s, "\n") {
		return errors.New("satu baris saja; untuk banyak langkah buat script lalu jadwalkan script-nya")
	}
	return nil
}

func validateTime(s string) error {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return errors.New("format HH:MM, mis. 02:30")
	}
	return nil
}

// PlanFromAnswers membangun Plan dari jawaban wizard.
func PlanFromAnswers(a ask.Answers) (run.Plan, error) {
	name, err := syssched.SlugName(a["desc"].Value())
	if err != nil {
		return run.Plan{}, err
	}
	spec := syssched.Spec{Name: name, Description: strings.TrimSpace(a["desc"].Value()), Command: strings.TrimSpace(a["command"].Value()), User: strings.TrimSpace(a["user"].Value())}
	switch f := a["freq"].Value(); f {
	case "5", "15":
		n, _ := strconv.Atoi(f)
		spec.Freq = syssched.Frequency{Kind: "minutes", Minutes: n}
	case "hourly":
		spec.Freq = syssched.Frequency{Kind: "hourly"}
	case "daily", "weekly":
		h, mm, _ := strings.Cut(a["time"].Value(), ":")
		spec.Freq.Kind = f
		spec.Freq.Hour, _ = strconv.Atoi(h)
		spec.Freq.Minute, _ = strconv.Atoi(mm)
		spec.Freq.Weekday, _ = strconv.Atoi(a["weekday"].Value())
	case "custom":
		spec.Freq = syssched.Frequency{Kind: "custom", Custom: a["cron"].Value()}
	}
	if a["kind"].Value() == "cron" {
		return syssched.CreateCronPlan(spec), nil
	}
	return syssched.CreateTimerPlan(spec)
}

// detailModel menampilkan satu jadwal.
type detailModel struct {
	env    shared.Env
	job    syssched.Job
	status string
}

func newDetail(env shared.Env, j syssched.Job) *detailModel { return &detailModel{env: env, job: j} }

func (m *detailModel) Title() string { return m.job.Name }
func (m *detailModel) Init() tea.Cmd { return nil }

func (m *detailModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	keys := []key.Binding{b("x", "jalankan sekarang"), b("l", "lihat log")}
	if m.job.Managed {
		keys = append(keys, b("d", "hapus jadwal"))
	}
	return keys
}

func (m *detailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "x":
			return m, nav.Push(runflow.Confirm(syssched.RunNowPlan(m.job), m.env.Deps))
		case "l":
			return m, nav.Push(screenlogs.NewEntries(m.env, "Log "+m.job.Name, m.logQuery()))
		case "d":
			if m.job.Managed {
				return m, nav.Push(runflow.Confirm(syssched.RemovePlan(m.job), m.env.Deps))
			}
		}
	case nav.ResumedMsg:
		if r, ok := msg.Result.(run.Outcome); ok && r.Approved {
			if r.OK() {
				m.status = "✓ " + r.Plan.Title + " selesai."
				if strings.HasPrefix(r.Plan.Title, "Hapus") {
					return m, nav.Pop(r)
				}
			} else {
				m.status = "✗ " + r.Plan.Title + " gagal. Lihat log (l) untuk penyebabnya."
			}
		}
	}
	return m, nil
}

func (m *detailModel) logQuery() syslogs.Query {
	j := m.job
	q := syslogs.Query{Boot: nil, Priority: -1, Lines: 300, Since: "7 days ago"}
	switch {
	case j.Kind == syssched.KindTimer:
		q.Unit = j.Unit
	case j.Managed:
		q.Tag = j.Name
	default:
		q.Unit = "cron.service"
	}
	return q
}

func (m *detailModel) View(width, height int) string {
	t := ui.Current
	j := m.job
	now := m.env.Now()
	pairs := []ui.Pair{
		{Key: "Jenis", Value: map[syssched.Kind]string{syssched.KindTimer: "systemd timer", syssched.KindCron: "baris cron", syssched.KindCronDir: "script run-parts", syssched.KindUserCron: "crontab user"}[j.Kind]},
		{Key: "Jadwal", Value: j.Schedule, Hint: j.Human},
		{Key: "User", Value: j.User},
		{Key: "Command", Value: j.Command},
		{Key: "Sumber", Value: j.Source},
	}
	if j.Unit != "" {
		pairs = append(pairs, ui.Pair{Key: "Service", Value: j.Unit})
	}
	if !j.Last.IsZero() {
		pairs = append(pairs, ui.Pair{Key: "Terakhir jalan", Value: j.Last.Format("2006-01-02 15:04") + " (" + shared.Ago(j.Last, now) + ")"})
	}
	lines := []string{""}
	if j.Problem != "" {
		lines = append(lines, ui.Wrap(t.Warning.Render("⚠ "+j.Problem), width, " "), "")
	}
	lines = append(lines, ui.Detail(pairs, width))
	if e, err := syssched.ParseCron(j.Schedule); err == nil && j.Kind != syssched.KindTimer && e.Special != "@reboot" {
		lines = append(lines, "", ui.Section("5 jadwal berikutnya"))
		for _, n := range e.Next(now, 5) {
			lines = append(lines, "   "+n.Format("Mon 2006-01-02 15:04"))
		}
	} else if !j.Next.IsZero() {
		lines = append(lines, "", ui.Section("Jadwal berikutnya"), "   "+j.Next.Format("Mon 2006-01-02 15:04"))
	}
	lines = append(lines, "", ui.Section("Cara cek sendiri"))
	cmds := []string{"crontab -l", "cat " + j.Source}
	if j.Kind == syssched.KindTimer {
		cmds = []string{"systemctl status " + j.Name, "systemctl cat " + j.Name + " " + j.Unit, "journalctl -u " + j.Unit + " -n 50"}
	} else if j.Schedule != "" && !strings.HasPrefix(j.Schedule, "@") {
		cmds = append(cmds, "systemd-analyze calendar …  (untuk timer)")
	}
	for _, c := range cmds {
		lines = append(lines, "   "+t.Muted.Render("$ ")+t.Code.Render(" "+c+" "))
	}
	if m.status != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.status), width, " "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}
