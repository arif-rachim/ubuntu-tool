// Package web mendeteksi web server (nginx/apache/caddy), membaca site nginx, dan membangun command
// untuk reverse proxy, HTTPS Let's Encrypt, dan pengelolaan site. Murni pengumpul data.
package web

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Server adalah satu web server yang terdeteksi.
type Server struct {
	Name      string // nginx, apache2, caddy
	Installed bool
	Active    bool
}

// Site adalah satu file site nginx.
type Site struct {
	Name        string
	Path        string
	Enabled     bool
	ServerNames []string
	Listen      []string
	ProxyPass   []string
	Root        string
	SSL         bool
	CertPath    string
	Managed     bool // dibuat oleh ubt
}

// Paths adalah lokasi yang dibaca (diganti saat test).
type Paths struct {
	NginxBin       string
	ApacheBin      string
	CaddyBin       string
	SitesAvailable string
	SitesEnabled   string
	CertbotBins    []string
}

// DefaultPaths untuk sistem sungguhan.
var DefaultPaths = Paths{
	NginxBin: "/usr/sbin/nginx", ApacheBin: "/usr/sbin/apache2", CaddyBin: "/usr/bin/caddy",
	SitesAvailable: "/etc/nginx/sites-available", SitesEnabled: "/etc/nginx/sites-enabled",
	CertbotBins: []string{"/usr/bin/certbot", "/snap/bin/certbot"},
}

// State adalah kondisi web server.
type State struct {
	Servers []Server
	Sites   []Site
	Certbot string // path certbot, kosong bila tidak terpasang
}

// Nginx mengembalikan status nginx.
func (s State) Nginx() Server {
	for _, sv := range s.Servers {
		if sv.Name == "nginx" {
			return sv
		}
	}
	return Server{Name: "nginx"}
}

// Directive bisa di awal baris atau setelah { / ; (blok satu baris seperti "location / { proxy_pass …; }").
var directiveRe = regexp.MustCompile(`(?m)(?:^|[{;])\s*(server_name|listen|proxy_pass|root|ssl_certificate)\s+([^;{}]+);`)

// ParseSite membaca directive penting dari isi file site nginx.
func ParseSite(content string) Site {
	var s Site
	for _, m := range directiveRe.FindAllStringSubmatch(stripComments(content), -1) {
		val := strings.TrimSpace(m[2])
		switch m[1] {
		case "server_name":
			for _, n := range strings.Fields(val) {
				if n != "_" && !contains(s.ServerNames, n) {
					s.ServerNames = append(s.ServerNames, n)
				}
			}
		case "listen":
			if !contains(s.Listen, val) {
				s.Listen = append(s.Listen, val)
			}
			if strings.Contains(val, "ssl") || strings.Contains(val, "443") {
				s.SSL = true
			}
		case "proxy_pass":
			if !contains(s.ProxyPass, val) {
				s.ProxyPass = append(s.ProxyPass, val)
			}
		case "root":
			s.Root = val
		case "ssl_certificate":
			s.CertPath, s.SSL = val, true
		}
	}
	s.Managed = strings.Contains(content, "Dibuat oleh ubt")
	return s
}

func stripComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if j := strings.Index(l, "#"); j >= 0 {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// ReadSites membaca sites-available dan menandai yang aktif di sites-enabled.
func ReadSites(p Paths) []Site {
	enabled := map[string]bool{}
	links, _ := os.ReadDir(p.SitesEnabled)
	for _, l := range links {
		target, err := filepath.EvalSymlinks(filepath.Join(p.SitesEnabled, l.Name()))
		if err == nil {
			enabled[target] = true
		}
		enabled[filepath.Join(p.SitesAvailable, l.Name())] = true
	}
	files, _ := os.ReadDir(p.SitesAvailable)
	var sites []Site
	for _, f := range files {
		if f.IsDir() || strings.HasPrefix(f.Name(), ".") {
			continue
		}
		path := filepath.Join(p.SitesAvailable, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		s := ParseSite(string(data))
		s.Name, s.Path = f.Name(), path
		real, _ := filepath.EvalSymlinks(path)
		s.Enabled = enabled[path] || enabled[real]
		sites = append(sites, s)
	}
	sort.SliceStable(sites, func(i, j int) bool {
		if sites[i].Enabled != sites[j].Enabled {
			return sites[i].Enabled
		}
		return sites[i].Name < sites[j].Name
	})
	return sites
}

// Read mendeteksi web server, site nginx, dan certbot.
func Read(ctx context.Context, r run.Runner, p Paths) State {
	var st State
	for _, sv := range []struct{ name, bin, unit string }{{"nginx", p.NginxBin, "nginx"}, {"apache2", p.ApacheBin, "apache2"}, {"caddy", p.CaddyBin, "caddy"}} {
		s := Server{Name: sv.name}
		if _, err := os.Stat(sv.bin); err == nil {
			s.Installed = true
			out, _, _ := r.Capture(ctx, run.Command{Argv: []string{"systemctl", "is-active", sv.unit}})
			s.Active = strings.TrimSpace(out) == "active"
		}
		st.Servers = append(st.Servers, s)
	}
	if st.Nginx().Installed {
		st.Sites = ReadSites(p)
	}
	for _, b := range p.CertbotBins {
		if _, err := os.Stat(b); err == nil {
			st.Certbot = b
			break
		}
	}
	return st
}

var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// ValidateDomain memeriksa nama domain untuk site & sertifikat.
func ValidateDomain(s string) error {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "://") || strings.Contains(s, "/"):
		return errors.New("isi domain saja tanpa http:// atau path, mis. app.contoh.com")
	case !domainRe.MatchString(s):
		return errors.New("domain tidak valid, contoh: app.contoh.com")
	}
	return nil
}

