package users

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysusers "github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// detailModel menampilkan satu user beserta SSH key dan aksinya.
type detailModel struct {
	env     shared.Env
	user    sysusers.User
	data    sysusers.Data
	me      string
	keys    []sysusers.Key
	keysErr error
	perms   []sysusers.PermProblem
	message string
	offset  int
}

func newDetail(env shared.Env, u sysusers.User, d sysusers.Data, me string) *detailModel {
	m := &detailModel{env: env, user: u, data: d, me: me}
	m.reload()
	return m
}

func (m *detailModel) reload() {
	m.keys, m.keysErr = sysusers.ReadAuthorizedKeys(m.user.Home)
	m.perms = sysusers.CheckPermissions(m.user.Home, m.user.UID)
}

func (m *detailModel) self() bool { return m.user.Name == m.me }

func (m *detailModel) Title() string { return m.user.Name }
func (m *detailModel) Init() tea.Cmd { return nil }
func (m *detailModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "aksi")), key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "gulir"))}
}

func (m *detailModel) ctx() sysusers.Context {
	return sysusers.Context{CurrentUser: m.me, IsRoot: m.env.IsRoot}
}

// ActionForm menawarkan aksi untuk user, dengan pilihan berbahaya dinonaktifkan oleh pengaman.
func (m *detailModel) ActionForm() ask.Form {
	u := m.user
	guard := func(action string) string {
		if err := sysusers.GuardUserChange(action, u, m.data, m.ctx()); err != nil {
			return strings.TrimPrefix(err.Error(), "ditolak: ")
		}
		return ""
	}
	var opts []ask.Option
	opts = append(opts, ask.Option{Value: "add-key", Label: "Tambah SSH key", Recommended: len(m.keys) == 0,
		Description: "Paste public key (isi file .pub dari komputer yang akan login)."})
	if len(m.keys) > 0 {
		opts = append(opts, ask.Option{Value: "remove-key", Label: "Hapus SSH key", Risk: risk.Dangerous, Description: "Cabut akses satu key (mis. laptop hilang atau orangnya sudah tidak bekerja lagi)."})
	}
	if len(m.perms) > 0 {
		opts = append(opts, ask.Option{Value: "fix-perms", Label: "Perbaiki izin ~/.ssh", Recommended: true,
			Description: "Penyebab paling umum \"key sudah dipasang tapi tetap minta password\"."})
	}
	for _, g := range []string{"sudo", "docker", "adm"} {
		if _, exists := m.data.Groups[g]; !exists {
			continue
		}
		if u.InGroup(g) {
			o := ask.Option{Value: "remove-" + g, Label: "Keluarkan dari grup " + g, Risk: risk.Caution}
			if g == "sudo" {
				o.Disabled = guard("remove-sudo")
				o.Risk = risk.Dangerous
			}
			opts = append(opts, o)
		} else if u.UID != 0 {
			lvl := risk.Caution
			if g != "adm" {
				lvl = risk.Dangerous
			}
			opts = append(opts, ask.Option{Value: "add-" + g, Label: "Tambahkan ke grup " + g, Risk: lvl, Description: groupHint(g)})
		}
	}
	if u.UID != 0 {
		opts = append(opts,
			ask.Option{Value: "lock", Label: "Kunci login password", Risk: risk.Caution, Disabled: guard("lock"), Description: "Login dengan SSH key tetap bisa."},
			ask.Option{Value: "unlock", Label: "Buka kunci password", Risk: risk.Caution},
			ask.Option{Value: "expire", Label: "Nonaktifkan akun sepenuhnya", Risk: risk.Dangerous, Disabled: guard("expire"), Description: "Semua cara login ditolak; data tetap ada."},
			ask.Option{Value: "delete", Label: "Hapus user & folder home", Risk: risk.Dangerous, Disabled: guard("delete")},
		)
	}
	return ask.Form{ID: "user-action", Title: "Aksi " + u.Name, Questions: []ask.Question{{ID: "aksi", Prompt: "Apa yang ingin dilakukan dengan " + u.Name + "?", Kind: ask.Single, Options: opts}}}
}

