// Package web adalah layar modul Web & TLS: site nginx, wizard reverse proxy, HTTPS Let's Encrypt,
// dan pemeriksaan sertifikat.
package web

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
	"github.com/arif-rachim/ubuntu-tool/internal/sys/netinfo"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/tlscheck"
	sysweb "github.com/arif-rachim/ubuntu-tool/internal/sys/web"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Model adalah dashboard Web & TLS.
type Model struct {
	env     shared.Env
	paths   sysweb.Paths
	state   sysweb.State
	loaded  bool
	table   ui.Table
	message string
	ufwConf string
}

type stateMsg struct {
	owner *Model
	state sysweb.State
}

// New membuat dashboard Web & TLS.
func New(env shared.Env) *Model {
	return &Model{env: env, paths: sysweb.DefaultPaths, ufwConf: "/etc/ufw/ufw.conf", table: ui.Table{Columns: []ui.Column{
		{Title: "Site", Width: 14, Flex: 1},
		{Title: "Aktif", Width: 5},
		{Title: "Domain", Width: 16, Flex: 2},
		{Title: "Diteruskan ke", Width: 16, Flex: 2},
		{Title: "HTTPS", Width: 5},
	}}}
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Web & TLS" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	if !m.state.Nginx().Installed {
		return []key.Binding{b("i", "install nginx"), b("c", "cek sertifikat domain")}
	}
	return []key.Binding{b("p", "reverse proxy baru"), b("s", "pasang HTTPS"), b("c", "cek sertifikat"), b("t", "uji konfigurasi"), b("enter", "detail site")}
}

func (m *Model) HelpText() string {
	return "Reverse proxy: nginx menerima pengunjung di port 80/443 lalu meneruskan ke aplikasimu yang berjalan di localhost (mis. port 3000). " +
		"Dengan begitu aplikasi tidak perlu dibuka langsung ke internet, dan HTTPS cukup dipasang di nginx. " +
		"HTTPS gratis dari Let's Encrypt lewat certbot; syaratnya DNS domain sudah mengarah ke IP server ini dan port 80 terbuka. " +
		"Command setara: ls /etc/nginx/sites-enabled, sudo nginx -t, sudo certbot certificates"
}

func (m *Model) Init() tea.Cmd {
	r, p := m.env.Runner, m.paths
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return stateMsg{owner: m, state: sysweb.Read(ctx, r, p)}
	}
}

func (m *Model) ufwActive() bool {
	data, err := os.ReadFile(m.ufwConf)
	return err == nil && netinfo.UFWEnabled(data)
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	confirm := func(p run.Plan) (nav.Screen, tea.Cmd) { return m, nav.Push(runflow.Confirm(p, m.env.Deps)) }
	switch msg := msg.(type) {
	case stateMsg:
		if msg.owner == m {
			m.loaded, m.state = true, msg.state
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
			case "proxy":
				spec := ProxyFromAnswers(r.Answers)
				return confirm(sysweb.ProxyPlan(spec, m.paths, m.ufwActive()))
			case "https":
				domain := domainAnswer(r.Answers["domain"])
				return confirm(HTTPSPlan(m.state.Certbot, domain, r.Answers["www"].Yes()))
			case "cert":
				return m, nav.Push(newCertCheck(m.env, strings.TrimSpace(r.Answers["domain"].Value())))
			}
		case run.Outcome:
			if r.Approved {
				if r.OK() {
					m.message = "✓ " + r.Plan.Title + " selesai."
				} else {
					m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
				}
			}
		}
		return m, m.Init()
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		st := m.state
		switch msg.String() {
		case "i":
			if !st.Nginx().Installed {
				return confirm(sysweb.InstallNginxPlan())
			}
		case "p":
			if !st.Nginx().Installed {
				m.message = "Pasang nginx dulu (tekan i)."
				return m, nil
			}
			return m, nav.Push(ask.New(ProxyForm(m.env.ProcRoot, st.Sites)))
		case "s":
			switch {
			case !st.Nginx().Installed:
				m.message = "Pasang nginx dulu (tekan i)."
			case st.Certbot == "":
				return confirm(sysweb.InstallCertbotPlan())
			default:
				return m, nav.Push(ask.New(HTTPSForm(st.Sites)))
			}
		case "c":
			return m, nav.Push(ask.New(ask.Form{ID: "cert", Title: "Cek sertifikat", Questions: []ask.Question{{
				ID: "domain", Kind: ask.Text, Prompt: "Domain yang diperiksa?", Placeholder: "contoh.com",
				Help:     "Bisa domain di server ini maupun di server lain. ubt terhubung ke port 443 dan memeriksa sertifikatnya.",
				Validate: sysweb.ValidateDomain,
			}}}))
		case "t":
			if st.Nginx().Installed {
				return confirm(sysweb.TestConfigPlan())
			}
		case "enter":
			if m.table.Cursor < len(st.Sites) {
				return m, nav.Push(newSite(m.env, m.paths, st.Sites[m.table.Cursor], st.Certbot))
			}
		}
	}
	return m, nil
}

