// Package firewall adalah layar modul Firewall (ufw): status, aturan bernomor, wizard buka/tutup port,
// dan pengaman supaya sesi SSH tidak terputus.
package firewall

import (
	"context"
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
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysfw "github.com/arif-rachim/ubuntu-tool/internal/sys/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	sysusers "github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah layar firewall.
type Model struct {
	env     shared.Env
	paths   sysfw.Paths
	sshd    string
	status  sysfw.Status
	ssh     sysfw.SSHContext
	loaded  bool
	table   ui.Table
	message string
}

type statusMsg struct {
	owner  *Model
	status sysfw.Status
	ssh    sysfw.SSHContext
}

// New membuat layar firewall.
func New(env shared.Env) *Model {
	return &Model{env: env, paths: sysfw.DefaultPaths, sshd: "/etc/ssh/sshd_config", table: ui.Table{Columns: []ui.Column{
		{Title: "#", Width: 3, Right: true},
		{Title: "Tujuan", Width: 14, Flex: 2},
		{Title: "Aksi", Width: 10},
		{Title: "Dari", Width: 14, Flex: 2},
		{Title: "Catatan", Width: 10, Flex: 2},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Firewall (ufw)" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	switch {
	case !m.status.Installed:
		return []key.Binding{b("i", "install ufw")}
	case !m.status.Readable:
		return []key.Binding{b("s", "baca dengan sudo")}
	}
	toggle := "aktifkan"
	if m.status.Active {
		toggle = "matikan"
	}
	return []key.Binding{b("a", "tambah aturan"), b("d", "hapus aturan"), b("e", toggle), b("x", "reset")}
}

func (m *Model) HelpText() string {
	return "ufw memutuskan koneksi masuk mana yang boleh. Setelan umum server: tolak semua koneksi masuk (default deny) lalu izinkan hanya yang dibutuhkan (SSH, web). " +
		"\"limit\" mengizinkan tetapi memblok sementara IP yang mencoba terlalu sering — cocok untuk SSH. Aturan dicek dari atas; nomor bisa bergeser setelah aturan dihapus. " +
		"Command setara: sudo ufw status numbered, sudo ufw status verbose"
}

func (m *Model) Init() tea.Cmd {
	r, paths, sshdConf, root := m.env.Runner, m.paths, m.sshd, m.env.ProcRoot
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		st := sysfw.Read(ctx, r, paths)
		return statusMsg{owner: m, status: st, ssh: sshContext(sshdConf, root)}
	}
}

func sshContext(sshdConf, procRoot string) sysfw.SSHContext {
	var c sysfw.SSHContext
	cfg := sysusers.ReadSSHD(sshdConf, "/usr/sbin/sshd")
	c.Port, _ = strconv.Atoi(cfg.Values["port"])
	if p := procact.SSHPortFromEnv(); p != 0 {
		c.InSession = true
		c.Port = p
		c.SessionIP = strings.Fields(os.Getenv("SSH_CONNECTION"))[0]
	}
	if snap, err := ports.ReadListeners(procRoot); err == nil {
		for _, l := range snap.Listeners {
			if int(l.Local.Port()) == c.Port && l.Proto == ports.TCP {
				c.SSHRunning = true
			}
		}
	}
	if !cfg.Installed && !c.InSession && !c.SSHRunning {
		c.Port = 0
	}
	return c
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		if msg.owner == m {
			m.loaded, m.status, m.ssh = true, msg.status, msg.ssh
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
			action, target, comment := RuleFromAnswers(r.Answers)
			return m, nav.Push(runflow.Confirm(sysfw.RulePlan(action, target, comment), m.env.Deps))
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " selesai."
				} else {
					m.message = "✗ " + r.Plan.Title + " gagal."
				}
			}
		}
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.status.Readable && m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		st := m.status
		confirm := func(p run.Plan) (nav.Screen, tea.Cmd) { return m, nav.Push(runflow.Confirm(p, m.env.Deps)) }
		switch msg.String() {
		case "i":
			if !st.Installed {
				return confirm(sysfw.InstallPlan())
			}
		case "s":
			if st.Installed && !st.Readable {
				return confirm(run.Single(run.Command{Title: "Baca status firewall", Argv: []string{"ufw", "status", "verbose"}, NeedsRoot: true,
					Explain: []run.Line{{Token: "ufw status verbose", Meaning: "tampilkan aturan & kebijakan default (butuh root)"}},
					Effect:  "Hanya membaca. Setelah sudo diizinkan, ubt bisa menampilkan aturan dalam tabel selama beberapa menit."}))
			}
		case "a":
			if st.Readable {
				return m, nav.Push(ask.New(AddRuleForm(st, m.ssh)))
			}
		case "d":
			if st.Readable && m.table.Cursor < len(st.Rules) {
				r := st.Rules[m.table.Cursor]
				return confirm(sysfw.DeletePlan(r, sysfw.Lockout(st, m.ssh, &r)))
			}
		case "e":
			if st.Readable {
				if st.Active {
					return confirm(sysfw.DisablePlan())
				}
				return confirm(sysfw.EnablePlan(st, m.ssh))
			}
		case "x":
			if st.Readable {
				return confirm(sysfw.ResetPlan())
			}
		}
	}
	return m, nil
}

