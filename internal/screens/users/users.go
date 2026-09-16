// Package users adalah layar modul User & SSH: akun, grup admin, SSH key, hardening SSH server,
// dan audit login gagal — dengan pengaman supaya admin tidak terkunci dari server.
package users

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syslogs "github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/netinfo"
	sysusers "github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Paths adalah lokasi yang dibaca (diganti saat test).
type Paths struct {
	SSHDConfig string
	SSHDBinary string
	UFWConf    string
}

// DefaultPaths untuk sistem sungguhan.
var DefaultPaths = Paths{SSHDConfig: "/etc/ssh/sshd_config", SSHDBinary: "/usr/sbin/sshd", UFWConf: "/etc/ufw/ufw.conf"}

// State adalah semua data modul.
type State struct {
	Data       sysusers.Data
	SSHD       sysusers.SSHD
	OwnKeys    []sysusers.OwnKey
	Failed     []sysusers.FailedLogin
	FailedErr  error
	Socket     bool
	UFWActive  bool
	CurrentKey bool
	KeyOwners  []string // user sudo yang authorized_keys-nya terbaca & berisi key valid
}

// Model adalah dashboard User & SSH.
type Model struct {
	env     shared.Env
	paths   Paths
	me      string
	home    string
	state   State
	loaded  bool
	err     error
	table   ui.Table
	message string
}

type stateMsg struct {
	owner *Model
	state State
	err   error
}

// New membuat dashboard User & SSH.
func New(env shared.Env) *Model {
	home, _ := os.UserHomeDir()
	me := os.Getenv("USER")
	if me == "" {
		me = "root"
	}
	return &Model{env: env, paths: DefaultPaths, me: me, home: home, table: ui.Table{Columns: []ui.Column{
		{Title: "User", Width: 12, Flex: 1},
		{Title: "UID", Width: 5, Right: true},
		{Title: "Admin", Width: 5},
		{Title: "Grup penting", Width: 14, Flex: 1},
		{Title: "Login terakhir", Width: 16},
		{Title: "Shell", Width: 10, Flex: 1},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "User & SSH" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("enter", "detail user"), b("n", "tambah user"), b("h", "amankan SSH"), b("k", "SSH key saya"), b("f", "login gagal")}
}

func (m *Model) HelpText() string {
	return "User di grup sudo adalah admin. Login dengan SSH key jauh lebih aman daripada password: pasang key, tes login dengan key, baru matikan login password. " +
		"ubt menolak perubahan yang bisa membuatmu terkunci (mis. mematikan password saat belum ada key). Command setara: getent passwd, groups USER, who, last, sudo sshd -T"
}

func (m *Model) Init() tea.Cmd {
	env, paths, me := m.env, m.paths, m.me
	home := m.home
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, err := Load(ctx, env, paths, me, home)
		return stateMsg{owner: m, state: st, err: err}
	}
}

// Load membaca semua data modul.
func Load(ctx context.Context, env shared.Env, paths Paths, me, home string) (State, error) {
	var st State
	var err error
	if st.Data, err = sysusers.Read(ctx, env.Runner); err != nil {
		return st, err
	}
	st.SSHD = sysusers.ReadSSHD(paths.SSHDConfig, paths.SSHDBinary)
	st.OwnKeys = sysusers.ReadOwnKeys(home)
	if data, err := os.ReadFile(paths.UFWConf); err == nil {
		st.UFWActive = netinfo.UFWEnabled(data)
	}
	if out, _, _ := env.Runner.Capture(ctx, run.Command{Argv: []string{"systemctl", "is-enabled", "ssh.socket"}}); strings.TrimSpace(out) == "enabled" {
		st.Socket = true
	}
	for _, u := range st.Data.Users {
		keys, err := sysusers.ReadAuthorizedKeys(u.Home)
		if err != nil {
			continue
		}
		valid := 0
		for _, k := range keys {
			if k.Err == nil {
				valid++
			}
		}
		if valid > 0 {
			if u.Name == me {
				st.CurrentKey = true
			}
			if u.InGroup("sudo") {
				st.KeyOwners = append(st.KeyOwners, u.Name)
			}
		}
	}
	if st.SSHD.Installed {
		res, err := syslogs.Read(ctx, env.Runner, syslogs.Query{Unit: "ssh.service", Grep: "Failed password|Failed publickey|Invalid user", Since: "7 days ago", Priority: -1, Lines: 5000})
		st.FailedErr = err
		var lines []string
		var times []time.Time
		for _, e := range res.Entries {
			lines = append(lines, e.Message)
			times = append(times, e.Time)
		}
		st.Failed = sysusers.ParseFailedLogins(lines, times)
	}
	return st, nil
}

func (m *Model) safety() sysusers.SafetyContext {
	isSudo := false
	for _, u := range m.state.Data.Users {
		if u.Name == m.me && u.InGroup("sudo") {
			isSudo = true
		}
	}
	return sysusers.SafetyContext{
		CurrentUser: m.me, CurrentHasKey: m.state.CurrentKey, SudoUsersWithKey: m.state.KeyOwners, CurrentIsSudo: isSudo,
		UFWActive: m.state.UFWActive, SocketActivated: m.state.Socket, CurrentPort: m.state.SSHD.Values["port"],
	}
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case stateMsg:
		if msg.owner == m {
			m.loaded, m.state, m.err = true, msg.state, msg.err
			m.fill()
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
			case "add-user":
				return m, nav.Push(runflow.Confirm(sysusers.AddUserPlan(strings.TrimSpace(r.Answers["name"].Value()), r.Answers["admin"].Yes()), m.env.Deps))
			case "harden":
				in := HardeningFromAnswers(r.Answers)
				if err := sysusers.Guard(in, m.safety()); err != nil {
					m.message = "✗ " + err.Error()
					return m, nil
				}
				existing := sysusers.ReadDropIn(sysusers.DropIn)
				return m, nav.Push(runflow.Confirm(sysusers.HardeningPlan(in, m.safety(), existing, m.env.Now()), m.env.Deps))
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
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "enter":
			if m.table.Cursor < len(m.state.Data.Users) {
				return m, nav.Push(newDetail(m.env, m.state.Data.Users[m.table.Cursor], m.state.Data, m.me))
			}
		case "n":
			return m, nav.Push(ask.New(addUserForm(m.state.Data)))
		case "h":
			if !m.state.SSHD.Installed {
				return m, nav.Push(runflow.Confirm(sysusers.InstallSSHPlan(), m.env.Deps))
			}
			return m, nav.Push(ask.New(HardeningForm(m.state.SSHD, m.safety())))
		case "k":
			return m, nav.Push(newOwnKeys(m.env, m.home, m.state.OwnKeys))
		case "f":
			return m, nav.Push(newFailed(m.env, m.state))
		}
	}
	return m, nil
}

