package web

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestParseSite(t *testing.T) {
	s := ParseSite(`server {
    listen 80;
    server_name contoh.com www.contoh.com;  # komentar; server_name palsu.com;
    return 301 https://$host$request_uri;
}
server {
    listen 443 ssl http2;
    server_name contoh.com;
    ssl_certificate /etc/letsencrypt/live/contoh.com/fullchain.pem;
    location / { proxy_pass http://127.0.0.1:3000; }
    location /static { root /var/www/app; }
}`)
	if !reflect.DeepEqual(s.ServerNames, []string{"contoh.com", "www.contoh.com"}) || !s.SSL || s.CertPath != "/etc/letsencrypt/live/contoh.com/fullchain.pem" ||
		!reflect.DeepEqual(s.ProxyPass, []string{"http://127.0.0.1:3000"}) || s.Root != "/var/www/app" || len(s.Listen) != 2 {
		t.Errorf("%+v", s)
	}
}

func TestReadSitesDanRead(t *testing.T) {
	dir := t.TempDir()
	avail, enabled := filepath.Join(dir, "sites-available"), filepath.Join(dir, "sites-enabled")
	os.MkdirAll(avail, 0o755)
	os.MkdirAll(enabled, 0o755)
	os.WriteFile(filepath.Join(avail, "default"), []byte("server { listen 80 default_server; server_name _; root /var/www/html; }"), 0o644)
	os.WriteFile(filepath.Join(avail, "app.contoh.com"), []byte(ProxyConfig(ProxySpec{Domain: "app.contoh.com", Port: "3000"})), 0o644)
	os.Symlink(filepath.Join(avail, "app.contoh.com"), filepath.Join(enabled, "app.contoh.com"))
	bin := filepath.Join(dir, "nginx")
	os.WriteFile(bin, nil, 0o755)
	p := Paths{NginxBin: bin, ApacheBin: filepath.Join(dir, "x"), CaddyBin: filepath.Join(dir, "y"), SitesAvailable: avail, SitesEnabled: enabled}
	fake := &run.Fake{Responses: map[string]run.FakeResponse{"systemctl is-active nginx": {Stdout: "active\n"}}}
	st := Read(context.Background(), fake, p)
	if !st.Nginx().Installed || !st.Nginx().Active || st.Certbot != "" {
		t.Errorf("nginx: %+v", st)
	}
	if len(st.Sites) != 2 || st.Sites[0].Name != "app.contoh.com" || !st.Sites[0].Enabled || !st.Sites[0].Managed || st.Sites[1].Enabled {
		t.Errorf("sites: %+v", st.Sites)
	}
}

func TestProxyConfigDanPlan(t *testing.T) {
	cfg := ProxyConfig(ProxySpec{Domain: "app.contoh.com", Port: "3000", WebSocket: true})
	for _, s := range []string{"server_name app.contoh.com;", "proxy_pass http://127.0.0.1:3000;", "proxy_set_header X-Forwarded-Proto $scheme;", `Connection "upgrade"`, "client_max_body_size 20m;"} {
		if !strings.Contains(cfg, s) {
			t.Errorf("config tidak memuat %q:\n%s", s, cfg)
		}
	}
	if strings.Contains(ProxyConfig(ProxySpec{Domain: "a.com", Port: "80"}), "Upgrade") {
		t.Error("tanpa websocket tidak boleh ada header Upgrade")
	}
	p := ProxyPlan(ProxySpec{Domain: "app.contoh.com", Port: "3000"}, DefaultPaths, true)
	var cmds []string
	for _, s := range p.Sequence() {
		cmds = append(cmds, s.Command.Preview(false))
	}
	want := []string{
		"sudo ufw allow 'Nginx Full'",
		"sudo install -m 0644 /dev/stdin /etc/nginx/sites-available/app.contoh.com",
		"sudo ln -sfn /etc/nginx/sites-available/app.contoh.com /etc/nginx/sites-enabled/app.contoh.com",
		"sudo nginx -t",
		"sudo systemctl reload nginx",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("plan:\n%s", strings.Join(cmds, "\n"))
	}
	if c := CertbotPlan("/usr/bin/certbot", "contoh.com", true).Steps[0]; c.Preview(false) != "sudo /usr/bin/certbot --nginx -d contoh.com -d www.contoh.com" || !c.Interactive {
		t.Errorf("certbot: %+v", c)
	}
}

func TestValidateDomain(t *testing.T) {
	for _, ok := range []string{"contoh.com", "app.contoh.co.id", "a-b.example.org"} {
		if ValidateDomain(ok) != nil {
			t.Errorf("%s harus valid", ok)
		}
	}
	for _, bad := range []string{"https://contoh.com", "contoh", "-a.com", "contoh.com/app", "a b.com", ""} {
		if ValidateDomain(bad) == nil {
			t.Errorf("%q harus ditolak", bad)
		}
	}
	if !strings.Contains(ExplainHTTPStatus(502), "Bad Gateway") {
		t.Error("penjelasan 502")
	}
}
