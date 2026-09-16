package docker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// ComposeSpec adalah isi wizard docker-compose.yml untuk satu service.
type ComposeSpec struct {
	Service string
	Image   string
	Build   bool     // build dari Dockerfile di direktori yang sama, bukan image jadi
	Ports   []string // "8080:80"
	Volumes []string // "./data:/data" atau "nama:/var/lib/postgresql/data"
	Env     []string // "KEY=value"
	Restart string   // no, unless-stopped, always
}

// NamedVolumes adalah volume bernama (bukan path host) yang perlu dideklarasikan di tingkat atas.
func (s ComposeSpec) NamedVolumes() []string {
	var names []string
	for _, v := range s.Volumes {
		src, _, _ := strings.Cut(v, ":")
		if src != "" && !strings.ContainsAny(src[:1], "./~") {
			names = append(names, src)
		}
	}
	return names
}

var composeTmpl = template.Must(template.New("compose").Funcs(template.FuncMap{"q": yamlQuote}).Parse(`# Dibuat oleh ubt. Jalankan: docker compose up -d
services:
  {{.Service}}:
{{- if .Build}}
    build: .
{{- else}}
    image: {{.Image}}
{{- end}}
    restart: {{.Restart}}
{{- if .Ports}}
    ports:
{{- range .Ports}}
      - {{q .}}
{{- end}}
{{- end}}
{{- if .Volumes}}
    volumes:
{{- range .Volumes}}
      - {{q .}}
{{- end}}
{{- end}}
{{- if .Env}}
    environment:
{{- range .Env}}
      - {{q .}}
{{- end}}
{{- end}}
{{- with .NamedVolumes}}

volumes:
{{- range .}}
  {{.}}:
{{- end}}
{{- end}}
`))

// yamlQuote mengutip string YAML dengan tanda kutip ganda.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// ComposeFile merender docker-compose.yml.
func ComposeFile(s ComposeSpec) string {
	if s.Restart == "" {
		s.Restart = "unless-stopped"
	}
	var b strings.Builder
	_ = composeTmpl.Execute(&b, s)
	return b.String()
}

// DockerfileSpec adalah isi wizard Dockerfile.
type DockerfileSpec struct {
	Kind string // python, node, static
	Base string // image dasar
	Port string
	Cmd  string // command utama, mis. "python -m app" atau "node server.js"
	Deps bool   // requirements.txt / package.json ada: install dependensi di layer terpisah
}

// DockerfileKinds adalah jenis aplikasi yang didukung beserta image dasar dan command default.
var DockerfileKinds = map[string]DockerfileSpec{
	"python": {Kind: "python", Base: "python:3.12-slim", Port: "8000", Cmd: "python -m app"},
	"node":   {Kind: "node", Base: "node:22-alpine", Port: "3000", Cmd: "node server.js"},
	"static": {Kind: "static", Base: "nginx:alpine", Port: "80"},
}

var dockerfileTmpl = template.Must(template.New("dockerfile").Funcs(template.FuncMap{"exec": execForm}).Parse(`# Dibuat oleh ubt. Build: docker build -t nama-app .
FROM {{.Base}}
{{- if eq .Kind "python"}}
WORKDIR /app
{{- if .Deps}}
# Salin daftar dependensi dulu supaya layer install di-cache bila kode saja yang berubah.
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
{{- end}}
COPY . .
ENV PYTHONUNBUFFERED=1
{{- else if eq .Kind "node"}}
WORKDIR /app
{{- if .Deps}}
COPY package*.json ./
RUN if [ -f package-lock.json ]; then npm ci --omit=dev; else npm install --omit=dev; fi
{{- end}}
COPY . .
ENV NODE_ENV=production
{{- else}}
# Isi direktori ini disajikan nginx sebagai situs statis.
COPY . /usr/share/nginx/html
{{- end}}
EXPOSE {{.Port}}
{{- if .Cmd}}
CMD {{exec .Cmd}}
{{- end}}
`))

