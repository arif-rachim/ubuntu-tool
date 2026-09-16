package netinfo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/tlscheck"
)

// Alias supaya kode pemeriksaan tetap ringkas; definisinya ada di paket check.
type (
	Status     = check.Status
	StepResult = check.Result
	Step       = check.Step
)

const (
	StatusOK   = check.OK
	StatusWarn = check.Warn
	StatusFail = check.Fail
	StatusSkip = check.Skip
)

// Target adalah tujuan yang diperiksa.
type Target struct {
	Host   string
	Port   string
	Scheme string // http, https, atau kosong
}

// ParseTarget menerima "contoh.com", "contoh.com:5432", "1.2.3.4", atau "https://contoh.com/path".
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Target{}, errors.New("isi nama domain, IP, atau URL")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return Target{}, errors.New("URL tidak valid")
		}
		t := Target{Host: u.Hostname(), Port: u.Port(), Scheme: u.Scheme}
		if t.Port == "" {
			t.Port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
		}
		if t.Port == "" {
			return Target{}, errors.New("skema URL tidak dikenal; pakai http:// atau https://")
		}
		return t, nil
	}
	host, port := s, ""
	if h, p, err := net.SplitHostPort(s); err == nil {
		host, port = h, p
	}
	if strings.ContainsAny(host, " /") {
		return Target{}, errors.New("nama host tidak boleh mengandung spasi atau /")
	}
	if port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return Target{}, errors.New("port harus angka 1-65535")
		}
	}
	t := Target{Host: host, Port: port}
	switch port {
	case "":
		t.Port, t.Scheme = "443", "https"
	case "80":
		t.Scheme = "http"
	case "443":
		t.Scheme = "https"
	}
	return t, nil
}

func (t Target) isIP() bool {
	_, err := netip.ParseAddr(strings.Trim(t.Host, "[]"))
	return err == nil
}

// OutboundEnv adalah ketergantungan pemeriksaan keluar (diganti saat test).
type OutboundEnv struct {
	Runner     run.Runner
	Info       func(ctx context.Context) (Info, error)
	Resolve    func(ctx context.Context, host string) ([]string, error)
	ResolveVia func(ctx context.Context, server, host string) ([]string, error)
	Dial       func(ctx context.Context, addr string) error
	HTTP       func(ctx context.Context, rawURL string) (status int, elapsed time.Duration, err error)
	TLS        func(ctx context.Context, host, port string) tlscheck.Result
	Now        func() time.Time
}

// DefaultOutboundEnv memakai jaringan sungguhan.
func DefaultOutboundEnv(r run.Runner, procRoot string) OutboundEnv {
	return OutboundEnv{
		Runner: r,
		Info:   func(ctx context.Context) (Info, error) { return Read(ctx, r, procRoot) },
		Resolve: func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		},
		ResolveVia: func(ctx context.Context, server, host string) ([]string, error) {
			res := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
			}}
			return res.LookupHost(ctx, host)
		},
		Dial: func(ctx context.Context, addr string) error {
			c, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", addr)
			if err == nil {
				c.Close()
			}
			return err
		},
		HTTP: func(ctx context.Context, rawURL string) (int, time.Duration, error) {
			client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
			if err != nil {
				return 0, 0, err
			}
			start := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				return 0, time.Since(start), err
			}
			resp.Body.Close()
			return resp.StatusCode, time.Since(start), nil
		},
		TLS: func(ctx context.Context, host, port string) tlscheck.Result {
			return tlscheck.CheckHost(ctx, host, port, time.Now())
		},
		Now: time.Now,
	}
}

// DescribeDialError menerjemahkan kegagalan koneksi TCP.
func DescribeDialError(err error) (summary, explain string) {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "koneksi ditolak", "Host bisa dijangkau, tetapi tidak ada aplikasi yang menunggu di port itu (atau firewall-nya menolak dengan tegas). Periksa aplikasi di server tujuan sudah berjalan dan port-nya benar."
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "tidak ada rute", "Server ini tidak tahu jalan ke host tujuan. Periksa gateway default dan koneksi internet."
	case errors.As(err, &ne) && ne.Timeout():
		return "tidak ada jawaban (timeout)", "Paket tidak dibalas sama sekali. Biasanya diblok firewall (di tujuan, di server ini, atau security group cloud), atau host sedang mati."
	}
	return "gagal", err.Error()
}