func groupHint(g string) string {
	return map[string]string{
		"sudo":   "Jadi admin: bisa menjalankan apa pun sebagai root.",
		"docker": "Bisa memakai docker tanpa sudo. Setara root.",
		"adm":    "Bisa membaca log sistem.",
	}[g]
}

func (m *detailModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "a", "enter":
			return m, nav.Push(ask.New(m.ActionForm()))
		case "up", "k":
			m.offset = max(m.offset-1, 0)
		case "down", "j":
			m.offset++
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			return m, m.onAnswer(r)
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " selesai."
				} else {
					m.message = "✗ " + r.Plan.Title + " tidak selesai."
				}
			}
			m.reload()
		}
	}
	return m, nil
}

func (m *detailModel) onAnswer(r ask.Result) tea.Cmd {
	u := m.user
	confirm := func(p run.Plan) tea.Cmd { return nav.Push(runflow.Confirm(p, m.env.Deps)) }
	switch r.ID {
	case "add-key":
		k, err := sysusers.ParseKey(strings.TrimSpace(r.Answers["key"].Value()))
		if err != nil {
			m.message = "✗ " + err.Error()
			return nil
		}
		return confirm(sysusers.AddKeyPlan(u.Name, u.Home, k, m.self()))
	case "remove-key":
		idx, _ := strconv.Atoi(r.Answers["key"].Value())
		return confirm(sysusers.RemoveKeyPlan(u.Name, u.Home, m.keys, idx, m.self(), m.env.Now()))
	}
	switch a := r.Answers["aksi"].Value(); {
	case a == "add-key":
		return nav.Push(ask.New(ask.Form{ID: "add-key", Title: "Tambah SSH key", Questions: []ask.Question{{
			ID: "key", Kind: ask.TextArea, Prompt: "Paste public key untuk " + u.Name,
			Placeholder: "ssh-ed25519 AAAA… nama@laptop",
			Help:        "Di komputer yang akan login, tampilkan dengan: cat ~/.ssh/id_ed25519.pub. JANGAN paste private key (file tanpa .pub).",
			Validate:    keyValidator(m),
		}}}))
	case a == "remove-key":
		var opts []ask.Option
		for i, k := range m.keys {
			label := k.Comment
			if label == "" {
				label = "(tanpa komentar)"
			}
			if k.Err != nil {
				label = "(baris rusak)"
			}
			opts = append(opts, ask.Option{Value: strconv.Itoa(i), Label: label, Description: k.Type + " " + k.Fingerprint})
		}
		return nav.Push(ask.New(ask.Form{ID: "remove-key", Title: "Hapus SSH key", Questions: []ask.Question{{ID: "key", Kind: ask.Single, Prompt: "Key mana yang dihapus?", Options: opts}}}))
	case a == "fix-perms":
		return confirm(sysusers.FixPermissionsPlan(u.Name, u.Home, m.self()))
	case strings.HasPrefix(a, "add-"):
		return confirm(sysusers.GroupPlan(u.Name, strings.TrimPrefix(a, "add-"), true))
	case strings.HasPrefix(a, "remove-"):
		return confirm(sysusers.GroupPlan(u.Name, strings.TrimPrefix(a, "remove-"), false))
	case a == "lock":
		return confirm(sysusers.LockPlan(u.Name, true))
	case a == "unlock":
		return confirm(sysusers.LockPlan(u.Name, false))
	case a == "expire":
		return confirm(sysusers.ExpirePlan(u.Name))
	case a == "delete":
		return confirm(sysusers.DeleteUserPlan(u.Name))
	}
	return nil
}

// keyValidator memeriksa public key yang di-paste: menolak private key, isi rusak, dan duplikat.
func keyValidator(m *detailModel) func(string) error {
	return func(s string) error {
		if strings.Contains(s, "PRIVATE KEY") {
			return errors.New("ini PRIVATE key! Jangan pernah dibagikan. Pakai isi file .pub")
		}
		k, err := sysusers.ParseKey(strings.TrimSpace(s))
		if err != nil {
			return err
		}
		for _, existing := range m.keys {
			if existing.Fingerprint == k.Fingerprint {
				return errors.New("key ini sudah terpasang")
			}
		}
		return nil
	}
}

