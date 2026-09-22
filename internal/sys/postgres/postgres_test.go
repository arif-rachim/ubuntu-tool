package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

const lsOut = `17  main    5432 online postgres /var/lib/postgresql/17/main /var/log/postgresql/postgresql-17-main.log
16  lama    5433 down   postgres /var/lib/postgresql/16/lama /var/log/postgresql/postgresql-16-lama.log`

func TestParseClusters(t *testing.T) {
	cs := ParseClusters("Ver Cluster Port Status Owner Data directory Log file\n" + lsOut + "\nbaris rusak\n")
	if len(cs) != 2 {
		t.Fatalf("cluster = %+v", cs)
	}
	c := cs[0]
	if c.ID() != "17/main" || c.Port != 5432 || !c.Online() {
		t.Errorf("cluster pertama = %+v", c)
	}
	if c.Unit() != "postgresql@17-main.service" {
		t.Errorf("unit = %q", c.Unit())
	}
	if c.HBAFile() != "/etc/postgresql/17/main/pg_hba.conf" {
		t.Errorf("hba = %q", c.HBAFile())
	}
	if c.DropIn(DropInTuning) != "/etc/postgresql/17/main/conf.d/10-ubt-tuning.conf" {
		t.Errorf("drop-in = %q", c.DropIn(DropInTuning))
	}
	if cs[1].Online() {
		t.Error("cluster kedua seharusnya down")
	}
}