// execForm mengubah "python -m app" menjadi ["python", "-m", "app"] (exec form, sinyal stop diteruskan benar).
func execForm(cmd string) string {
	parts := strings.Fields(cmd)
	for i, p := range parts {
		parts[i] = strconv.Quote(p)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// Dockerfile merender Dockerfile.
func Dockerfile(s DockerfileSpec) string {
	if s.Kind == "static" {
		s.Cmd = ""
	}
	var b strings.Builder
	_ = dockerfileTmpl.Execute(&b, s)
	return b.String()
}

// DockerIgnore adalah .dockerignore dasar.
const DockerIgnore = `# Dibuat oleh ubt
.git
node_modules
__pycache__
*.pyc
.venv
.env
`

// ValidPortMapping memeriksa "8080:80", "127.0.0.1:8080:80", atau "8080:80/udp".
func ValidPortMapping(s string) error {
	s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(s), "/tcp"), "/udp")
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return errors.New("format: PORT_HOST:PORT_CONTAINER, mis. 8080:80")
	}
	for _, p := range parts[len(parts)-2:] {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("port %q tidak valid", p)
		}
	}
	return nil
}

// ValidVolume memeriksa "SUMBER:TUJUAN" dengan tujuan path absolut.
func ValidVolume(s string) error {
	src, dst, ok := strings.Cut(strings.TrimSpace(s), ":")
	dst, _, _ = strings.Cut(dst, ":") // opsi :ro
	if !ok || src == "" || !strings.HasPrefix(dst, "/") {
		return errors.New("format: SUMBER:/path/di/container, mis. ./data:/data atau dbdata:/var/lib/postgresql/data")
	}
	return nil
}

// ValidEnv memeriksa "KEY=value".
func ValidEnv(s string) error {
	k, _, ok := strings.Cut(strings.TrimSpace(s), "=")
	if !ok || k == "" || strings.ContainsAny(k, " \t") {
		return errors.New("format: NAMA=nilai")
	}
	return nil
}

// SplitList memecah input dipisah koma atau baris baru, membuang yang kosong.
func SplitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// ErrExists dikembalikan bila file tujuan sudah ada dan user tidak mengizinkan menimpa.
var ErrExists = errors.New("file sudah ada")

// WriteFilesPlan menulis file-file hasil wizard ke dir (tanpa root). File yang sudah ada TIDAK ditimpa
// kecuali overwrite true; bila ada yang sudah ada dan overwrite false, dikembalikan ErrExists beserta daftarnya.
func WriteFilesPlan(title, dir string, files map[string]string, order []string, overwrite bool) (run.Plan, []string, error) {
	var existing []string
	var steps []run.Command
	for _, name := range order {
		content, ok := files[name]
		if !ok {
			continue
		}
		path := filepath.Join(dir, name)
		exists := false
		if _, err := os.Stat(path); err == nil {
			exists = true
			existing = append(existing, path)
		}
		c := run.Command{
			Title: "Tulis " + path, Argv: []string{"install", "-m", "0644", "/dev/stdin", path},
			Stdin: content, StdinLabel: fmt.Sprintf("(%s, %d baris)", name, strings.Count(content, "\n")),
			Explain: []run.Line{
				{Token: "install -m 0644", Meaning: "tulis file dengan izin baca untuk semua"},
				{Token: "/dev/stdin", Meaning: "isi file diambil dari teks di bawah"},
				{Token: path, Meaning: "lokasi file tujuan"},
			},
			Risk: risk.Safe,
		}
		if exists {
			c.Title = "TIMPA " + path
			c.Risk = risk.Dangerous
			c.Effect = "File yang sudah ada diganti; isi lamanya hilang."
			c.Safer = "Batalkan dan pilih direktori lain, atau cadangkan dulu: cp " + path + " " + path + ".bak"
		}
		steps = append(steps, c)
	}
	if len(existing) > 0 && !overwrite {
		return run.Plan{}, existing, ErrExists
	}
	if len(existing) > 0 {
		title = "TIMPA: " + title
	}
	return run.Plan{Title: title, Steps: steps}, existing, nil
}