// ProxySpec adalah isian wizard reverse proxy.
type ProxySpec struct {
	Domain    string
	Port      string
	WebSocket bool
	BodySize  string // client_max_body_size, mis. "20m"
}

var proxyTmpl = template.Must(template.New("proxy").Parse(`# Dibuat oleh ubt: reverse proxy {{.Domain}} → 127.0.0.1:{{.Port}}
server {
    listen 80;
    listen [::]:80;
    server_name {{.Domain}};

    # Ukuran upload maksimal; error 413 berarti file lebih besar dari ini.
    client_max_body_size {{.BodySize}};

    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
{{- if .WebSocket}}

        # WebSocket (socket.io, live update)
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
{{- end}}
    }
}
`))

// ProxyConfig merender file site nginx untuk reverse proxy.
func ProxyConfig(s ProxySpec) string {
	if s.BodySize == "" {
		s.BodySize = "20m"
	}
	var b strings.Builder
	proxyTmpl.Execute(&b, s)
	return b.String()
}

// ProxyPlan menulis site, mengaktifkannya, memvalidasi, lalu reload nginx.
func ProxyPlan(s ProxySpec, p Paths, ufwActive bool) run.Plan {
	avail := filepath.Join(p.SitesAvailable, run.FileName(s.Domain))
	enabled := filepath.Join(p.SitesEnabled, run.FileName(s.Domain))
	var steps []run.Command
	if ufwActive {
		steps = append(steps, run.Command{Title: "Buka port 80 & 443 di firewall", Argv: []string{"ufw", "allow", "Nginx Full"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "ufw allow 'Nginx Full'", Meaning: "profil ufw bawaan nginx: izinkan HTTP (80) dan HTTPS (443)"}}, Risk: risk.Caution})
	}
	steps = append(steps,
		run.Command{Title: "Tulis konfigurasi site", Argv: []string{"install", "-m", "0644", "/dev/stdin", avail}, NeedsRoot: true,
			Stdin: ProxyConfig(s), StdinLabel: "(site nginx)",
			Explain: []run.Line{{Token: avail, Meaning: "sites-available: semua konfigurasi site, aktif atau tidak"}}, Risk: risk.Caution},
		run.Command{Title: "Aktifkan site", Argv: []string{"ln", "-sfn", avail, enabled}, NeedsRoot: true,
			Explain: []run.Line{{Token: "ln -s", Meaning: "site aktif bila ada symlink di sites-enabled"}}},
		run.Command{Title: "Muat ulang nginx", Argv: []string{"systemctl", "reload", "nginx"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "reload", Meaning: "terapkan konfigurasi baru tanpa memutus koneksi yang sedang berjalan"}},
			Effect:  "http://" + s.Domain + " diteruskan ke aplikasi di port " + s.Port + ". Bila muncul 502 Bad Gateway, aplikasinya belum berjalan atau port-nya salah.",
			Risk:    risk.Caution},
	)
	return run.Plan{
		Title: "Reverse proxy " + s.Domain + " → port " + s.Port,
		Steps: steps,
		Check: &run.Command{Title: "Validasi konfigurasi nginx", Argv: []string{"nginx", "-t"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "nginx -t", Meaning: "cek sintaks semua konfigurasi; bila gagal, nginx tidak di-reload sehingga site lain tetap jalan"}}},
	}
}

// SiteTogglePlan mengaktifkan atau menonaktifkan site.
func SiteTogglePlan(site Site, p Paths, enable bool) run.Plan {
	// Path dibangun ulang dari nama, bukan dari site.Path, supaya symlink selalu di dalam folder nginx.
	name := run.FileName(site.Name)
	link := filepath.Join(p.SitesEnabled, name)
	site.Path = filepath.Join(p.SitesAvailable, name)
	var step run.Command
	if enable {
		step = run.Command{Title: "Aktifkan site " + site.Name, Argv: []string{"ln", "-sfn", site.Path, link}, NeedsRoot: true,
			Explain: []run.Line{{Token: "ln -s", Meaning: "buat symlink di sites-enabled"}}}
	} else {
		step = run.Command{Title: "Nonaktifkan site " + site.Name, Argv: []string{"rm", link}, NeedsRoot: true,
			Explain: []run.Line{{Token: "rm " + link, Meaning: "hapus symlink saja; file di sites-available tetap ada"}}, Risk: risk.Caution}
	}
	return run.Plan{
		Title: step.Title,
		Steps: []run.Command{step, {Title: "Muat ulang nginx", Argv: []string{"systemctl", "reload", "nginx"}, NeedsRoot: true, Risk: risk.Caution,
			Explain: []run.Line{{Token: "systemctl reload nginx", Meaning: "terapkan konfigurasi baru tanpa memutus koneksi yang sedang berjalan"}}}},
		Check: &run.Command{Title: "Validasi konfigurasi nginx", Argv: []string{"nginx", "-t"}, NeedsRoot: true,
			Explain: []run.Line{{Token: "nginx -t", Meaning: "periksa konfigurasi; bila gagal, nginx tidak di-reload sehingga situs lain tetap jalan"}}},
	}
}