func (m *Model) fill() {
	rows := make([][]string, len(m.state.Data.Users))
	now := m.env.Now()
	for i, u := range m.state.Data.Users {
		admin := ""
		if u.InGroup("sudo") || u.UID == 0 {
			admin = "ya"
		}
		var important []string
		for _, g := range []string{"docker", "adm", "www-data"} {
			if u.InGroup(g) {
				important = append(important, g)
			}
		}
		last := "belum pernah"
		if !u.LastLogin.IsZero() {
			last = shared.Ago(u.LastLogin, now)
		}
		name := u.Name
		if u.Name == m.me {
			name += " (kamu)"
		}
		rows[i] = []string{name, strconv.Itoa(u.UID), admin, strings.Join(important, ", "), last, u.Shell}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if c == 2 {
			return t.Warning
		}
		if c == 5 || c == 4 {
			return t.Subtle
		}
		return lipgloss.NewStyle()
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca akun & SSH…")
	case m.err != nil:
		return ui.ErrorState(m.err, "", width)
	}
	st := m.state
	var lines []string
	lines = append(lines, "", " "+t.Title.Render(fmt.Sprintf("Akun (%d)", len(st.Data.Users)))+"   "+t.Muted.Render("command setara: getent passwd · getent group sudo · lastlog"))
	lines = append(lines, strings.Split(m.table.View(width, len(st.Data.Users)+1), "\n")...)

	if len(st.Data.Sessions) > 0 {
		var ss []string
		for _, s := range st.Data.Sessions {
			from := s.TTY
			if s.From != "" {
				from = s.From
			}
			ss = append(ss, s.User+"@"+from)
		}
		lines = append(lines, "", " "+t.Title.Render("Sedang login")+"   "+strings.Join(ss, ", ")+"   "+t.Muted.Render("command setara: who"))
	}

	lines = append(lines, "", " "+t.Title.Render("SSH server")+"   "+t.Muted.Render("sumber: /etc/ssh/sshd_config + sshd_config.d/*.conf"))
	if !st.SSHD.Installed {
		lines = append(lines, "   "+t.Warning.Render("openssh-server belum terpasang")+t.Subtle.Render(" — server ini tidak bisa diakses lewat SSH. Tekan h untuk memasangnya."))
	} else {
		for _, f := range st.SSHD.Audit() {
			icon := t.Success.Render("✓")
			if !f.OK {
				icon = map[risk.Level]string{risk.Safe: t.Subtle.Render("•"), risk.Caution: t.Warning.Render("⚠"), risk.Dangerous: t.Danger.Render("✗")}[f.Level]
			}
			src := ""
			if f.Source != "" {
				src = t.Muted.Render("  (" + f.Source + ")")
			}
			lines = append(lines, fmt.Sprintf("   %s %-26s %s%s", icon, f.Title, t.Accent.Render(f.Value), src))
			if !f.OK {
				lines = append(lines, ui.Wrap(t.Subtle.Render(f.Advice), width, "       "))
			}
		}
		keyState := t.Warning.Render("⚠ kamu belum punya SSH key terpasang di akun ini")
		if st.CurrentKey {
			keyState = t.Success.Render("✓ akunmu sudah punya SSH key")
		}
		lines = append(lines, "   "+keyState+t.Subtle.Render(" — tekan h untuk mengamankan SSH, enter pada user untuk memasang key"))
		if len(st.Failed) > 0 {
			total := 0
			for _, f := range st.Failed {
				total += f.Count
			}
			lines = append(lines, "   "+t.Warning.Render(fmt.Sprintf("⚠ %d percobaan login gagal dari %d IP dalam 7 hari", total, len(st.Failed)))+t.Subtle.Render(" — tekan f"))
		}
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}

func addUserForm(d sysusers.Data) ask.Form {
	return ask.Form{ID: "add-user", Title: "Tambah user", Questions: []ask.Question{
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama user baru?", Placeholder: "budi",
			Help: "Huruf kecil, angka, - atau _. Diawali huruf. Contoh: budi, deploy, app_web.",
			Validate: func(s string) error {
				s = strings.TrimSpace(s)
				if !sysusers.ValidUsername(s) {
					return errors.New("nama tidak valid: huruf kecil/angka/-/_, diawali huruf, maksimal 32 karakter")
				}
				for _, u := range d.Users {
					if u.Name == s {
						return errors.New("user " + s + " sudah ada")
					}
				}
				return nil
			}},
		{ID: "admin", Header: "Admin", Kind: ask.Confirm, Prompt: "Jadikan admin (grup sudo)?", Default: []string{ask.ValueNo}, Options: []ask.Option{
			{Label: "Ya, jadikan admin", Description: "Bisa menjalankan apa pun sebagai root dengan sudo. Berikan hanya ke orang yang dipercaya.", Risk: risk.Dangerous},
			{Label: "Tidak", Description: "User biasa. Bisa ditambahkan ke grup sudo nanti.", Recommended: true},
		}},
	}}
}

