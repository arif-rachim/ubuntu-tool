// Package network adalah layar modul Network: IP, DNS, koneksi aktif, dan wizard
// "kenapa tidak bisa konek" (keluar maupun masuk).
package network

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/netinfo"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/checklist"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// Opener membuka modul lain berdasarkan ID menu (dipasang oleh main supaya tidak import melingkar).
type Opener func(id string) nav.Screen

// Model adalah dashboard jaringan.
type Model struct {
	env        shared.Env
	open       Opener
	info       netinfo.Info
	loaded     bool
	err        error
	showVirt   bool
	offset     int
	outbound   func(netinfo.Target) []netinfo.Step
	inbound    func(port int) []netinfo.Step
	hostsPath  string
	netplanDir string
	wizard     string // "k" atau "m": langsung buka wizard saat layar dibuka (dari Diagnosa)
}

type infoMsg struct {
	owner *Model
	info  netinfo.Info
	err   error
}

// New membuat dashboard jaringan.
func New(env shared.Env, open Opener) *Model {
	out := netinfo.DefaultOutboundEnv(env.Runner, env.ProcRoot)
	in := netinfo.DefaultInboundEnv(env.Runner, env.ProcRoot)
	return &Model{
		env: env, open: open,
		outbound:   func(t netinfo.Target) []netinfo.Step { return netinfo.OutboundSteps(out, t) },
		inbound:    func(p int) []netinfo.Step { return netinfo.InboundSteps(in, p) },
		hostsPath:  "/etc/hosts",
		netplanDir: "/etc/netplan",
	}
}

// WithWizard membuka wizard "outbound" atau "inbound" begitu layar tampil.
func (m *Model) WithWizard(name string) *Model {
	m.wizard = map[string]string{"outbound": "k", "inbound": "m"}[name]
	return m
}

var _ nav.Screen = (*Model)(nil)

func (m *Model) Title() string { return "Network & konektivitas" }

func (m *Model) Keys() []key.Binding {
	b := func(k, d string) key.Binding { return key.NewBinding(key.WithKeys(k), key.WithHelp(k, d)) }
	return []key.Binding{b("k", "tidak bisa konek ke…"), b("m", "tidak bisa diakses dari luar"), b("t", "tracepath"), b("i", "IP publik"), b("h", "/etc/hosts & netplan"), b("v", "interface virtual")}
}

func (m *Model) HelpText() string {
	return "Gateway adalah router yang dilewati server untuk keluar ke internet. DNS mengubah nama domain menjadi IP. " +
		"Di Ubuntu, /etc/resolv.conf menunjuk 127.0.0.53 (systemd-resolved) yang meneruskan ke server DNS sebenarnya. " +
		"Command setara: ip addr, ip route, resolvectl status, ss -tn state established"
}

func (m *Model) Init() tea.Cmd {
	r, root := m.env.Runner, m.env.ProcRoot
	load := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		info, err := netinfo.Read(ctx, r, root)
		return infoMsg{owner: m, info: info, err: err}
	}
	if m.wizard != "" {
		w := m.wizard
		m.wizard = ""
		_, open := m.key(w)
		return tea.Batch(load, open)
	}
	return load
}

func (m *Model) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case infoMsg:
		if msg.owner == m {
			m.loaded, m.info, m.err = true, msg.info, msg.err
		}
	case nav.RefreshMsg:
		return m, m.Init()
	case nav.ResumedMsg:
		if r, ok := msg.Result.(ask.Result); ok && !r.Cancelled {
			switch r.ID {
			case "outbound":
				target, _ := netinfo.ParseTarget(r.Answers["target"].Value())
				return m, nav.Push(m.checklist("Cek koneksi ke "+target.Host,
					"ubt memeriksa jalur dari server ini ke "+targetAddr(target)+" langkah demi langkah dan berhenti di langkah pertama yang bermasalah.",
					m.outbound(target)))
			case "inbound":
				port, _ := strconv.Atoi(r.Answers["port"].Value())
				return m, nav.Push(m.checklist(fmt.Sprintf("Kenapa port %d tidak bisa diakses", port),
					"ubt memeriksa penyebab paling umum sebuah aplikasi tidak bisa diakses dari luar, dari yang paling sering terjadi.",
					m.inbound(port)))
			case "tracepath":
				host := strings.TrimSpace(r.Answers["host"].Value())
				return m, nav.Push(runflow.Confirm(TracepathPlan(host), m.env.Deps))
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg.String())
	}
	return m, nil
}