// OutboundSteps menyusun pemeriksaan "kenapa tidak bisa konek ke X" dari server ini.
func OutboundSteps(env OutboundEnv, t Target) []Step {
	var info Info
	hostPort := net.JoinHostPort(t.Host, t.Port)
	steps := []Step{
		{
			Title: "Interface jaringan & gateway", Equivalent: "ip addr; ip route",
			Run: func(ctx context.Context) StepResult {
				var err error
				if info, err = env.Info(ctx); err != nil {
					return StepResult{Status: StatusFail, Summary: "tidak bisa membaca konfigurasi jaringan", Explain: err.Error()}
				}
				def, ok := DefaultRoute(info.Routes)
				if !ok {
					return StepResult{Status: StatusFail, Summary: "tidak ada gateway default", Explain: "Server tidak punya jalan keluar ke internet. Periksa konfigurasi netplan / DHCP, atau kabel/jaringan VM."}
				}
				for _, i := range info.Interfaces {
					if i.Name == def.Dev {
						if !i.Up() {
							return StepResult{Status: StatusFail, Summary: "interface " + i.Name + " mati (" + i.State + ")", Explain: "Interface yang dipakai ke internet tidak terhubung."}
						}
						return StepResult{Status: StatusOK, Summary: fmt.Sprintf("%s aktif, IP %s, gateway %s", i.Name, orDash(def.PrefSrc, i.GlobalIPv4()), def.Gateway), Explain: ""}
					}
				}
				return StepResult{Status: StatusWarn, Summary: "gateway " + def.Gateway + " lewat " + def.Dev, Explain: ""}
			},
		},
		{
			Title: "Ping gateway", Equivalent: "ping -c 2 GATEWAY",
			Run: func(ctx context.Context) StepResult {
				def, ok := DefaultRoute(info.Routes)
				if !ok || def.Gateway == "" {
					return StepResult{Status: StatusSkip, Summary: "gateway tidak diketahui"}
				}
				if err := ping(ctx, env.Runner, def.Gateway); err != nil {
					return StepResult{Status: StatusWarn, Summary: "gateway " + def.Gateway + " tidak membalas ping", Explain: "Sebagian router/cloud memang memblok ping. Bila langkah berikutnya juga gagal, masalahnya ada di jaringan lokal/penyedia."}
				}
				return StepResult{Status: StatusOK, Summary: "gateway " + def.Gateway + " membalas", Explain: ""}
			},
		},
		{
			Title: "Resolusi DNS", Equivalent: "resolvectl query " + t.Host + "  (pembanding: dig +short " + t.Host + " @1.1.1.1)",
			Run: func(ctx context.Context) StepResult {
				if t.isIP() {
					return StepResult{Status: StatusSkip, Summary: "tujuan berupa IP, tidak perlu DNS"}
				}
				ips, err := env.Resolve(ctx, t.Host)
				if err == nil && len(ips) > 0 {
					return StepResult{Status: StatusOK, Summary: t.Host + " → " + strings.Join(firstN(ips, 3), ", "), Explain: ""}
				}
				if pub, perr := env.ResolveVia(ctx, "1.1.1.1", t.Host); perr == nil && len(pub) > 0 {
					return StepResult{Status: StatusFail, Summary: "DNS server ini gagal, tetapi DNS publik 1.1.1.1 berhasil (" + pub[0] + ")",
						Explain: "Resolver lokal bermasalah. Cek dengan resolvectl status; coba sudo systemctl restart systemd-resolved, atau ganti DNS di netplan."}
				}
				return StepResult{Status: StatusFail, Summary: "nama " + t.Host + " tidak ditemukan", Explain: "Domain tidak ada, salah ketik, atau belum diarahkan (record A/AAAA belum dibuat). Bila DNS publik juga gagal, periksa juga koneksi internet."}
			},
		},
		{
			Title: "Ping tujuan", Equivalent: "ping -c 2 " + t.Host,
			Run: func(ctx context.Context) StepResult {
				if err := ping(ctx, env.Runner, t.Host); err != nil {
					return StepResult{Status: StatusWarn, Summary: t.Host + " tidak membalas ping", Explain: "Bukan vonis: banyak server & cloud memblok ICMP. Langkah berikutnya (port TCP) lebih menentukan."}
				}
				return StepResult{Status: StatusOK, Summary: t.Host + " membalas ping", Explain: ""}
			},
		},
		{
			Title: "Koneksi ke port " + t.Port, Equivalent: "nc -zv -w 5 " + t.Host + " " + t.Port,
			Run: func(ctx context.Context) StepResult {
				if err := env.Dial(ctx, hostPort); err != nil {
					summary, explain := DescribeDialError(err)
					return StepResult{Status: StatusFail, Summary: hostPort + ": " + summary, Explain: explain}
				}
				return StepResult{Status: StatusOK, Summary: hostPort + " terbuka", Explain: ""}
			},
		},
	}
	if t.Scheme == "http" || t.Scheme == "https" {
		u := t.Scheme + "://" + hostPort + "/"
		steps = append(steps, Step{
			Title: "Permintaan HTTP", Equivalent: "curl -sS -o /dev/null -w '%{http_code} %{time_total}' " + u,
			Run: func(ctx context.Context) StepResult {
				code, elapsed, err := env.HTTP(ctx, u)
				if err != nil {
					return StepResult{Status: StatusFail, Summary: "HTTP gagal", Explain: err.Error()}
				}
				summary := fmt.Sprintf("HTTP %d dalam %s", code, elapsed.Round(time.Millisecond))
				switch {
				case code >= 500:
					return StepResult{Status: StatusFail, Summary: summary, Explain: "Server tujuan error. 502/504 biasanya berarti aplikasi di belakang reverse proxy mati atau lambat."}
				case code >= 400:
					return StepResult{Status: StatusWarn, Summary: summary, Explain: "Server menjawab, tetapi menolak permintaan (mis. 403 dilarang, 404 halaman tidak ada). Koneksi jaringannya sendiri sehat."}
				}
				return StepResult{Status: StatusOK, Summary: summary, Explain: ""}
			},
		})
	}
	if t.Scheme == "https" {
		steps = append(steps, Step{
			Title: "Sertifikat TLS", Equivalent: "openssl s_client -connect " + hostPort + " -servername " + t.Host + " </dev/null | openssl x509 -noout -dates -subject -issuer",
			Run: func(ctx context.Context) StepResult {
				res := env.TLS(ctx, t.Host, t.Port)
				if res.Err != nil {
					return StepResult{Status: StatusFail, Summary: "handshake TLS gagal", Explain: res.Err.Error()}
				}
				summary := fmt.Sprintf("diterbitkan %s, berlaku sampai %s", res.Cert.Issuer, res.Cert.NotAfter.Format("2006-01-02"))
				if res.Problem != "" {
					return StepResult{Status: StatusFail, Summary: res.Problem, Explain: summary}
				}
				if w := res.Warning(env.Now()); w != "" {
					return StepResult{Status: StatusWarn, Summary: w, Explain: summary}
				}
				return StepResult{Status: StatusOK, Summary: summary, Explain: ""}
			},
		})
	}
	return steps
}

