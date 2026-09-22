package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"python -m app", []string{"python", "-m", "app"}},
		{`sh -c "echo halo dunia"`, []string{"sh", "-c", "echo halo dunia"}},
		{"  spasi   berlebih  ", []string{"spasi", "berlebih"}},
		{`node 'server one.js'`, []string{"node", "server one.js"}},
		// Karakter shell tetap menjadi teks biasa: ubt tidak pernah menjalankan shell di sini.
		{"echo $(id)", []string{"echo", "$(id)"}},
		{"echo a|b", []string{"echo", "a|b"}},
	}
	for _, tc := range cases {
		got, err := SplitCommand(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q = %q, ingin %q", tc.in, got, tc.want)
		}
	}
	if _, err := SplitCommand(`echo "belum ditutup`); err == nil {
		t.Error("kutip tidak ditutup harus error")
	}
}

func TestRunSpecArgs(t *testing.T) {
	s := RunSpec{
		Name: "app", Image: "nginx:alpine", Detach: true,
		Ports: []string{"127.0.0.1:8080:80"}, Volumes: []string{"data:/var/www"},
		Env: []string{"TZ=Asia/Jakarta"}, Workdir: "/app", Command: []string{"nginx", "-g", "daemon off;"},
		User: "1000:1000", Network: "app-net", Restart: "unless-stopped", Memory: "512m", CPUs: "1.5", HealthCmd: "curl -f http://localhost/",
	}
	got := run.JoinShell(s.Args())
	for _, want := range []string{"run -d --name app", "-p 127.0.0.1:8080:80", "-v data:/var/www", "-e TZ=Asia/Jakarta",
		"-w /app", "--user 1000:1000", "--network app-net", "--restart unless-stopped", "-m 512m", "--cpus 1.5",
		"--health-cmd", "nginx -g 'daemon off;'"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv tidak memuat %q:\n%s", want, got)
		}
	}
	// Setiap bagian command harus tetap satu argumen utuh.
	argv := s.Args()
	if argv[len(argv)-1] != "daemon off;" {
		t.Errorf("argumen terakhir terpecah: %q", argv[len(argv)-1])
	}
}

func TestRunSpecModeInteraktifDanSekaliJalan(t *testing.T) {
	once := RunSpec{Name: "coba", Image: "alpine", Interactive: true, AutoRemove: true}
	got := run.JoinShell(once.Args())
	if !strings.Contains(got, "run -it --rm --name coba alpine") {
		t.Errorf("argv = %s", got)
	}
	if strings.Contains(got, "-d") {
		t.Error("mode interaktif tidak boleh memakai -d")
	}
}

func TestRunSpecValidate(t *testing.T) {
	base := RunSpec{Name: "app", Image: "nginx"}
	if err := base.Validate(); err != nil {
		t.Errorf("spec dasar ditolak: %v", err)
	}
	bad := base
	bad.AutoRemove, bad.Restart = true, "always"
	if err := bad.Validate(); err == nil {
		t.Error("--rm + restart harus ditolak")
	}
	bad = base
	bad.Detach, bad.Interactive = true, true
	if err := bad.Validate(); err == nil {
		t.Error("detach + interaktif harus ditolak")
	}
	bad = base
	bad.Ports = []string{"delapanribu:80"}
	if err := bad.Validate(); err == nil {
		t.Error("port bukan angka harus ditolak")
	}
	bad = base
	bad.Volumes = []string{"data"}
	if err := bad.Validate(); err == nil {
		t.Error("volume tanpa tujuan harus ditolak")
	}
}