func (m *Model) fill() {
	rows := make([][]string, len(m.state.Sites))
	for i, s := range m.state.Sites {
		active := ""
		if s.Enabled {
			active = "ya"
		}
		target := strings.Join(s.ProxyPass, ", ")
		if target == "" && s.Root != "" {
			target = "file: " + s.Root
		}
		https := ""
		if s.SSL {
			https = "ya"
		}
		rows[i] = []string{s.Name, active, strings.Join(s.ServerNames, " "), target, https}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = func(r, c int) lipgloss.Style {
		t := ui.Current
		if r >= len(m.state.Sites) {
			return lipgloss.NewStyle()
		}
		s := m.state.Sites[r]
		switch {
		case !s.Enabled:
			return t.Muted
		case c == 4 && s.SSL:
			return t.Success
		}
		return lipgloss.NewStyle()
	}
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	if !m.loaded {
		return "\n " + t.Subtle.Render("Mendeteksi web server…")
	}
	st := m.state
	var lines []string
	lines = append(lines, "", " "+t.Title.Render("Web server"))
	active := 0
	for _, s := range st.Servers {
		switch {
		case !s.Installed:
			lines = append(lines, fmt.Sprintf("   %-8s %s", s.Name, t.Muted.Render("tidak terpasang")))
		case s.Active:
			active++
			lines = append(lines, fmt.Sprintf("   %-8s %s", s.Name, t.Success.Render("● berjalan")))
		default:
			lines = append(lines, fmt.Sprintf("   %-8s %s", s.Name, t.Warning.Render("● terpasang tetapi berhenti")))
		}
	}
	if active > 1 {
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ Lebih dari satu web server berjalan: mereka berebut port 80/443 dan salah satunya gagal start. Hentikan yang tidak dipakai di modul Service."), width, "   "))
	}
	certbot := t.Muted.Render("certbot belum terpasang (tekan s untuk memasang)")
	if st.Certbot != "" {
		certbot = t.Success.Render("✓ certbot terpasang") + t.Subtle.Render(" ("+st.Certbot+")")
	}
	lines = append(lines, "   "+certbot)
	if m.message != "" {
		lines = append(lines, "", ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	if !st.Nginx().Installed {
		lines = append(lines, "", ui.Wrap(t.Subtle.Render("nginx belum terpasang. Tekan i untuk memasang, lalu p untuk membuat reverse proxy ke aplikasimu. Tanpa web server pun, kamu tetap bisa memeriksa sertifikat domain mana saja (c)."), width, " "))
		return ui.FitHeight(strings.Join(lines, "\n"), width, height)
	}
	lines = append(lines, "", " "+t.Title.Render(fmt.Sprintf("Site nginx (%d)", len(st.Sites)))+"   "+t.Muted.Render("command setara: ls -l /etc/nginx/sites-enabled"))
	top := strings.Join(lines, "\n")
	if len(st.Sites) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada site", "", "tekan p untuk membuat reverse proxy", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// ProxyForm adalah wizard reverse proxy. Pilihan port diisi dari aplikasi yang sedang listening.
func ProxyForm(procRoot string, sites []sysweb.Site) ask.Form {
	return ask.Form{ID: "proxy", Title: "Reverse proxy baru", Questions: []ask.Question{
		{ID: "domain", Header: "Domain", Kind: ask.Text, Prompt: "Domain untuk aplikasi ini?", Placeholder: "app.contoh.com",
			Help: "Domain yang diketik pengunjung. Buat record DNS A yang mengarah ke IP server ini di penyedia domainmu.",
			Validate: func(s string) error {
				if err := sysweb.ValidateDomain(s); err != nil {
					return err
				}
				for _, site := range sites {
					for _, n := range site.ServerNames {
						if n == strings.ToLower(strings.TrimSpace(s)) {
							return errors.New("domain ini sudah dipakai site " + site.Name)
						}
					}
				}
				return nil
			}},
		{ID: "port", Header: "Port", Kind: ask.Single, Prompt: "Aplikasimu berjalan di port berapa?", Other: true, Load: listeningOptions(procRoot),
			Help: "Port aplikasi di server ini. Aplikasi sebaiknya bind ke 127.0.0.1 supaya hanya bisa diakses lewat nginx.",
			Validate: func(s string) error {
				n, err := strconv.Atoi(strings.TrimSpace(s))
				if err != nil || n < 1 || n > 65535 {
					return errors.New("port harus angka 1-65535")
				}
				return nil
			}},
		{ID: "ws", Header: "WebSocket", Kind: ask.Confirm, Prompt: "Aktifkan dukungan WebSocket?", Default: []string{ask.ValueNo}, Options: []ask.Option{
			{Label: "Ya", Description: "Perlu untuk socket.io, live update, chat, dan aplikasi real-time."},
			{Label: "Tidak", Description: "Cukup untuk website dan REST API biasa.", Recommended: true},
		}},
	}}
}

// listeningOptions menawarkan port TCP yang sedang listening sebagai pilihan.
func listeningOptions(procRoot string) func(context.Context) ([]ask.Option, error) {
	return func(context.Context) ([]ask.Option, error) {
		snap, err := ports.ReadListeners(procRoot)
		if err != nil {
			return nil, err
		}
		seen := map[uint16]bool{}
		var opts []ask.Option
		for _, l := range snap.Listeners {
			p := l.Local.Port()
			if l.Proto != ports.TCP || seen[p] || p == 80 || p == 443 || p == 22 || p < 1024 && p != 8 {
				continue
			}
			seen[p] = true
			desc := "dengarkan di " + l.Local.Addr().String()
			if l.Scope() == ports.ScopeLoopback {
				desc += " (hanya localhost — cocok di belakang nginx)"
			}
			name := "?"
			if len(l.PIDs) > 0 {
				if pr, err := procs.Read(procRoot, l.PIDs[0]); err == nil {
					name = pr.Name
				}
			}
			opts = append(opts, ask.Option{Value: strconv.Itoa(int(p)), Label: strconv.Itoa(int(p)), Meta: name, Description: desc, Recommended: len(opts) == 0 && l.Scope() == ports.ScopeLoopback})
		}
		return opts, nil
	}
}

// ProxyFromAnswers membaca jawaban wizard reverse proxy.
func ProxyFromAnswers(a ask.Answers) sysweb.ProxySpec {
	return sysweb.ProxySpec{Domain: strings.ToLower(strings.TrimSpace(a["domain"].Value())), Port: strings.TrimSpace(a["port"].Value()), WebSocket: a["ws"].Yes()}
}

// HTTPSForm menanyakan domain untuk sertifikat.
func HTTPSForm(sites []sysweb.Site) ask.Form {
	var opts []ask.Option
	for _, s := range sites {
		if !s.Enabled || s.SSL {
			continue
		}
		for _, n := range s.ServerNames {
			if strings.HasPrefix(n, "www.") {
				continue
			}
			opts = append(opts, ask.Option{Value: n, Label: n, Description: "site " + s.Name, Recommended: len(opts) == 0})
		}
	}
	return ask.Form{ID: "https", Title: "Pasang HTTPS", Questions: []ask.Question{
		{ID: "domain", Header: "Domain", Kind: ask.Single, Prompt: "Pasang HTTPS untuk domain mana?", Options: opts, Other: true, Validate: sysweb.ValidateDomain,
			Help: "Syarat: DNS domain sudah mengarah ke IP server ini, port 80 terbuka dari internet, dan ada site nginx dengan server_name domain tersebut."},
		{ID: "www", Header: "www", Kind: ask.Confirm, Prompt: "Sertakan juga www.<domain>?", Default: []string{ask.ValueNo}, Options: []ask.Option{
			{Label: "Ya", Description: "Hanya bila record DNS www juga sudah dibuat; bila tidak, certbot gagal."},
			{Label: "Tidak", Recommended: true},
		}},
	}}
}

func domainAnswer(a ask.Answer) string { return strings.ToLower(strings.TrimSpace(a.Value())) }

// HTTPSPlan memastikan domain bisa di-resolve dan nginx valid sebelum menjalankan certbot.
func HTTPSPlan(certbot, domain string, www bool) run.Plan {
	p := sysweb.CertbotPlan(certbot, domain, www)
	p.Title = "Pasang HTTPS untuk " + domain
	p.Steps = append([]run.Command{{
		Title: "Pastikan DNS " + domain + " sudah ada", Argv: []string{"getent", "ahosts", domain},
		Explain: []run.Line{{Token: "getent ahosts", Meaning: "cari IP domain; bila gagal, record DNS belum dibuat atau belum menyebar"}},
		Effect:  "Bandingkan IP yang muncul dengan IP publik server ini. Bila berbeda, Let's Encrypt tidak bisa memverifikasi domain.",
		Risk:    risk.Safe,
	}}, p.Steps...)
	p.Check = &run.Command{Title: "Validasi konfigurasi nginx", Argv: []string{"nginx", "-t"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "nginx -t", Meaning: "certbot mengubah konfigurasi nginx; pastikan konfigurasi saat ini valid"}}}
	return p
}

// siteModel menampilkan satu site nginx.
type siteModel struct {
	env     shared.Env
	paths   sysweb.Paths
	site    sysweb.Site
	certbot string
	viewer  ui.Viewer
	message string
}

func newSite(env shared.Env, p sysweb.Paths, s sysweb.Site, certbot string) *siteModel {
	m := &siteModel{env: env, paths: p, site: s, certbot: certbot}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		m.viewer.Lines = []string{err.Error()}
	} else {
		m.viewer.Lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	return m
}

func (m *siteModel) Title() string { return m.site.Name }
func (m *siteModel) Init() tea.Cmd { return nil }
func (m *siteModel) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	toggle := "aktifkan"
	if m.site.Enabled {
		toggle = "nonaktifkan"
	}
	keys := []key.Binding{b("e", toggle), b("↑↓", "gulir")}
	if len(m.site.ServerNames) > 0 {
		keys = append(keys, b("c", "cek sertifikat"))
	}
	return keys
}

func (m *siteModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "e":
			return m, nav.Push(runflow.Confirm(sysweb.SiteTogglePlan(m.site, m.paths, !m.site.Enabled), m.env.Deps))
		case "c":
			if len(m.site.ServerNames) > 0 {
				return m, nav.Push(newCertCheck(m.env, m.site.ServerNames[0]))
			}
		default:
			m.viewer.HandleKey(msg.String())
		}
	case nav.ResumedMsg:
		if r, ok := msg.Result.(run.Outcome); ok && r.Approved && r.OK() {
			m.site.Enabled = !m.site.Enabled
			m.message = "✓ " + r.Plan.Title + " selesai."
		}
	}
	return m, nil
}

