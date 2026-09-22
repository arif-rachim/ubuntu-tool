package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

const psOut = `{"ID":"73523e85068d","Image":"postgres:17","Labels":"com.docker.compose.project=shop,x=y","Names":"shop_db","Ports":"0.0.0.0:5432->5432/tcp","State":"running","Status":"Up 2 hours"}
rusak
{"ID":"aaaaaaaaaaaa","Image":"alpine","Labels":"","Names":"coba","State":"exited","Status":"Exited (0) 3 days ago"}
{"ID":"bbbbbbbbbbbb","Image":"nginx","Labels":"","Names":"api","State":"running","Status":"Up 1 minute"}
`

func TestParse(t *testing.T) {
	cs := ParseContainers(psOut)
	if len(cs) != 3 || cs[0].Name() != "api" || cs[1].Name() != "shop_db" || cs[2].Running() {
		t.Fatalf("%+v", cs)
	}
	if cs[1].ComposeProject() != "shop" || cs[0].ComposeProject() != "" {
		t.Error("label compose")
	}
	imgs := ParseImages(`{"ID":"9a079ac1c94d","Repository":"python","Tag":"3.12-slim","Size":"120MB"}
{"ID":"1234","Repository":"<none>","Tag":"<none>","Size":"1GB"}`)
	if imgs[0].Ref() != "python:3.12-slim" || imgs[1].Ref() != "1234" || !imgs[1].Dangling() {
		t.Errorf("%+v", imgs)
	}
	for in, want := range map[string]Availability{
		"permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock": NoPermission,
		"Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?":    DaemonDown,
		"error aneh": Unknown,
	} {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %v", in, got)
		}
	}
}

func TestRead(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "docker")
	os.WriteFile(bin, nil, 0o755)
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"docker info --format '{{json .ServerVersion}}'": {Stdout: "\"29.1.3\"\n"},
		"docker ps -a --no-trunc --format '{{json .}}'":  {Stdout: psOut},
		"docker images --format '{{json .}}'":            {Stdout: `{"ID":"1","Repository":"alpine","Tag":"latest"}`},
		"docker system df --format '{{json .}}'":         {Stdout: `{"Type":"Images","TotalCount":"1","Size":"8MB","Reclaimable":"0B"}`},
		"docker compose version":                         {Err: errors.New("exit 1")},
	}}
	st := Client{Bin: bin}.Read(context.Background(), fake)
	if st.Avail != Ready || st.Version != "29.1.3" || len(st.Containers) != 3 || len(st.Images) != 1 || len(st.Disk) != 1 || st.Compose {
		t.Errorf("%+v", st)
	}
	down := &run.Fake{Responses: map[string]run.FakeResponse{
		"docker info --format '{{json .ServerVersion}}'": {Stderr: "permission denied while trying to connect to the Docker daemon socket", Err: errors.New("exit 1")},
	}}
	if st := (Client{Bin: bin}).Read(context.Background(), down); st.Avail != NoPermission {
		t.Errorf("%+v", st)
	}
	if st := (Client{Bin: filepath.Join(t.TempDir(), "x")}).Read(context.Background(), down); st.Avail != NotInstalled {
		t.Error("binary tidak ada")
	}
}

func TestPlans(t *testing.T) {
	c := Client{}
	cs := ParseContainers(psOut)
	preview := func(p run.Plan) string { return p.Steps[0].Preview(false) }
	if got := preview(c.ExecShellPlan(cs[0])); got != `docker exec -it api sh -c 'if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi'` || !c.ExecShellPlan(cs[0]).Steps[0].Interactive {
		t.Errorf("exec: %s", got)
	}
	if p := c.RemovePlan(cs[0]); preview(p) != "docker rm -f api" || p.Steps[0].Risk != risk.Dangerous {
		t.Errorf("rm: %s", preview(p))
	}
	if p := c.RemovePlan(cs[2]); preview(p) != "docker rm coba" || p.Steps[0].Risk != risk.Caution {
		t.Errorf("rm berhenti: %s", preview(p))
	}
	if !strings.Contains(c.StopPlan(cs[1]).Steps[0].Safer, "compose") {
		t.Error("stop container compose harus menyarankan docker compose")
	}
	if got := preview(Client{Sudo: true}.PythonRunPlan("python:3.12-slim", "/home/u/proyek saya", "app.main", 1000, 1000)); got != "sudo docker run --rm -it -v '/home/u/proyek saya:/app' -w /app --user 1000:1000 -e HOME=/tmp python:3.12-slim python -m app.main" {
		t.Errorf("python: %s", got)
	}
	if got := preview(c.RunShellPlan("alpine", true, "eksperimen")); !strings.HasPrefix(got, "docker run -it --name eksperimen alpine sh -c") {
		t.Errorf("run: %s", got)
	}
	if p := c.PrunePlan(true, true); preview(p) != "docker system prune -f -a --volumes" || p.Steps[0].Risk != risk.Dangerous {
		t.Errorf("prune: %s", preview(p))
	}
}