func TestRunPlanMencatatResepSetelahContainerJalan(t *testing.T) {
	c := Client{}
	s := RunSpec{Name: "db", Image: "postgres:17", Detach: true, Env: []string{"POSTGRES_PASSWORD=rahasia"}}
	p := c.RunPlan(s, "/home/budi/.config/ubt/containers.json", Recipes{})
	if len(p.Steps) != 2 {
		t.Fatalf("langkah = %d, ingin 2 (jalankan lalu catat)", len(p.Steps))
	}
	if !strings.HasPrefix(p.Steps[0].Preview(true), "docker run -d") {
		t.Errorf("langkah 1 = %q", p.Steps[0].Preview(true))
	}
	catat := p.Steps[1]
	if !catat.Sensitive {
		t.Error("resep berisi environment harus ditandai sensitif supaya tidak masuk riwayat")
	}
	if !strings.Contains(catat.Stdin, `"name": "db"`) {
		t.Errorf("isi resep:\n%s", catat.Stdin)
	}
	// Tanpa path resep (atau mode interaktif), tidak ada langkah pencatatan.
	if p := c.RunPlan(s, "", Recipes{}); len(p.Steps) != 1 {
		t.Errorf("tanpa path: %d langkah", len(p.Steps))
	}
	s.Detach, s.Interactive = false, true
	if p := c.RunPlan(s, "/tmp/x.json", Recipes{}); len(p.Steps) != 1 {
		t.Errorf("mode interaktif tidak boleh mencatat resep: %d langkah", len(p.Steps))
	}
}

func TestRecreatePlanUrutannya(t *testing.T) {
	c := Client{}
	p := c.RecreatePlan(RunSpec{Name: "app", Image: "app:2", Detach: true}, true, true)
	var got []string
	for _, s := range p.Steps {
		got = append(got, strings.Fields(s.Preview(true))[1]) // subcommand docker
	}
	want := []string{"pull", "stop", "rm", "run"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("urutan langkah = %v, ingin %v", got, want)
	}
	// Container yang belum ada: langsung jalankan.
	p = c.RecreatePlan(RunSpec{Name: "app", Image: "app:2", Detach: true}, false, false)
	if len(p.Steps) != 1 {
		t.Errorf("container baru: %d langkah", len(p.Steps))
	}
}

func TestRecipesRoundTrip(t *testing.T) {
	rs := Recipes{}.Upsert(RunSpec{Name: "b", Image: "b"}).Upsert(RunSpec{Name: "a", Image: "a"})
	if rs.Items[0].Name != "a" {
		t.Errorf("urutan: %+v", rs.Items)
	}
	if _, ok := rs.Find("b"); !ok {
		t.Error("Find gagal")
	}
	if rs = rs.Remove("a"); len(rs.Items) != 1 {
		t.Errorf("Remove: %+v", rs.Items)
	}
	if !strings.Contains(RecipesJSON(Recipes{}), `"containers": []`) {
		t.Errorf("resep kosong: %s", RecipesJSON(Recipes{}))
	}
}

func TestValidasiSettingLanjutan(t *testing.T) {
	ok := func(err error) bool { return err == nil }
	if !ok(ValidMemory("")) || !ok(ValidMemory("512m")) || !ok(ValidMemory("2g")) {
		t.Error("batas memori sah ditolak")
	}
	if ValidMemory("512 MB") == nil || ValidMemory("banyak") == nil {
		t.Error("batas memori ngawur harus ditolak")
	}
	if !ok(ValidCPUs("0.5")) || ValidCPUs("-1") == nil || ValidCPUs("dua") == nil {
		t.Error("validasi cpus salah")
	}
	if !ok(ValidUserSpec("1000:1000")) || !ok(ValidUserSpec("app")) || ValidUserSpec("root;rm -rf /") == nil {
		t.Error("validasi user salah")
	}
	if !ok(ValidWorkdir("/app")) || ValidWorkdir("app") == nil {
		t.Error("workdir harus absolut")
	}
}

func TestSpecFromContainerMembacaPort(t *testing.T) {
	ct := Container{Names: "web", Image: "nginx", Ports: "0.0.0.0:8080->80/tcp, [::]:8080->80/tcp, 127.0.0.1:9000->9000/tcp"}
	s := FromContainer(ct)
	want := []string{"8080:80", "127.0.0.1:9000:9000"}
	if !reflect.DeepEqual(s.Ports, want) {
		t.Errorf("port = %q, ingin %q", s.Ports, want)
	}
}