func (m *siteModel) View(width, height int) string {
	t := ui.Current
	s := m.site
	state := t.Muted.Render("tidak aktif")
	if s.Enabled {
		state = t.Success.Render("aktif")
	}
	top := []string{"", " " + t.Title.Render(s.Path) + "  " + state + "   " + t.Muted.Render("command setara: cat "+s.Path)}
	if m.message != "" {
		top = append(top, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	if len(s.ProxyPass) > 0 {
		top = append(top, ui.Wrap(t.Subtle.Render("Bila situs menampilkan 502 Bad Gateway: aplikasi di "+strings.Join(s.ProxyPass, ", ")+" tidak berjalan atau port-nya salah."), width, " "))
	}
	top = append(top, "")
	topStr := strings.Join(top, "\n")
	return topStr + "\n" + m.viewer.View(width, height-lipgloss.Height(topStr), false)
}

// certCheckModel memeriksa sertifikat HTTPS sebuah domain.
type certCheckModel struct {
	env    shared.Env
	domain string
	res    *tlscheck.Result
}

type certMsg struct {
	owner *certCheckModel
	res   tlscheck.Result
}

func newCertCheck(env shared.Env, domain string) *certCheckModel {
	return &certCheckModel{env: env, domain: domain}
}

func (m *certCheckModel) Title() string { return "Sertifikat " + m.domain }
func (m *certCheckModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "periksa ulang"))}
}