func (m *detailModel) View(width, height int) string {
	t := ui.Current
	u := m.user
	last := "belum pernah / tidak diketahui"
	if !u.LastLogin.IsZero() {
		last = u.LastLogin.Format("2006-01-02 15:04") + " (" + shared.Ago(u.LastLogin, m.env.Now()) + ")"
	}
	lines := []string{"", ui.Detail([]ui.Pair{
		{Key: "User", Value: u.Name},
		{Key: "UID", Value: strconv.Itoa(u.UID)},
		{Key: "Home", Value: u.Home},
		{Key: "Shell", Value: u.Shell},
		{Key: "Grup", Value: strings.Join(u.Groups, ", "), Hint: "sudo = admin, docker = setara root, adm = baca log"},
		{Key: "Login terakhir", Value: last},
	}, width), "", ui.Section("SSH key yang boleh login (~/.ssh/authorized_keys)")}
	switch {
	case m.keysErr != nil:
		lines = append(lines, "   "+t.Muted.Render("tidak terbaca tanpa sudo ("+m.keysErr.Error()+")"))
	case len(m.keys) == 0:
		lines = append(lines, "   "+t.Subtle.Render("belum ada — user ini hanya bisa login dengan password"))
	}
	for _, k := range m.keys {
		if k.Err != nil {
			lines = append(lines, "   "+t.Danger.Render("✗ baris rusak: "+k.Err.Error()))
			continue
		}
		opt := ""
		if k.Options != "" {
			opt = t.Warning.Render("  [" + k.Options + "]")
		}
		lines = append(lines, fmt.Sprintf("   %s %s %s%s", t.Success.Render("🔑"), k.Comment, t.Muted.Render(k.Type+" "+k.Fingerprint), opt))
	}
	if len(m.perms) > 0 {
		lines = append(lines, "", ui.Section("Masalah izin")+"   "+t.Subtle.Render("sshd menolak key bila izin terlalu longgar"))
		for _, p := range m.perms {
			lines = append(lines, "   "+t.Warning.Render("⚠ "+p.Detail))
		}
	}
	lines = append(lines, "", ui.Section("Cara cek sendiri"))
	for _, c := range []string{"id " + u.Name, "sudo cat " + u.Home + "/.ssh/authorized_keys", "ssh-keygen -lf " + u.Home + "/.ssh/authorized_keys", "sudo chage -l " + u.Name} {
		lines = append(lines, "   "+t.Muted.Render("$ ")+t.Code.Render(" "+c+" "))
	}
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	lines = append(lines, "", " "+t.Accent.Render("Tekan a untuk memasang key, mengatur grup, atau mengunci akun."))
	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}

// ownKeysModel menampilkan SSH key milik user saat ini.
type ownKeysModel struct {
	env     shared.Env
	home    string
	keys    []sysusers.OwnKey
	message string
}

func newOwnKeys(env shared.Env, home string, keys []sysusers.OwnKey) *ownKeysModel {
	return &ownKeysModel{env: env, home: home, keys: keys}
}

func (m *ownKeysModel) Title() string { return "SSH key saya" }
func (m *ownKeysModel) Init() tea.Cmd { return nil }
func (m *ownKeysModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("g", "buat key baru"), b("c", "pasang ke server lain")}
}