func (m *Model) key(k string) (nav.Screen, tea.Cmd) {
	{
		switch k {
		case "k":
			return m, nav.Push(ask.New(ask.Form{ID: "outbound", Title: "Tidak bisa konek", Questions: []ask.Question{{
				ID: "target", Prompt: "Ke mana server ini tidak bisa terhubung?", Kind: ask.Text, Placeholder: "contoh.com, api.contoh.com:8443, atau https://contoh.com",
				Help:     "Isi nama domain, IP, atau URL. Tanpa port, yang diperiksa adalah HTTPS (443).",
				Validate: func(s string) error { _, err := netinfo.ParseTarget(s); return err },
			}}}))
		case "m":
			return m, nav.Push(ask.New(ask.Form{ID: "inbound", Title: "Tidak bisa diakses dari luar", Questions: []ask.Question{{
				ID: "port", Prompt: "Aplikasi di port berapa yang tidak bisa diakses dari luar?", Kind: ask.Text, Placeholder: "3000",
				Help:     "Port tempat aplikasimu berjalan di server ini, mis. 80 untuk web, 3000 untuk Node.js, 5432 untuk PostgreSQL.",
				Validate: validPort,
			}}}))
		case "t":
			return m, nav.Push(ask.New(ask.Form{ID: "tracepath", Title: "Lacak rute", Questions: []ask.Question{{
				ID: "host", Prompt: "Lacak rute jaringan ke host mana?", Kind: ask.Text, Placeholder: "1.1.1.1",
				Validate: func(s string) error {
					if strings.TrimSpace(s) == "" || strings.ContainsAny(s, " /:") {
						return errors.New("isi nama host atau IP saja, tanpa port atau URL")
					}
					return nil
				},
			}}}))
		case "i":
			return m, nav.Push(runflow.Confirm(PublicIPPlan(), m.env.Deps))
		case "h":
			return m, nav.Push(newFiles(m.hostsPath, m.netplanDir))
		case "v":
			m.showVirt = !m.showVirt
		case "up":
			m.offset = max(m.offset-1, 0)
		case "down":
			m.offset++
		}
	}
	return m, nil
}

func targetAddr(t netinfo.Target) string { return t.Host + ":" + t.Port }

func validPort(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return errors.New("port harus angka 1-65535")
	}
	return nil
}

func (m *Model) checklist(title, intro string, steps []netinfo.Step) *checklist.Model {
	c := checklist.New(title, intro, steps)
	if m.open != nil {
		c.OnNext = func(id string) tea.Cmd {
			if s := m.open(id); s != nil {
				return nav.Push(s)
			}
			return nil
		}
	}
	return c
}

// PublicIPPlan menanyakan IP publik server ke layanan luar.
func PublicIPPlan() run.Plan {
	return run.Single(run.Command{
		Title: "Cari IP publik server",
		Argv:  []string{"curl", "-sS", "--max-time", "10", "https://ifconfig.me/ip"},
		Explain: []run.Line{
			{Token: "curl -sS", Meaning: "ambil halaman web tanpa progress bar, tetapi tetap tampilkan error"},
			{Token: "https://ifconfig.me/ip", Meaning: "layanan pihak ketiga yang membalas dengan IP asal permintaan"},
		},
		Effect: "Mengirim permintaan ke ifconfig.me (layanan luar). IP ini yang dilihat orang dari internet; bisa berbeda dari IP di interface bila server berada di balik NAT.",
		Risk:   risk.Safe,
	})
}

// TracepathPlan melacak rute ke host.
func TracepathPlan(host string) run.Plan {
	return run.Single(run.Command{
		Title: "Lacak rute ke " + host,
		Argv:  []string{"tracepath", "-n", host},
		Explain: []run.Line{
			{Token: "tracepath", Meaning: "tampilkan setiap router (hop) yang dilewati paket menuju tujuan; tidak butuh root"},
			{Token: "-n", Meaning: "tampilkan IP tanpa mencari nama host (lebih cepat)"},
		},
		Effect: "Baris \"no reply\" wajar untuk router yang tidak membalas. Bila berhenti total di satu hop, di situlah jalurnya terputus.",
		Risk:   risk.Safe,
	})
}