// TestConfigPlan menjalankan nginx -t.
func TestConfigPlan() run.Plan {
	return run.Single(run.Command{Title: "Uji konfigurasi nginx", Argv: []string{"nginx", "-t"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "nginx -t", Meaning: "periksa sintaks semua file konfigurasi tanpa mengubah apa pun"}}})
}

// InstallNginxPlan memasang nginx.
func InstallNginxPlan() run.Plan {
	return run.Single(run.Command{Title: "Install nginx", Argv: []string{"apt-get", "install", "-y", "nginx"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "nginx", Meaning: "web server & reverse proxy ringan"}},
		Effect:  "nginx langsung berjalan di port 80 dengan halaman default. Bila apache2 juga terpasang, keduanya berebut port 80.",
		Risk:    risk.Caution})
}

// InstallCertbotPlan memasang certbot dengan cara resmi (snap).
func InstallCertbotPlan() run.Plan {
	return run.Plan{
		Title: "Install certbot",
		Steps: []run.Command{
			{Title: "Install certbot lewat snap", Argv: []string{"snap", "install", "--classic", "certbot"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "snap install --classic certbot", Meaning: "cara resmi yang dianjurkan Let's Encrypt: selalu versi terbaru & perpanjangan otomatis"}}},
			{Title: "Buat perintah certbot", Argv: []string{"ln", "-sfn", "/snap/bin/certbot", "/usr/bin/certbot"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "ln -s", Meaning: "supaya perintah certbot bisa dipanggil dari mana saja"}}},
		},
	}
}

// CertbotPlan meminta sertifikat untuk domain dan memasangnya ke nginx.
func CertbotPlan(certbot, domain string, www bool) run.Plan {
	argv := []string{certbot, "--nginx", "-d", domain}
	if www {
		argv = append(argv, "-d", "www."+domain)
	}
	return run.Single(run.Command{
		Title: "Pasang HTTPS untuk " + domain, Argv: argv, NeedsRoot: true, Interactive: true,
		Explain: []run.Line{
			{Token: "--nginx", Meaning: "minta sertifikat lalu ubah konfigurasi nginx otomatis (listen 443 ssl + redirect)"},
			{Token: "-d " + domain, Meaning: "domain yang disertifikasi; harus mengarah ke IP server ini"},
		},
		Effect: "certbot menanyakan email (untuk pemberitahuan kedaluwarsa) dan persetujuan syarat layanan di terminal. Sertifikat berlaku 90 hari dan diperpanjang otomatis.",
		Risk:   risk.Caution,
	})
}

// RenewDryRunPlan menguji perpanjangan sertifikat.
func RenewDryRunPlan(certbot string) run.Plan {
	return run.Single(run.Command{Title: "Uji perpanjangan sertifikat", Argv: []string{certbot, "renew", "--dry-run"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "renew --dry-run", Meaning: "simulasi perpanjangan semua sertifikat tanpa benar-benar menggantinya"}},
		Effect:  "Bila berhasil, perpanjangan otomatis akan bekerja."})
}

// ListCertsPlan menampilkan sertifikat certbot.
func ListCertsPlan(certbot string) run.Plan {
	return run.Single(run.Command{Title: "Daftar sertifikat", Argv: []string{certbot, "certificates"}, NeedsRoot: true,
		Explain: []run.Line{{Token: "certificates", Meaning: "tampilkan domain, lokasi file, dan tanggal kedaluwarsa setiap sertifikat"}}})
}

// ExplainHTTPStatus menerjemahkan kode error HTTP yang sering muncul di balik nginx.
func ExplainHTTPStatus(code int) string {
	switch code {
	case 502:
		return "502 Bad Gateway: nginx tidak bisa menghubungi aplikasi di belakangnya. Aplikasi mati, crash, atau proxy_pass menunjuk port yang salah. Cek di modul Ports & Proses."
	case 504:
		return "504 Gateway Timeout: aplikasi terlalu lama menjawab. Cek beban server (modul Resource) atau naikkan proxy_read_timeout."
	case 413:
		return "413 Request Entity Too Large: upload melebihi client_max_body_size di konfigurasi nginx."
	case 403:
		return "403 Forbidden: nginx tidak boleh membaca file (izin folder root) atau index.html tidak ada."
	case 404:
		return "404 Not Found: path tidak ada, atau server_name tidak cocok sehingga site default yang menjawab."
	}
	return fmt.Sprintf("HTTP %d", code)
}