func TestValidasi(t *testing.T) {
	for _, ok := range []string{"nginx", "python:3.12-slim", "ghcr.io/org/app:1.0", "localhost:5000/app", "public.ecr.aws/supabase/postgres:17.6.1.167"} {
		if err := ValidImage(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Nginx", "a b", "app:", "-app"} {
		if ValidImage(bad) == nil {
			t.Errorf("%q harus ditolak", bad)
		}
	}
	if ValidPortMapping("8080:80") != nil || ValidPortMapping("127.0.0.1:8080:80/udp") != nil || ValidPortMapping("80") == nil || ValidPortMapping("8080:99999") == nil {
		t.Error("port mapping")
	}
	if ValidVolume("./data:/data") != nil || ValidVolume("db:/var/lib/x:ro") != nil || ValidVolume("data") == nil || ValidVolume("./a:b") == nil {
		t.Error("volume")
	}
	if ValidModule("app.main") != nil || ValidModule("app.py") == nil || ValidEnv("A=1") != nil || ValidEnv("=1") == nil {
		t.Error("modul/env")
	}
}

func TestFiles(t *testing.T) {
	compose := ComposeFile(ComposeSpec{Service: "db", Image: "postgres:17", Ports: []string{"127.0.0.1:5432:5432"},
		Volumes: []string{"dbdata:/var/lib/postgresql/data", "./init:/docker-entrypoint-initdb.d"}, Env: []string{`POSTGRES_PASSWORD=ra"hasia`}})
	want := `# Dibuat oleh ubt. Jalankan: docker compose up -d
services:
  db:
    image: postgres:17
    restart: unless-stopped
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - "dbdata:/var/lib/postgresql/data"
      - "./init:/docker-entrypoint-initdb.d"
    environment:
      - "POSTGRES_PASSWORD=ra\"hasia"

volumes:
  dbdata:
`
	if compose != want {
		t.Errorf("compose:\n%s", compose)
	}
	if b := ComposeFile(ComposeSpec{Service: "web", Build: true}); !strings.Contains(b, "build: .") || strings.Contains(b, "image:") || strings.Contains(b, "volumes:") {
		t.Errorf("build:\n%s", b)
	}
	spec := DockerfileKinds["python"]
	spec.Deps = true
	py := Dockerfile(spec)
	if strings.Contains(Dockerfile(DockerfileKinds["python"]), "requirements") {
		t.Error("tanpa requirements.txt tidak boleh ada COPY requirements.txt")
	}
	for _, s := range []string{"FROM python:3.12-slim", "pip install --no-cache-dir -r requirements.txt", "EXPOSE 8000", `CMD ["python", "-m", "app"]`} {
		if !strings.Contains(py, s) {
			t.Errorf("dockerfile tidak memuat %q:\n%s", s, py)
		}
	}
	if st := Dockerfile(DockerfileKinds["static"]); strings.Contains(st, "CMD") || !strings.Contains(st, "/usr/share/nginx/html") {
		t.Errorf("static:\n%s", st)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("lama"), 0o644)
	files := map[string]string{"Dockerfile": py, ".dockerignore": DockerIgnore}
	order := []string{"Dockerfile", ".dockerignore"}
	if _, existing, err := WriteFilesPlan("x", dir, files, order, false); !errors.Is(err, ErrExists) || len(existing) != 1 {
		t.Errorf("harus menolak menimpa: %v %v", existing, err)
	}
	p, _, err := WriteFilesPlan("x", dir, files, order, true)
	if err != nil || len(p.Steps) != 2 || p.Steps[0].Risk != risk.Dangerous || !strings.HasPrefix(p.Steps[0].Title, "TIMPA") || p.Steps[1].Risk != risk.Safe || p.Steps[0].NeedsRoot {
		t.Errorf("%+v %v", p, err)
	}
}

func TestPublishedPorts(t *testing.T) {
	cs := ParseContainers(`{"ID":"abc123def456","Names":"web","Image":"nginx","State":"running","Ports":"0.0.0.0:8080->80/tcp, [::]:8080->80/tcp"}
{"ID":"ffff0000","Names":"db","Image":"postgres:18","State":"running","Ports":"127.0.0.1:5432->5432/tcp"}
{"ID":"eeee1111","Names":"internal","Image":"redis","State":"running","Ports":"6379/tcp"}`)
	pub := PublishedPorts(cs)
	if len(pub) != 2 {
		t.Fatalf("port terpublikasi = %+v", pub)
	}
	// Baris IPv4 lebih informatif daripada [::] dan tidak boleh ditimpa.
	if got := pub["8080/tcp"]; got.Container != "web" || got.HostAddr != "0.0.0.0" || got.Target != "80" {
		t.Errorf("8080/tcp = %+v", got)
	}
	if got := pub["5432/tcp"]; got.Container != "db" || got.HostAddr != "127.0.0.1" {
		t.Errorf("5432/tcp = %+v", got)
	}
	// Port yang hanya dibuka di dalam container (tanpa publish) tidak dihitung.
	if _, ok := pub["6379/tcp"]; ok {
		t.Error("port tanpa publish ikut terdaftar")
	}
}

func TestNameByID(t *testing.T) {
	cs := ParseContainers(`{"ID":"abc123def456789","Names":"web","State":"running"}`)
	for _, id := range []string{"abc123def456789", "abc123def456", "abc123def456789abc"} {
		if got := NameByID(cs, id); got != "web" {
			t.Errorf("NameByID(%q) = %q", id, got)
		}
	}
	if got := NameByID(cs, "zzz999"); got != "" {
		t.Errorf("ID asing = %q", got)
	}
	if got := NameByID(cs, ""); got != "" {
		t.Errorf("ID kosong = %q", got)
	}
}