func (m *certCheckModel) Init() tea.Cmd {
	d, now := m.domain, m.env.Now
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return certMsg{owner: m, res: tlscheck.CheckHost(ctx, d, "443", now())}
	}
}

func (m *certCheckModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case certMsg:
		if msg.owner == m {
			m.res = &msg.res
		}
	case nav.RefreshMsg:
		m.res = nil
		return m, m.Init()
	}
	return m, nil
}

func (m *certCheckModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Muted.Render("command setara: openssl s_client -connect "+m.domain+":443 -servername "+m.domain+" </dev/null | openssl x509 -noout -dates -subject -issuer"), ""}
	switch r := m.res; {
	case r == nil:
		lines = append(lines, " "+t.Subtle.Render("Menghubungi "+m.domain+":443…"))
	case r.Err != nil:
		lines = append(lines, ui.Wrap(t.Danger.Render("✗ Tidak bisa terhubung: "+r.Err.Error()), width, " "),
			ui.Wrap(t.Subtle.Render("Periksa DNS domain, port 443 terbuka, dan HTTPS sudah dipasang. Wizard konektivitas di modul Network bisa membantu."), width, " "))
	default:
		status := t.Success.Render("✓ Sertifikat valid")
		if r.Problem != "" {
			status = t.Danger.Render("✗ " + r.Problem)
		} else if w := r.Warning(m.env.Now()); w != "" {
			status = t.Warning.Render("⚠ " + w)
		}
		lines = append(lines, ui.Wrap(status, width, " "), "")
		c := r.Cert
		days := c.DaysLeft(m.env.Now())
		san := c.DNSNames
		if len(san) > 6 {
			san = append(san[:6:6], fmt.Sprintf("(+%d lagi)", len(c.DNSNames)-6))
		}
		lines = append(lines, ui.Detail([]ui.Pair{
			{Key: "Untuk", Value: c.Subject},
			{Key: "Nama lain (SAN)", Value: strings.Join(san, ", ")},
			{Key: "Penerbit", Value: c.Issuer},
			{Key: "Berlaku", Value: c.NotBefore.Format("2006-01-02") + " sampai " + c.NotAfter.Format("2006-01-02"), Hint: fmt.Sprintf("sisa %d hari", days)},
			{Key: "Rantai", Value: fmt.Sprintf("%d sertifikat dikirim server", r.Chain), Hint: "minimal 2 (sertifikat + perantara); bila 1, sebagian perangkat menganggapnya tidak tepercaya"},
		}, width))
	}
	return ui.FitHeight(strings.Join(lines, "\n"), width, height)
}
