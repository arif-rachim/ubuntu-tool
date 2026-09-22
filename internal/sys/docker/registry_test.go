package docker

import (
	"strings"
	"testing"
)

func TestRegistryRef(t *testing.T) {
	r := Registry{Name: "nexus", Host: "nexus.contoh.com:8082", Repo: "tim-a"}
	if got := r.Ref("app:1.0"); got != "nexus.contoh.com:8082/tim-a/app:1.0" {
		t.Errorf("Ref = %q", got)
	}
	// Nama yang sudah lengkap tidak diberi awalan dua kali.
	full := "nexus.contoh.com:8082/tim-a/app:1.0"
	if got := r.Ref(full); got != full {
		t.Errorf("Ref ganda = %q", got)
	}
	if got := (Registry{Host: "reg.lokal"}).Ref("app"); got != "reg.lokal/app" {
		t.Errorf("Ref tanpa repo = %q", got)
	}
}

func TestRegistryValidasi(t *testing.T) {
	for _, s := range []string{"nexus.contoh.com", "nexus.contoh.com:8082", "localhost:5000", "reg"} {
		if err := ValidRegistryHost(s); err != nil {
			t.Errorf("host %q ditolak: %v", s, err)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"https://nexus.contoh.com", "tanpa https://"},
		{"nexus.contoh.com/tim-a", "awalan repository"},
		{"", "wajib diisi"},
		{"nexus contoh", "tidak valid"},
	} {
		err := ValidRegistryHost(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("host %q: error %v, ingin memuat %q", tc.in, err, tc.want)
		}
	}
	if err := ValidRegistryRepo(""); err != nil {
		t.Errorf("repo kosong harus boleh: %v", err)
	}
	if err := ValidRegistryRepo("Tim A"); err == nil {
		t.Error("repo dengan spasi & huruf besar harus ditolak")
	}
	if err := ValidRegistryName("Nexus Kantor"); err == nil {
		t.Error("nama profil dengan spasi harus ditolak")
	}
}

func TestRegistriesUpsertRemove(t *testing.T) {
	rs := Registries{}.Upsert(Registry{Name: "b", Host: "b.lokal"}).Upsert(Registry{Name: "a", Host: "a.lokal"})
	if len(rs.Items) != 2 || rs.Items[0].Name != "a" {
		t.Fatalf("urutan salah: %+v", rs.Items)
	}
	rs = rs.Upsert(Registry{Name: "a", Host: "a2.lokal"})
	if len(rs.Items) != 2 {
		t.Fatalf("upsert nama sama harus mengganti: %+v", rs.Items)
	}
	if got, _ := rs.Find("a"); got.Host != "a2.lokal" {
		t.Errorf("host setelah upsert = %q", got.Host)
	}
	if rs = rs.Remove("a"); len(rs.Items) != 1 || rs.Items[0].Name != "b" {
		t.Errorf("remove: %+v", rs.Items)
	}
	// Berkas profil tidak boleh berisi password dalam bentuk apa pun.
	js := RegistriesJSON(rs)
	if strings.Contains(strings.ToLower(js), "password") || strings.Contains(strings.ToLower(js), "token") {
		t.Errorf("berkas profil memuat rahasia:\n%s", js)
	}
}

func TestInsecureRegistryMenjagaIsiLama(t *testing.T) {
	cfg := map[string]any{"log-driver": "json-file", "insecure-registries": []any{"lama.lokal"}}
	got := WithInsecureRegistry(cfg, "baru.lokal:8082")
	list, ok := got["insecure-registries"].([]any)
	if !ok || len(list) != 2 || list[0] != "lama.lokal" || list[1] != "baru.lokal:8082" {
		t.Fatalf("insecure-registries = %v", got["insecure-registries"])
	}
	if got["log-driver"] != "json-file" {
		t.Error("kunci lain hilang")
	}
	// Menambahkan host yang sama dua kali tidak menduplikasi.
	again := WithInsecureRegistry(got, "baru.lokal:8082")
	if list, _ := again["insecure-registries"].([]any); len(list) != 2 {
		t.Errorf("duplikat: %v", again["insecure-registries"])
	}
	if !strings.Contains(DaemonJSON(got), `"baru.lokal:8082"`) {
		t.Error("render daemon.json tidak memuat host baru")
	}
}

func TestRegistryHint(t *testing.T) {
	cases := map[string]string{
		"x509: certificate signed by unknown authority":      "sertifikat CA internal",
		"http: server gave HTTP response to HTTPS client":    "insecure-registries",
		"denied: requested access to the resource is denied": "login",
		"dial tcp: lookup nexus: no such host":               "DNS",
		"semua baik-baik saja":                               "",
	}
	for out, want := range cases {
		got := RegistryHint(out)
		if want == "" {
			if got != "" {
				t.Errorf("%q: seharusnya tanpa saran, dapat %q", out, got)
			}
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("%q: saran %q, ingin memuat %q", out, got, want)
		}
	}
}

func TestPushPlanMemberiTagDuluLaluUnggah(t *testing.T) {
	c := Client{}
	p := c.PushPlan("app:1.0", "nexus.lokal:8082/tim-a/app:1.0")
	if len(p.Steps) != 2 {
		t.Fatalf("langkah = %d, ingin 2", len(p.Steps))
	}
	if got := p.Steps[0].Preview(true); got != "docker tag app:1.0 nexus.lokal:8082/tim-a/app:1.0" {
		t.Errorf("langkah 1 = %q", got)
	}
	if got := p.Steps[1].Preview(true); got != "docker push nexus.lokal:8082/tim-a/app:1.0" {
		t.Errorf("langkah 2 = %q", got)
	}
	// Nama yang sudah sama tidak perlu di-tag ulang.
	if p := c.PushPlan("reg/app:1", "reg/app:1"); len(p.Steps) != 1 {
		t.Errorf("tanpa tag ulang: %d langkah", len(p.Steps))
	}
}

func TestCertPathTidakBisaKeluarDirektori(t *testing.T) {
	if got := CertPath("../../etc/ssl/ca"); strings.Contains(got, "..") {
		t.Errorf("CertPath = %q", got)
	}
}