func (m *Model) fill() {
	rows := make([][]string, len(m.status.Rules))
	for i, r := range m.status.Rules {
		to := r.To
		if r.V6 {
			to += " (IPv6)"
		}
		action := r.Action
		if r.Dir != "IN" {
			action += " " + r.Dir
		}
		from := r.From
		if from == "Anywhere" {
			from = "mana saja"
		}
		rows[i] = []string{strconv.Itoa(r.Num), to, action, from, r.Comment}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if r >= len(m.status.Rules) || c != 2 {
			return lipgloss.NewStyle()
		}
		switch m.status.Rules[r].Action {
		case "ALLOW":
			return t.Success
		case "LIMIT":
			return t.Accent
		}
		return t.Danger
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	st := m.status
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca firewall…")
	case !st.Installed:
		return ui.EmptyState("ufw belum terpasang", "Tanpa firewall, semua port yang listening bisa diakses dari luar server (kecuali diblok firewall penyedia cloud).", "tekan i untuk memasang ufw", width)
	}
	var lines []string
	state := t.Warning.Render("● tidak aktif")
	if st.Active {
		state = t.Success.Render("● aktif")
	}
	lines = append(lines, "", " "+t.Title.Render("Firewall ufw")+"  "+state+"   "+t.Muted.Render("command setara: sudo ufw status numbered"))
	if !st.Readable {
		lines = append(lines, "", ui.Wrap(t.Subtle.Render("Aturan firewall hanya bisa dibaca root. Tekan s untuk memasukkan password sudo sekali, lalu aturan tampil di sini."), width, " "))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}
	if st.Defaults != "" {
		lines = append(lines, "   "+t.Subtle.Render("kebijakan default: "+st.Defaults+" · logging: "+st.Logging))
	}
	if m.ssh.Port != 0 {
		if warn := sysfw.Lockout(st, m.ssh, nil); warn != "" {
			prefix := "⚠ "
			if st.Active {
				prefix = "✗ "
			}
			lines = append(lines, ui.Wrap(t.Danger.Render(prefix+"SSH: "+warn), width, "   "))
			if !st.Active {
				lines = append(lines, "   "+t.Subtle.Render("ubt otomatis menambahkan aturan SSH saat kamu mengaktifkan firewall (e)."))
			}
		} else {
			lines = append(lines, "   "+t.Success.Render(fmt.Sprintf("✓ port SSH %d diizinkan", m.ssh.Port)))
		}
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "")
	top := strings.Join(lines, "\n")
	if len(st.Rules) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada aturan", "Bila ufw aktif tanpa aturan, semua koneksi masuk ditolak.", "tekan a untuk menambah aturan", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// AddRuleForm adalah wizard membuka/menolak port.
func AddRuleForm(st sysfw.Status, ssh sysfw.SSHContext) ask.Form {
	var targets []ask.Option
	sshPort := "22"
	if ssh.Port != 0 {
		sshPort = strconv.Itoa(ssh.Port)
	}
	targets = append(targets,
		ask.Option{Value: "port:" + sshPort + "/tcp", Label: "SSH (" + sshPort + "/tcp)", Description: "Supaya bisa login ke server dari jauh."},
		ask.Option{Value: "port:80,443/tcp", Label: "Web (80 & 443)", Description: "Website HTTP dan HTTPS."},
	)
	for _, app := range st.Apps {
		targets = append(targets, ask.Option{Value: "app:" + app, Label: "Aplikasi: " + app, Description: "Profil ufw bawaan paket (lihat: ufw app info \"" + app + "\")."})
	}
	targets = append(targets, ask.Option{Value: "custom", Label: "Port lain…", Description: "Isi nomor port atau rentang sendiri."})
	isCustom := func(a ask.Answers) bool { return a["target"].Value() == "custom" }
	return ask.Form{ID: "rule", Title: "Tambah aturan firewall", Questions: []ask.Question{
		{ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan?", Options: []ask.Option{
			{Value: "allow", Label: "Izinkan (buka port)", Recommended: true, Description: "Koneksi masuk ke port ini diterima."},
			{Value: "limit", Label: "Izinkan dengan pembatasan", Description: "Seperti izinkan, tetapi IP yang membuka ≥6 koneksi/30 detik diblok sementara. Cocok untuk SSH."},
			{Value: "deny", Label: "Tolak", Risk: risk.Caution, Description: "Koneksi masuk ke port ini dibuang."},
		}},
		{ID: "target", Header: "Tujuan", Kind: ask.Single, Prompt: "Untuk port/aplikasi apa?", Options: targets},
		{ID: "port", Header: "Port", Kind: ask.Text, Prompt: "Nomor port atau rentang?", Placeholder: "8080 atau 6000:6007", When: isCustom, Validate: sysfw.ValidatePort},
		{ID: "proto", Header: "Protokol", Kind: ask.Single, Prompt: "Protokol?", When: isCustom, Options: []ask.Option{
			{Value: "tcp", Label: "TCP", Recommended: true, Description: "Web, SSH, database, dan hampir semua aplikasi."},
			{Value: "udp", Label: "UDP", Description: "DNS, VPN (WireGuard/OpenVPN), game, streaming."},
			{Value: "", Label: "Keduanya"},
		}},
		{ID: "from", Header: "Dari", Kind: ask.Single, Prompt: "Dari mana koneksi boleh datang?", Other: true, Validate: sysfw.ValidateSource, Options: []ask.Option{
			{Value: "", Label: "Mana saja (internet)", Recommended: true, Description: "Untuk layanan publik seperti web."},
			{Value: "10.0.0.0/8", Label: "Jaringan privat 10.0.0.0/8", Description: "Contoh untuk database yang hanya boleh diakses server lain di jaringan internal."},
		}},
		{ID: "comment", Header: "Catatan", Kind: ask.Text, Prompt: "Catatan untuk aturan ini? (opsional)", Placeholder: "api node", Optional: true},
	}}
}

// RuleFromAnswers membaca jawaban wizard.
func RuleFromAnswers(a ask.Answers) (action string, t sysfw.Target, comment string) {
	action = a["action"].Value()
	switch v := a["target"].Value(); {
	case strings.HasPrefix(v, "app:"):
		t.App = strings.TrimPrefix(v, "app:")
	case strings.HasPrefix(v, "port:"):
		spec := strings.TrimPrefix(v, "port:")
		t.Port, t.Proto, _ = strings.Cut(spec, "/")
	default:
		t.Port = strings.TrimSpace(a["port"].Value())
		if len(a["proto"].Values) > 0 {
			t.Proto = a["proto"].Values[0]
		}
	}
	if from := a["from"]; from.Other != "" {
		t.From = strings.TrimSpace(from.Other)
	} else if len(from.Values) > 0 {
		t.From = from.Values[0]
	}
	return action, t, strings.TrimSpace(a["comment"].Value())
}