// HardeningForm menanyakan perubahan keamanan SSH, menonaktifkan pilihan yang akan mengunci user.
func HardeningForm(s sysusers.SSHD, ctx sysusers.SafetyContext) ask.Form {
	var opts []ask.Option
	if s.Values["passwordauthentication"] != "no" {
		o := ask.Option{Value: "password", Label: "Matikan login dengan password", Recommended: true, Risk: risk.Dangerous,
			Description: "Hanya SSH key yang bisa login. Menghentikan serangan tebak password."}
		if err := sysusers.Guard(sysusers.HardeningInput{DisablePassword: true}, ctx); err != nil {
			o.Recommended, o.Disabled = false, strings.TrimPrefix(err.Error(), "ditolak: ")
		}
		opts = append(opts, o)
	}
	if v := s.Values["permitrootlogin"]; v != "no" {
		o := ask.Option{Value: "root", Label: "Larang root login lewat SSH", Recommended: true, Risk: risk.Caution,
			Description: "Admin login sebagai user biasa lalu memakai sudo. Saat ini: " + v + "."}
		if err := sysusers.Guard(sysusers.HardeningInput{DisableRoot: true}, ctx); err != nil {
			o.Recommended, o.Disabled = false, strings.TrimPrefix(err.Error(), "ditolak: ")
		}
		opts = append(opts, o)
	}
	if n, _ := strconv.Atoi(s.Values["maxauthtries"]); n == 0 || n > 4 {
		opts = append(opts, ask.Option{Value: "maxauth", Label: "Batasi 3 percobaan per koneksi", Description: "Memperlambat penebakan password.", Risk: risk.Safe})
	}
	opts = append(opts, ask.Option{Value: "port", Label: "Ganti port SSH", Description: "Mengurangi sampah log dari bot. Firewall dibuka lebih dulu bila ufw aktif.", Risk: risk.Caution})
	return ask.Form{ID: "harden", Title: "Amankan SSH", Questions: []ask.Question{
		{ID: "changes", Header: "Perubahan", Kind: ask.Multi, Prompt: "Perubahan apa yang ingin diterapkan?", Options: opts,
			Help: "Setelan ditulis ke " + sysusers.DropIn + " (bukan file utama), divalidasi dengan sshd -t, lalu diterapkan tanpa memutus sesi ini. Setelah itu tes login dari terminal baru sebelum menutup sesi ini."},
		{ID: "port", Header: "Port", Kind: ask.Text, Prompt: "Port SSH baru?", Placeholder: "2222",
			When: func(a ask.Answers) bool { return a["changes"].Has("port") },
			Validate: func(v string) error {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				switch {
				case err != nil || n < 1 || n > 65535:
					return errors.New("port harus angka 1-65535")
				case n < 1024 && n != 22:
					return ask.Warn("Port di bawah 1024 biasanya dipakai layanan sistem. Tekan enter lagi bila yakin.")
				}
				return nil
			}},
	}}
}

// HardeningFromAnswers mengubah jawaban form menjadi input hardening.
func HardeningFromAnswers(a ask.Answers) sysusers.HardeningInput {
	c := a["changes"]
	in := sysusers.HardeningInput{DisablePassword: c.Has("password"), DisableRoot: c.Has("root")}
	if c.Has("maxauth") {
		in.MaxAuthTries = 3
	}
	if c.Has("port") {
		in.Port = strings.TrimSpace(a["port"].Value())
	}
	return in
}