func (m *Model) View(width, height int) string {
	t := ui.Current
	switch {
	case !m.loaded:
		return "\n " + t.Subtle.Render("Membaca konfigurasi jaringan…")
	case m.err != nil:
		return ui.ErrorState(m.err, "install iproute2: sudo apt install iproute2", width)
	}
	info := m.info
	var lines []string
	lines = append(lines, "", " "+t.Title.Render("Interface")+"   "+t.Muted.Render("command setara: ip -br addr"))
	hidden := 0
	for _, i := range info.Interfaces {
		if i.Virtual() && !m.showVirt {
			hidden++
			continue
		}
		state := t.Success.Render("aktif")
		if !i.Up() {
			state = t.Muted.Render("mati")
		}
		var addrs []string
		for _, a := range i.Addrs {
			if a.Scope == "link" && !m.showVirt {
				continue
			}
			addrs = append(addrs, fmt.Sprintf("%s/%d", a.Local, a.Prefix))
		}
		kind := map[string]string{"loopback": "loopback (server ini sendiri)"}[i.LinkType]
		line := fmt.Sprintf("   %-16s %s  %s", i.Name, state, strings.Join(addrs, ", "))
		if kind != "" {
			line += "  " + t.Muted.Render(kind)
		}
		lines = append(lines, line)
	}
	if hidden > 0 {
		lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf("+ %d interface virtual (Docker/bridge) disembunyikan — tekan v", hidden)))
	}

	lines = append(lines, "", " "+t.Title.Render("Jalan keluar")+"   "+t.Muted.Render("command setara: ip route"))
	if def, ok := netinfo.DefaultRoute(info.Routes); ok {
		lines = append(lines, fmt.Sprintf("   gateway default %s lewat %s", t.Accent.Render(def.Gateway), def.Dev)+t.Subtle.Render(" ("+def.Protocol+")"))
	} else {
		lines = append(lines, "   "+t.Danger.Render("✗ tidak ada gateway default: server tidak bisa ke internet"))
	}

	lines = append(lines, "", " "+t.Title.Render("DNS")+"   "+t.Muted.Render("command setara: resolvectl status"))
	servers := info.DNS.Servers()
	if len(servers) == 0 {
		lines = append(lines, "   "+t.Warning.Render("⚠ tidak ada server DNS terdeteksi"))
	} else {
		lines = append(lines, "   server: "+strings.Join(servers, ", "))
	}
	if info.DNS.Stub {
		lines = append(lines, ui.Wrap(t.Subtle.Render("/etc/resolv.conf menunjuk 127.0.0.53 (systemd-resolved), yang meneruskan permintaan ke server di atas. Ini normal di Ubuntu."), width, "   "))
	}

	lines = append(lines, "", " "+t.Title.Render(fmt.Sprintf("Koneksi aktif (%d)", info.ConnTotal))+"   "+t.Muted.Render("command setara: ss -tn state established"))
	if len(info.Conns) == 0 {
		lines = append(lines, "   "+t.Muted.Render("tidak ada koneksi TCP ke luar server"))
	}
	for i, g := range info.Conns {
		if i == 8 {
			lines = append(lines, "   "+t.Muted.Render(fmt.Sprintf("+ %d IP lainnya", len(info.Conns)-8)))
			break
		}
		dir := t.Subtle.Render("keluar")
		if g.Inbound {
			var ps []string
			for _, p := range g.LocalPorts {
				ps = append(ps, strconv.Itoa(p))
			}
			dir = t.Accent.Render("masuk ke port " + strings.Join(ps, ", "))
		}
		warn := ""
		if g.Count >= 50 {
			warn = t.Warning.Render("  ⚠ sangat banyak koneksi dari satu IP")
		}
		lines = append(lines, fmt.Sprintf("   %-40s %4d koneksi  %s%s", g.IP, g.Count, dir, warn))
	}

	lines = append(lines, "", ui.Section("Ada masalah?"),
		"   "+t.Accent.Render("k")+"  server ini tidak bisa konek ke domain/IP tertentu",
		"   "+t.Accent.Render("m")+"  aplikasi di server ini tidak bisa diakses dari luar",
		"   "+t.Accent.Render("t")+"  lacak rute jaringan (tracepath)",
		"   "+t.Accent.Render("i")+"  cari IP publik server (menghubungi ifconfig.me)",
	)
	all := strings.Split(strings.Join(lines, "\n"), "\n")
	m.offset = min(m.offset, max(len(all)-height, 0))
	return ui.FitHeight(strings.Join(all[m.offset:], "\n"), width, height)
}

// filesModel menampilkan /etc/hosts dan konfigurasi netplan (hanya baca).
type filesModel struct {
	viewer ui.Viewer
}

func newFiles(hostsPath, netplanDir string) *filesModel {
	t := ui.Current
	var lines []string
	add := func(path string) {
		lines = append(lines, t.Accent.Bold(true).Render("── "+path))
		data, err := os.ReadFile(path)
		switch {
		case err != nil && os.IsPermission(err):
			lines = append(lines, t.Muted.Render("(butuh root untuk membaca: sudo cat "+path+")"))
		case err != nil:
			lines = append(lines, t.Muted.Render("("+err.Error()+")"))
		default:
			lines = append(lines, strings.Split(strings.TrimRight(string(data), "\n"), "\n")...)
		}
		lines = append(lines, "")
	}
	add(hostsPath)
	matches, _ := filepath.Glob(filepath.Join(netplanDir, "*.yaml"))
	if len(matches) == 0 {
		lines = append(lines, t.Muted.Render("(tidak ada file netplan di "+netplanDir+")"))
	}
	for _, p := range matches {
		add(p)
	}
	lines = append(lines, t.Warning.Render("Mengubah netplan yang salah bisa membuat server hilang dari jaringan."),
		t.Subtle.Render("Selalu uji dengan: sudo netplan try  (otomatis dibatalkan setelah 120 detik bila tidak dikonfirmasi)."))
	return &filesModel{viewer: ui.Viewer{Lines: lines}}
}

func (f *filesModel) Title() string { return "/etc/hosts & netplan" }
func (f *filesModel) Init() tea.Cmd { return nil }
func (f *filesModel) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("up"), key.WithHelp("↑↓", "gulir"))}
}
func (f *filesModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		f.viewer.HandleKey(k.String())
	}
	return f, nil
}
func (f *filesModel) View(width, height int) string {
	return "\n" + f.viewer.View(width, height-1, false)
}