func TestValidIdentDanQuote(t *testing.T) {
	for _, ok := range []string{"toko", "toko_online", "app2"} {
		if err := ValidIdent(ok); err != nil {
			t.Errorf("%q ditolak: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Toko Online", "1toko", "postgres", "template1", `x"; DROP DATABASE y; --`} {
		if err := ValidIdent(bad); err == nil {
			t.Errorf("%q seharusnya ditolak", bad)
		}
	}
	if got := QuoteIdent(`nama"aneh`); got != `"nama""aneh"` {
		t.Errorf("QuoteIdent = %s", got)
	}
	if got := QuoteLiteral("O'Brien"); got != `'O''Brien'` {
		t.Errorf("QuoteLiteral = %s", got)
	}
}

func TestClassifyQuery(t *testing.T) {
	cases := []struct{ sql, kind, warn string }{
		{"SELECT * FROM pesanan", QueryRead, ""},
		{"  with x as (select 1) select * from x", QueryRead, ""},
		{"UPDATE pesanan SET status='lunas' WHERE id=1", QueryWrite, ""},
		{"UPDATE pesanan SET status='lunas'", QueryWrite, "tidak punya WHERE"},
		{"delete from pesanan", QueryWrite, "tidak punya WHERE"},
		{"DROP TABLE pesanan", QueryDDL, "tidak bisa dibatalkan"},
		{"TRUNCATE pesanan", QueryDDL, "mengosongkan"},
		{"VACUUM FULL", QueryDDL, ""},
	}
	for _, tc := range cases {
		kind, warn := ClassifyQuery(tc.sql)
		if kind != tc.kind {
			t.Errorf("%q: jenis %q, ingin %q", tc.sql, kind, tc.kind)
		}
		if tc.warn != "" && !strings.Contains(warn, tc.warn) {
			t.Errorf("%q: peringatan %q, ingin memuat %q", tc.sql, warn, tc.warn)
		}
	}
}

func TestQueryPlanMenguncTransaksiBacaSaja(t *testing.T) {
	c := Client{Port: 5432}
	p := c.QueryPlan("toko", "SELECT count(*) FROM pesanan")
	cmd := p.Steps[0]
	if cmd.Risk != risk.Safe {
		t.Errorf("query baca risiko = %d", cmd.Risk)
	}
	if !strings.Contains(cmd.Preview(true), "SET TRANSACTION READ ONLY") {
		t.Errorf("tanpa pengaman baca-saja: %s", cmd.Preview(true))
	}
	// Query yang mengubah data tidak boleh dijalankan dalam transaksi baca-saja (akan selalu gagal),
	// dan harus ditandai berisiko.
	p = c.QueryPlan("toko", "UPDATE pesanan SET status='lunas'")
	cmd = p.Steps[0]
	if strings.Contains(cmd.Preview(true), "READ ONLY") {
		t.Error("query tulis tidak boleh dibungkus READ ONLY")
	}
	if cmd.Risk != risk.Dangerous {
		t.Errorf("UPDATE tanpa WHERE risiko = %d, ingin Dangerous", cmd.Risk)
	}
	if !strings.Contains(cmd.Effect, "SEMUA baris") {
		t.Errorf("efek = %q", cmd.Effect)
	}
}

func TestGrantPlanBerlapis(t *testing.T) {
	c := Client{}
	p := c.GrantPlan("toko", "app", AccessRead)
	var sqls []string
	for _, s := range p.Steps {
		sqls = append(sqls, s.Argv[len(s.Argv)-1])
	}
	joined := strings.Join(sqls, "\n")
	for _, want := range []string{"GRANT CONNECT ON DATABASE", "GRANT USAGE ON SCHEMA public", "GRANT SELECT ON ALL TABLES", "ALTER DEFAULT PRIVILEGES"} {
		if !strings.Contains(joined, want) {
			t.Errorf("grant baca tidak memuat %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "INSERT") {
		t.Errorf("akses baca tidak boleh memberi INSERT:\n%s", joined)
	}
	if w := c.GrantPlan("toko", "app", AccessWrite); !strings.Contains(strings.Join(argvTail(w), "\n"), "ALL SEQUENCES") {
		t.Error("akses tulis harus mencakup sequence")
	}
	if o := c.GrantPlan("toko", "app", AccessOwner); !strings.Contains(strings.Join(argvTail(o), "\n"), "ALTER DATABASE") {
		t.Error("akses pemilik harus memindahkan kepemilikan")
	}
	// Nama objek selalu dikutip supaya tidak bisa menyisipkan SQL lain.
	evil := c.GrantPlan("toko", `x"; DROP DATABASE toko; --`, AccessRead)
	for _, sql := range argvTail(evil) {
		if strings.Contains(sql, `TO x"`) {
			t.Errorf("identifier tidak dikutip: %s", sql)
		}
	}
}

func argvTail(p run.Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.Argv[len(s.Argv)-1])
	}
	return out
}

func TestPerintahBerjalanSebagaiPostgres(t *testing.T) {
	c := Client{Port: 5433}
	for _, p := range []run.Plan{
		c.CreateDatabasePlan("toko", "app"), c.DropDatabasePlan("toko"),
		c.CreateRolePlan("app", RoleOptions{Login: true}, true), c.SetPasswordPlan("app"),
		c.PsqlPlan("toko"), c.BackupPlan("toko", "/var/backups/postgresql", "toko.dump", FormatCustom),
	} {
		for _, s := range p.Steps {
			if s.Argv[0] == "install" {
				continue // langkah penyiapan direktori
			}
			if s.Argv[0] != "runuser" || s.Argv[1] != "-u" || s.Argv[2] != "postgres" || s.Argv[3] != "--" {
				t.Errorf("%s: tidak berjalan sebagai postgres: %v", p.Title, s.Argv)
			}
			if !s.NeedsRoot {
				t.Errorf("%s: runuser butuh root", p.Title)
			}
		}
	}
	// Port cluster non-bawaan ikut diteruskan.
	if !strings.Contains(c.CreateDatabasePlan("toko", "").Steps[0].Preview(true), "-p 5433") {
		t.Error("port cluster tidak diteruskan")
	}
}

func TestCreateRolePasswordInteraktif(t *testing.T) {
	c := Client{}
	p := c.CreateRolePlan("app", RoleOptions{Login: true}, true)
	cmd := p.Steps[0]
	if !cmd.Interactive {
		t.Error("createuser --pwprompt harus interaktif supaya password tidak lewat ubt")
	}
	if !strings.Contains(cmd.Preview(true), "--pwprompt") {
		t.Errorf("argv = %s", cmd.Preview(true))
	}
	if strings.Contains(cmd.Preview(true), "--superuser") {
		t.Error("role biasa tidak boleh superuser")
	}
	su := c.CreateRolePlan("admin", RoleOptions{Login: true, Superuser: true}, false)
	if su.Risk() != risk.Dangerous {
		t.Errorf("membuat superuser risiko = %d", su.Risk())
	}
}

func TestRemoteAccessPlan(t *testing.T) {
	cl := Cluster{Version: "17", Name: "main", Port: 5432}
	p := RemoteAccessPlan(cl, ListenSpecific, "toko", "app", "10.8.0.4/32")
	if p.Risk() != risk.Dangerous {
		t.Errorf("risiko = %d", p.Risk())
	}
	var kinds []string
	for _, s := range p.Steps {
		kinds = append(kinds, strings.Join(s.Argv[:1], ""))
	}
	want := []string{"cp", "tee", "install", "systemctl"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Errorf("langkah = %v, ingin %v", kinds, want)
	}
	hba := p.Steps[1].Stdin
	if !strings.Contains(hba, "host") || !strings.Contains(hba, "10.8.0.4/32") || !strings.Contains(hba, "scram-sha-256") {
		t.Errorf("baris pg_hba:\n%s", hba)
	}
	// Akses lokal saja: tidak mengubah listen_addresses dan cukup reload.
	local := RemoteAccessPlan(cl, ListenLocal, "toko", "app", "")
	if len(local.Steps) != 3 {
		t.Fatalf("akses lokal = %d langkah", len(local.Steps))
	}
	if !strings.Contains(local.Steps[2].Preview(true), "reload") {
		t.Errorf("akses lokal harus reload, bukan restart: %s", local.Steps[2].Preview(true))
	}
	if !strings.HasPrefix(strings.TrimSpace(local.Steps[1].Stdin[len("# Ditambahkan ubt\n"):]), "local") {
		t.Errorf("aturan lokal:\n%s", local.Steps[1].Stdin)
	}
}

func TestValidCIDR(t *testing.T) {
	if err := ValidCIDR("10.8.0.4/32"); err != nil {
		t.Errorf("CIDR sah ditolak: %v", err)
	}
	err := ValidCIDR("10.8.0.4")
	if err == nil || !strings.Contains(err.Error(), "/32") {
		t.Errorf("alamat tanpa prefix: %v", err)
	}
	if ValidCIDR("bukan alamat") == nil {
		t.Error("alamat ngawur harus ditolak")
	}
}

func TestTune(t *testing.T) {
	got := map[string]string{}
	for _, s := range Tune(Server{RAMBytes: 8 << 30, CPUs: 4, SSD: true, Workload: WorkloadWeb}) {
		got[s.Name] = s.Value
	}
	if got["shared_buffers"] != "2GB" {
		t.Errorf("shared_buffers = %q, ingin 2GB (seperempat dari 8 GB)", got["shared_buffers"])
	}
	if got["effective_cache_size"] != "6GB" {
		t.Errorf("effective_cache_size = %q", got["effective_cache_size"])
	}
	if got["random_page_cost"] != "1.1" {
		t.Errorf("SSD random_page_cost = %q", got["random_page_cost"])
	}
	if got["max_worker_processes"] != "4" || got["max_parallel_workers_per_gather"] != "2" {
		t.Errorf("paralel = %q / %q", got["max_worker_processes"], got["max_parallel_workers_per_gather"])
	}
	// Hard disk & beban analitik mengubah beberapa nilai.
	dw := map[string]string{}
	for _, s := range Tune(Server{RAMBytes: 8 << 30, CPUs: 4, Workload: WorkloadDW}) {
		dw[s.Name] = s.Value
	}
	if dw["random_page_cost"] != "4.0" || dw["default_statistics_target"] != "500" {
		t.Errorf("beban analitik: %q %q", dw["random_page_cost"], dw["default_statistics_target"])
	}
	if dw["work_mem"] == got["work_mem"] {
		t.Error("work_mem analitik seharusnya berbeda dari beban web")
	}
	// Mesin sangat kecil tidak boleh menghasilkan nilai nol atau negatif.
	for _, s := range Tune(Server{RAMBytes: 512 << 20, CPUs: 1, Workload: WorkloadWeb}) {
		if strings.HasPrefix(s.Value, "-") || s.Value == "0" || s.Value == "0MB" {
			t.Errorf("nilai tidak masuk akal di mesin kecil: %s = %s", s.Name, s.Value)
		}
	}
	if Tune(Server{}) != nil {
		t.Error("tanpa info RAM seharusnya tidak menghasilkan usulan")
	}
}

func TestTunedChanged(t *testing.T) {
	if (Tuned{Value: "2GB", Current: "2GB"}).Changed() {
		t.Error("nilai sama tidak boleh dianggap berubah")
	}
	if (Tuned{Value: "2GB", Current: "2048MB"}).Changed() {
		t.Error("satuan berbeda dengan nilai sama tidak boleh dianggap berubah")
	}
	if !(Tuned{Value: "2GB", Current: "128MB"}).Changed() {
		t.Error("nilai berbeda harus terdeteksi")
	}
}

func TestBackupScript(t *testing.T) {
	s := BackupSchedule{Databases: []string{"toko", "arsip"}, Dir: "/var/backups/postgresql", KeepDays: 7, Hour: 2}
	sc := s.Script()
	for _, want := range []string{"set -euo pipefail", "umask 077", "pg_dump -F c", "toko", "arsip",
		"pg_dumpall --globals-only", "-mtime +$KEEP -delete", "KEEP=7"} {
		if !strings.Contains(sc, want) {
			t.Errorf("script tidak memuat %q:\n%s", want, sc)
		}
	}
	cmd := s.ScriptCommand()
	if cmd.Argv[len(cmd.Argv)-1] != BackupScriptPath || !cmd.NeedsRoot {
		t.Errorf("perintah penulisan script = %v", cmd.Argv)
	}
	// Nama database dikutip supaya nama aneh tidak menjadi perintah shell tambahan.
	evil := BackupSchedule{Databases: []string{"x; rm -rf /"}, Dir: "/var/backups/postgresql", KeepDays: 1}
	if strings.Contains(evil.Script(), "pg_dump -F c -f \"$DIR/x; rm -rf /") {
		t.Errorf("nama database tidak dikutip:\n%s", evil.Script())
	}
}

func TestBackupNameDanFormat(t *testing.T) {
	now := time.Date(2026, 9, 22, 14, 30, 0, 0, time.UTC)
	if got := BackupName("toko", FormatCustom, now); got != "toko-2026-09-22-1430.dump" {
		t.Errorf("BackupName = %q", got)
	}
	if got := BackupName("../etc/passwd", FormatPlain, now); strings.Contains(got, "/") {
		t.Errorf("nama berkas bisa keluar direktori: %q", got)
	}
}

func TestRestorePlanClean(t *testing.T) {
	c := Client{}
	p := c.RestorePlan("/var/backups/postgresql/toko.dump", "toko", true, true)
	cmd := p.Steps[0]
	if cmd.Risk != risk.Dangerous {
		t.Errorf("restore --clean risiko = %d", cmd.Risk)
	}
	prev := cmd.Preview(true)
	if !strings.Contains(prev, "pg_restore") || !strings.Contains(prev, "--clean --if-exists") || !strings.Contains(prev, "--single-transaction") {
		t.Errorf("argv = %s", prev)
	}
	plain := c.RestorePlan("/var/backups/postgresql/toko.sql", "toko", false, false)
	if !strings.Contains(plain.Steps[0].Preview(true), "psql") {
		t.Errorf("berkas .sql harus lewat psql: %s", plain.Steps[0].Preview(true))
	}
}

// fakeRunner mengembalikan satu hasil JSON untuk query apa pun.
type fakeRunner struct {
	out    string
	stderr string
	err    error
	last   run.Command
}

func (f *fakeRunner) Capture(_ context.Context, c run.Command) (string, string, error) {
	f.last = c
	return f.out, f.stderr, f.err
}

func TestQueryMembacaJSON(t *testing.T) {
	f := &fakeRunner{out: `[{"name":"toko","owner":"app","size_bytes":1024,"connections":2,"encoding":"UTF8","allow_conn":true}]`}
	dbs, err := Client{Port: 5432}.ReadDatabases(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].Name != "toko" || dbs[0].Size != 1024 {
		t.Fatalf("database = %+v", dbs)
	}
	if !strings.Contains(run.JoinShell(f.last.Argv), "json_agg") {
		t.Errorf("query tidak dibungkus JSON: %v", f.last.Argv)
	}
	// Hasil kosong bukan error.
	f.out = ""
	if _, err := (Client{}).ReadDatabases(context.Background(), f); err != nil {
		t.Errorf("hasil kosong: %v", err)
	}
}

func TestQueryErrorButuhSudo(t *testing.T) {
	f := &fakeRunner{stderr: "sudo: a password is required", err: errExit}
	_, err := (Client{}).ReadRoles(context.Background(), f)
	if err != ErrNeedsSudo {
		t.Errorf("error = %v, ingin ErrNeedsSudo", err)
	}
	f = &fakeRunner{stderr: "psql: error: connection to server failed: connection refused", err: errExit}
	_, err = (Client{}).ReadRoles(context.Background(), f)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error = %v", err)
	}
}

type exitError struct{}

func (exitError) Error() string { return "exit status 1" }

var errExit = exitError{}

func TestHBARuleDescribe(t *testing.T) {
	r := HBARule{Type: "host", Database: []string{"toko"}, User: []string{"app"}, Address: "10.8.0.4/32", Method: "scram-sha-256"}
	got := r.Describe()
	for _, want := range []string{"app", "toko", "10.8.0.4/32", "password"} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe = %q, ingin memuat %q", got, want)
		}
	}
	if got := (HBARule{Error: "invalid connection type"}).Describe(); !strings.Contains(got, "BARIS RUSAK") {
		t.Errorf("baris rusak = %q", got)
	}
	if !strings.Contains(MethodMeaning("trust"), "TANPA password") {
		t.Error("metode trust harus diberi peringatan")
	}
}

func TestActivityProblem(t *testing.T) {
	cases := []struct {
		a    Activity
		want string
	}{
		{Activity{State: "idle in transaction", StateSecs: 120}, "menahan lock"},
		{Activity{State: "active", QuerySecs: 600}, "5 menit"},
		{Activity{State: "active", BlockedBy: []int{42}}, "menunggu lock"},
		{Activity{State: "idle", StateSecs: 5}, ""},
	}
	for _, tc := range cases {
		if got := tc.a.Problem(); !strings.Contains(got, tc.want) || (tc.want == "" && got != "") {
			t.Errorf("%+v: problem %q, ingin memuat %q", tc.a, got, tc.want)
		}
	}
}

func TestTableBloat(t *testing.T) {
	if got := (Table{LiveRows: 80, DeadRows: 20}).Bloat(); got != 20 {
		t.Errorf("Bloat = %v", got)
	}
	if got := (Table{}).Bloat(); got != 0 {
		t.Errorf("tabel kosong Bloat = %v", got)
	}
}