func ping(ctx context.Context, r run.Runner, host string) error {
	_, _, err := r.Capture(ctx, run.Command{Argv: []string{"ping", "-c", "2", "-W", "2", host}})
	return err
}

func orDash(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return "—"
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// InboundEnv adalah ketergantungan pemeriksaan "service tidak bisa diakses dari luar".
type InboundEnv struct {
	ProcRoot   string
	UFWConf    string // /etc/ufw/ufw.conf
	Info       func(ctx context.Context) (Info, error)
	Dial       func(ctx context.Context, addr string) error
	ReadFile   func(path string) ([]byte, error)
	ListenRead func(procRoot string) (ports.Snapshot, error)
}

// DefaultInboundEnv memakai sistem sungguhan.
func DefaultInboundEnv(r run.Runner, procRoot string) InboundEnv {
	out := DefaultOutboundEnv(r, procRoot)
	return InboundEnv{
		ProcRoot: procRoot, UFWConf: "/etc/ufw/ufw.conf",
		Info: out.Info, Dial: out.Dial, ReadFile: os.ReadFile, ListenRead: ports.ReadListeners,
	}
}

// UFWEnabled membaca ENABLED=yes dari ufw.conf (bisa dibaca tanpa root).
func UFWEnabled(conf []byte) bool {
	for _, line := range strings.Split(string(conf), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && k == "ENABLED" {
			return strings.Trim(v, `"' `) == "yes"
		}
	}
	return false
}

// InboundSteps menyusun pemeriksaan untuk port yang tidak bisa diakses dari luar.
func InboundSteps(env InboundEnv, port int) []Step {
	var listeners []ports.Listener
	p := strconv.Itoa(port)
	return []Step{
		{
			Title: "Ada aplikasi yang listening di port " + p + "?", Equivalent: fmt.Sprintf("sudo ss -tlpn 'sport = :%d'", port),
			Run: func(ctx context.Context) StepResult {
				snap, err := env.ListenRead(env.ProcRoot)
				if err != nil {
					return StepResult{Status: StatusFail, Summary: "tidak bisa membaca port", Explain: err.Error()}
				}
				for _, l := range snap.Listeners {
					if int(l.Local.Port()) == port && l.Proto == ports.TCP {
						listeners = append(listeners, l)
					}
				}
				if len(listeners) == 0 {
					return StepResult{Status: StatusFail, Summary: "tidak ada aplikasi di port " + p, Explain: "Aplikasinya belum berjalan, crash, atau memakai port lain. Periksa di modul Service/Log, atau lihat daftar port di modul Ports & Proses."}
				}
				var addrs []string
				for _, l := range listeners {
					addrs = append(addrs, l.Local.String())
				}
				return StepResult{Status: StatusOK, Summary: "listening di " + strings.Join(addrs, ", "), Explain: ""}
			},
		},
		{
			Title: "Alamat bind bisa diakses dari luar?", Equivalent: "ss -tln",
			Run: func(ctx context.Context) StepResult {
				for _, l := range listeners {
					if l.Scope() != ports.ScopeLoopback {
						return StepResult{Status: StatusOK, Summary: "bind ke " + l.Local.Addr().String() + " (bukan hanya localhost)", Explain: ""}
					}
				}
				return StepResult{Status: StatusFail, Summary: "aplikasi hanya mendengarkan 127.0.0.1/::1",
					Explain: "Ini penyebab paling umum! Aplikasi hanya menerima koneksi dari server ini sendiri. Ubah konfigurasinya supaya bind ke 0.0.0.0 (mis. --host 0.0.0.0, listen 0.0.0.0:" + p + ", HOST=0.0.0.0), atau pasang reverse proxy nginx di depannya (modul Web & TLS).", Next: "web"}
			},
		},
		{
			Title: "Firewall ufw mengizinkan port " + p + "?", Equivalent: "sudo ufw status numbered",
			Run: func(ctx context.Context) StepResult {
				conf, err := env.ReadFile(env.UFWConf)
				if err != nil {
					return StepResult{Status: StatusSkip, Summary: "ufw tidak terinstall"}
				}
				if !UFWEnabled(conf) {
					return StepResult{Status: StatusOK, Summary: "ufw tidak aktif (tidak memblok apa pun)", Explain: ""}
				}
				return StepResult{Status: StatusWarn, Summary: "ufw aktif — pastikan ada aturan allow " + p + "/tcp",
					Explain: "Daftar aturan butuh root untuk dibaca. Buka modul Firewall untuk memeriksa dan menambahkan: sudo ufw allow " + p + "/tcp", Next: "firewall"}
			},
		},
		{
			Title: "Bisa diakses lewat IP server sendiri?", Equivalent: "nc -zv -w 5 IP_SERVER " + p,
			Run: func(ctx context.Context) StepResult {
				info, err := env.Info(ctx)
				if err != nil {
					return StepResult{Status: StatusSkip, Summary: "IP server tidak diketahui"}
				}
				ip := ""
				if def, ok := DefaultRoute(info.Routes); ok {
					ip = def.PrefSrc
				}
				if ip == "" {
					return StepResult{Status: StatusSkip, Summary: "IP server tidak diketahui"}
				}
				addr := net.JoinHostPort(ip, p)
				if err := env.Dial(ctx, addr); err != nil {
					summary, explain := DescribeDialError(err)
					return StepResult{Status: StatusFail, Summary: addr + ": " + summary, Explain: explain}
				}
				return StepResult{Status: StatusOK, Summary: addr + " bisa dihubungi dari server ini", Explain: ""}
			},
		},
		{
			Title: "Firewall di luar server", Equivalent: "(periksa dashboard penyedia cloud / router)",
			Run: func(ctx context.Context) StepResult {
				return StepResult{Status: StatusWarn, Summary: "tidak bisa diperiksa dari dalam server",
					Explain: "Bila semua langkah di atas lolos tetapi dari luar tetap tidak bisa, periksa security group / firewall penyedia cloud (AWS, GCP, DigitalOcean, dll.) atau port forwarding router: izinkan TCP " + p + " masuk."}
			},
		},
	}
}