func (m *ownKeysModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "g":
			return m, nav.Push(ask.New(ask.Form{ID: "gen", Title: "Buat SSH key", Questions: []ask.Question{{
				ID: "email", Kind: ask.Text, Prompt: "Komentar untuk key ini (biasanya email)?", Placeholder: "nama@contoh.com",
				Validate: func(s string) error {
					if strings.TrimSpace(s) == "" {
						return errors.New("isi komentar supaya key mudah dikenali")
					}
					return nil
				},
			}}}))
		case "c":
			if len(m.keys) == 0 {
				m.message = "Buat key dulu (tekan g)."
				return m, nil
			}
			return m, nav.Push(ask.New(ask.Form{ID: "copy", Title: "Pasang key ke server lain", Questions: []ask.Question{{
				ID: "target", Kind: ask.Text, Prompt: "Server tujuan (user@host)?", Placeholder: "deploy@203.0.113.10",
				Validate: func(s string) error {
					if !strings.Contains(s, "@") || strings.ContainsAny(s, " ;") {
						return errors.New("format user@host")
					}
					return nil
				},
			}}}))
		}
	case nav.ResumedMsg:
		switch r := msg.Result.(type) {
		case ask.Result:
			if r.Cancelled {
				return m, nil
			}
			switch r.ID {
			case "gen":
				return m, nav.Push(runflow.Confirm(sysusers.GenerateKeyPlan(m.home, strings.TrimSpace(r.Answers["email"].Value())), m.env.Deps))
			case "copy":
				return m, nav.Push(runflow.Confirm(sysusers.CopyIDPlan(m.keys[0].Private+".pub", strings.TrimSpace(r.Answers["target"].Value())), m.env.Deps))
			}
		case run.Outcome:
			m.keys = sysusers.ReadOwnKeys(m.home)
		}
	}
	return m, nil
}

func (m *ownKeysModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", ui.Wrap(t.Subtle.Render("Key di bawah dipakai server ini untuk login ke server LAIN (mis. git, server produksi). Bagikan hanya isi file .pub."), width, " "), ""}
	if len(m.keys) == 0 {
		lines = append(lines, "   "+t.Muted.Render("Belum ada key di ~/.ssh. Tekan g untuk membuat."))
	}
	for _, k := range m.keys {
		lines = append(lines, " "+t.Title.Render(k.Private)+"  "+t.Muted.Render(k.Public.Type+" "+k.Public.Fingerprint))
		lines = append(lines, ui.Wrap(t.Code.Render(k.Public.Line), width, "   "), "")
	}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}

// failedModel menampilkan login gagal per IP.
type failedModel struct {
	env   shared.Env
	state State
}

func newFailed(env shared.Env, st State) *failedModel { return &failedModel{env: env, state: st} }

func (m *failedModel) Title() string { return "Login SSH gagal (7 hari)" }
func (m *failedModel) Init() tea.Cmd { return nil }
func (m *failedModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "pasang fail2ban"))}
}

func (m *failedModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "i" {
		return m, nav.Push(runflow.Confirm(run.Single(run.Command{
			Title: "Pasang fail2ban", Argv: []string{"apt-get", "install", "-y", "fail2ban"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "fail2ban", Meaning: "blokir sementara IP yang berulang kali gagal login (jail sshd aktif secara bawaan di Ubuntu)"}},
			Effect:  "IP yang gagal login 5× dalam 10 menit diblok 10 menit. Hati-hati: kamu sendiri bisa terblok bila salah password berkali-kali.",
			Risk:    risk.Caution,
		}), m.env.Deps))
	}
	return m, nil
}

func (m *failedModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Muted.Render("command setara: journalctl -u ssh --since '7 days ago' --grep 'Failed password|Invalid user'"), ""}
	switch {
	case !m.state.SSHD.Installed:
		lines = append(lines, "   "+t.Muted.Render("SSH server belum terpasang."))
	case m.state.FailedErr != nil:
		lines = append(lines, "   "+t.Muted.Render("log tidak terbaca: "+m.state.FailedErr.Error()))
	case len(m.state.Failed) == 0:
		lines = append(lines, "   "+t.Success.Render("✓ tidak ada login gagal dalam 7 hari"))
	}
	for i, f := range m.state.Failed {
		if i == 30 {
			break
		}
		lines = append(lines, fmt.Sprintf("   %-40s %5d×  %s  %s", f.IP, f.Count, t.Subtle.Render(strings.Join(f.Users, ", ")), t.Muted.Render(shared.Ago(f.LastSeen, m.env.Now()))))
	}
	if len(m.state.Failed) > 0 {
		lines = append(lines, "", ui.Wrap(t.Subtle.Render("Ribuan percobaan dari internet itu normal untuk server dengan port 22 terbuka (bot). Yang penting: matikan login password (h di layar sebelumnya) dan pertimbangkan fail2ban (tekan i)."), width, " "))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}
