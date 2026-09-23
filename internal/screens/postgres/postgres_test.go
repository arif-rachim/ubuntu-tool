package postgres

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func ready() syspg.Status {
	return syspg.Status{
		Avail:    syspg.Ready,
		Clusters: []syspg.Cluster{{Version: "17", Name: "main", Port: 5432, Status: "online"}},
		Cluster:  syspg.Cluster{Version: "17", Name: "main", Port: 5432, Status: "online"},
		Summary: syspg.Summary{Version: "17.6", Uptime: 7200, Connections: 12, MaxConn: 100, Active: 2,
			Idle: 1, Databases: 2, DataSize: 1 << 30},
	}
}

func dashboard(t *testing.T) *Model {
	t.Helper()
	m := newModel(shared.Env{Now: time.Now}, syspg.Client{LsBin: "/usr/bin/pg_lsclusters", PsqlBin: "/usr/bin/psql"})
	m.Update(statusMsg{owner: m, status: ready(), databases: []syspg.Database{
		{Name: "toko", Owner: "app", Encoding: "UTF8", Size: 900 << 20, Connections: 3},
		{Name: "postgres", Owner: "postgres", Encoding: "UTF8", Size: 8 << 20},
	}})
	return m
}

func TestDashboard(t *testing.T) {
	m := dashboard(t)
	view := ansi.Strip(m.View(120, 30))
	for _, want := range []string{"PostgreSQL 17.6", "cluster 17/main", "port 5432", "Koneksi 12/100", "1 idle in transaction", "toko", "Database (2)"} {
		if !strings.Contains(view, want) {
			t.Errorf("tampilan tidak memuat %q:\n%s", want, view)
		}
	}
	// pg_stat_statements belum aktif: user diberi tahu cara mengaktifkannya.
	if !strings.Contains(view, "pg_stat_statements belum aktif") {
		t.Errorf("tidak ada petunjuk pg_stat_statements:\n%s", view)
	}
}

func TestDashboardKondisiBelumSiap(t *testing.T) {
	cases := []struct {
		st   syspg.Status
		want []string
	}{
		{syspg.Status{Avail: syspg.NotInstalled}, []string{"belum terpasang", "PostgreSQL 18", "repository resmi"}},
		{syspg.Status{Avail: syspg.NoCluster}, []string{"belum ada cluster", "pg_createcluster"}},
		{syspg.Status{Avail: syspg.Down, Clusters: []syspg.Cluster{{Version: "17", Name: "main", Port: 5432, Status: "down", LogFile: "/var/log/postgresql/x.log"}}},
			[]string{"tidak berjalan", "/var/log/postgresql/x.log"}},
		{syspg.Status{Avail: syspg.NoPermission}, []string{"izin root", "runuser -u postgres"}},
	}
	for _, tc := range cases {
		m := newModel(shared.Env{Now: time.Now}, syspg.Client{})
		m.Update(statusMsg{owner: m, status: tc.st})
		view := ansi.Strip(m.View(120, 30))
		for _, want := range tc.want {
			if !strings.Contains(view, want) {
				t.Errorf("status %d tidak memuat %q:\n%s", tc.st.Avail, want, view)
			}
		}
	}
}

func TestInstallDanSudoProbe(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, syspg.Client{})
	m.Update(statusMsg{owner: m, status: syspg.Status{Avail: syspg.NotInstalled}})
	if _, cmd := m.Update(testutil.Key("i")); len(testutil.Run(cmd)) == 0 {
		t.Fatal("tombol i tidak membuka wizard versi")
	}
	// Versi bawaan Ubuntu: cukup satu perintah apt, tanpa repository tambahan.
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-install", Answers: ask.Answers{"versi": {Values: []string{"ubuntu"}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "apt-get install -y postgresql postgresql-contrib") {
		t.Errorf("install bawaan:\n%s", confirm)
	}
	if strings.Contains(confirm, "apt.postgresql.org") {
		t.Errorf("versi bawaan Ubuntu tidak boleh menambah repository:\n%s", confirm)
	}
	// PostgreSQL 18 belum ada di repository Ubuntu 24.04: repository resmi ditambahkan dulu.
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-install", Answers: ask.Answers{"versi": {Values: []string{"18"}}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 60))
	for _, want := range []string{"postgresql-common", "apt.postgresql.org.sh", "apt-get install -y postgresql-18 postgresql-contrib-18", "BAHAYA"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("install PGDG tidak memuat %q:\n%s", want, confirm)
		}
	}

	m = newModel(shared.Env{Now: time.Now}, syspg.Client{LsBin: "/usr/bin/pg_lsclusters"})
	m.Update(statusMsg{owner: m, status: syspg.Status{Avail: syspg.NoPermission, NeedsSudo: true}})
	_, cmd = m.Update(testutil.Key("s"))
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	// Yang diminta hanya izin membaca — bukan perintah yang mengubah apa pun.
	if !strings.Contains(confirm, "runuser -u postgres") || !strings.Contains(confirm, "SELECT 1") {
		t.Errorf("probe sudo:\n%s", confirm)
	}
	if strings.Contains(confirm, "BERISIKO") {
		t.Errorf("perintah baca tidak boleh ditandai berisiko:\n%s", confirm)
	}
}

func TestBuatDatabase(t *testing.T) {
	m := dashboard(t)
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "db-add", Answers: ask.Answers{
		"name": {Text: "gudang"}, "owner": {Values: []string{"app"}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "runuser -u postgres -- createdb -E UTF8 -O app -p 5432 gudang") {
		t.Errorf("createdb:\n%s", confirm)
	}
}

func TestAksiDatabaseQueryDanHapus(t *testing.T) {
	m := dashboard(t)
	m.selected = m.databases[0]

	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "db-query", Answers: ask.Answers{
		"sql": {Text: "SELECT count(*) FROM pesanan"}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "SET TRANSACTION READ ONLY") {
		t.Errorf("query baca harus dikunci read-only:\n%s", confirm)
	}

	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "db-action", Answers: ask.Answers{
		"action": {Values: []string{"drop"}}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "dropdb") || !strings.Contains(confirm, "BAHAYA") {
		t.Errorf("hapus database:\n%s", confirm)
	}
	if !strings.Contains(confirm, "Backup dulu") {
		t.Errorf("tidak ada alternatif yang lebih aman:\n%s", confirm)
	}
}

func TestAksiDatabaseMembukaLayar(t *testing.T) {
	m := dashboard(t)
	m.selected = m.databases[0]
	for _, tc := range []struct{ action, title string }{
		{"tables", "Tabel toko"}, {"backup", "Cadangan"},
	} {
		_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "db-action", Answers: ask.Answers{
			"action": {Values: []string{tc.action}}}}})
		msgs := testutil.Run(cmd)
		if len(msgs) == 0 {
			t.Errorf("aksi %s tidak membuka apa pun", tc.action)
			continue
		}
		if got := msgs[0].(nav.PushMsg).Screen.Title(); got != tc.title {
			t.Errorf("aksi %s membuka %q, ingin %q", tc.action, got, tc.title)
		}
	}
}

func TestAksesJaringan(t *testing.T) {
	m := dashboard(t)
	_, cmd := m.Update(testutil.Key("n"))
	if len(testutil.Run(cmd)) == 0 {
		t.Fatal("tombol n tidak membuka wizard akses jaringan")
	}
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-remote", Answers: ask.Answers{
		"mode": {Values: []string{syspg.ListenSpecific}}, "cidr": {Text: "10.8.0.4/32"},
		"db": {Values: []string{"toko"}}, "role": {Values: []string{"app"}},
		"firewall": {Values: []string{FirewallNo}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 60))
	for _, want := range []string{"pg_hba.conf", "10.8.0.4/32", "scram-sha-256", "listen_addresses", "systemctl restart postgresql@17-main.service", "BAHAYA"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("akses jaringan tidak memuat %q:\n%s", want, confirm)
		}
	}
	// Berkas lama dicadangkan lebih dulu.
	if !strings.Contains(confirm, "cp -a /etc/postgresql/17/main/pg_hba.conf") {
		t.Errorf("tanpa cadangan pg_hba:\n%s", confirm)
	}
}

func TestRoleWizard(t *testing.T) {
	m := NewRoles(shared.Env{Now: time.Now}, syspg.Client{Port: 5432}, []string{"toko"})
	m.Update(rolesMsg{owner: m, roles: []syspg.Role{
		{Name: "postgres", Superuser: true, Login: true, HasPassword: true},
		{Name: "app", Login: true},
		{Name: "laporan"},
	}})
	view := ansi.Strip(m.View(120, 30))
	for _, want := range []string{"Role (3)", "superuser", "grup", "app"} {
		if !strings.Contains(view, want) {
			t.Errorf("daftar role tidak memuat %q:\n%s", want, view)
		}
	}

	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "role-add", Answers: ask.Answers{
		"name": {Text: "app_baru"}, "kind": {Values: []string{"app"}},
		"password": {Values: []string{ask.ValueYes}}, "createdb": {Values: []string{ask.ValueNo}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "createuser --login --pwprompt") {
		t.Errorf("buat role:\n%s", confirm)
	}

	m.selected = syspg.Role{Name: "app", Login: true}
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "role-grant", Answers: ask.Answers{
		"db": {Values: []string{"toko"}}, "level": {Values: []string{syspg.AccessRead}}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 60))
	for _, want := range []string{"GRANT CONNECT ON DATABASE", "GRANT USAGE ON SCHEMA public", "GRANT SELECT ON ALL TABLES"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("grant tidak memuat %q:\n%s", want, confirm)
		}
	}
}

func TestMonitorTemuanKoneksi(t *testing.T) {
	m := NewMonitor(shared.Env{Now: time.Now}, syspg.Client{Port: 5432}, ready(), []string{"toko"})
	m.Update(monitorMsg{owner: m, activity: []syspg.Activity{
		{PID: 101, User: "app", Database: "toko", State: "idle in transaction", StateSecs: 300, Client: "10.0.0.5"},
		{PID: 102, User: "app", Database: "toko", State: "active", QuerySecs: 5, Query: "SELECT 1"},
	}})
	view := ansi.Strip(m.View(140, 30))
	if !strings.Contains(view, "PID 101") || !strings.Contains(view, "menahan lock") {
		t.Errorf("temuan koneksi:\n%s", view)
	}

	m.selected = syspg.Activity{PID: 101, User: "app", Database: "toko", State: "idle in transaction"}
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "act-action", Answers: ask.Answers{
		"action": {Values: []string{"terminate"}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "pg_terminate_backend(101)") || !strings.Contains(confirm, "BAHAYA") {
		t.Errorf("putus koneksi:\n%s", confirm)
	}
	if !strings.Contains(confirm, "pg_cancel_backend(101)") {
		t.Errorf("tidak menawarkan alternatif membatalkan query:\n%s", confirm)
	}
}

func TestTuningMenghitungDanMenerapkan(t *testing.T) {
	cluster := syspg.Cluster{Version: "17", Name: "main", Port: 5432}
	m := NewTuning(shared.Env{Now: time.Now, ProcRoot: "/proc"}, syspg.Client{Port: 5432}, cluster)
	m.server = syspg.Server{RAMBytes: 8 << 30, CPUs: 4, SSD: true, Workload: syspg.WorkloadWeb}
	m.Update(tuningMsg{owner: m, settings: []syspg.Setting{
		{Name: "shared_buffers", Value: "16384", Unit: "8kB", Source: "configuration file"},
		{Name: "max_connections", Value: "100", Source: "configuration file"},
	}})
	view := ansi.Strip(m.View(140, 40))
	if !strings.Contains(view, "shared_buffers") || !strings.Contains(view, "asal: configuration file") {
		t.Errorf("parameter yang berlaku:\n%s", view)
	}

	m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-tune", Answers: ask.Answers{
		"workload": {Values: []string{syspg.WorkloadWeb}}, "disk": {Values: []string{"ssd"}}, "maxconn": {Text: "100"}}}})
	view = ansi.Strip(m.View(140, 60))
	for _, want := range []string{"USULAN", "shared_buffers", "2GB", "128MB"} {
		if !strings.Contains(view, want) {
			t.Errorf("usulan tidak memuat %q:\n%s", want, view)
		}
	}

	_, cmd := m.Update(testutil.Key("enter"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 60))
	for _, want := range []string{"conf.d/10-ubt-tuning.conf", "shared_buffers", "systemctl restart postgresql@17-main.service"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("terapkan tidak memuat %q:\n%s", want, confirm)
		}
	}
}

func TestBackupDanJadwal(t *testing.T) {
	dir := t.TempDir()
	m := NewBackup(shared.Env{Now: func() time.Time { return time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC) }},
		syspg.Client{Port: 5432}, []string{"toko", "arsip"})
	m.dir = dir

	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-backup", Answers: ask.Answers{
		"db": {Values: []string{"toko"}}, "format": {Values: []string{syspg.FormatCustom}}, "dir": {Text: dir}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 50))
	if !strings.Contains(confirm, "pg_dump -F c") || !strings.Contains(confirm, "toko-2026-09-22-0200.dump") {
		t.Errorf("cadangkan:\n%s", confirm)
	}
	if !strings.Contains(confirm, "install -d -m 0750 -o postgres") {
		t.Errorf("direktori cadangan tidak disiapkan:\n%s", confirm)
	}

	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-schedule", Answers: ask.Answers{
		"dbs": {Values: []string{"toko", "arsip"}}, "dir": {Text: dir}, "keep": {Text: "14"}, "hour": {Values: []string{"2"}}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 60))
	for _, want := range []string{"/usr/local/bin/ubt-pg-backup", "/etc/cron.d/ubt-pg-backup", "KEEP=14"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("jadwal tidak memuat %q:\n%s", want, confirm)
		}
	}
}

// Aturan firewall harus dipasang SEBELUM port dibuka, supaya tidak ada jeda saat PostgreSQL
// mendengarkan jaringan tetapi belum dipagari ufw.
func TestAksesJaringanDenganUfw(t *testing.T) {
	m := dashboard(t)
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-remote", Answers: ask.Answers{
		"mode": {Values: []string{syspg.ListenSpecific}}, "cidr": {Text: "10.8.0.4/32"},
		"db": {Values: []string{"toko"}}, "role": {Values: []string{"app"}},
		"firewall": {Values: []string{FirewallFrom}}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(160, 200))
	if !strings.Contains(confirm, "ufw allow from 10.8.0.4/32 to any port 5432 proto tcp") {
		t.Errorf("aturan ufw tidak ada:\n%s", confirm)
	}
	ufw := strings.Index(confirm, "ufw allow")
	listen := strings.Index(confirm, "listen_addresses")
	if ufw < 0 || listen < 0 || ufw > listen {
		t.Errorf("urutan salah: ufw di %d, listen_addresses di %d\n%s", ufw, listen, confirm)
	}

	// Pilihan "dari mana saja" tidak membatasi sumber.
	_, cmd = m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-remote", Answers: ask.Answers{
		"mode": {Values: []string{syspg.ListenAll}}, "cidr": {Text: "0.0.0.0/0"},
		"db": {Values: []string{"toko"}}, "role": {Values: []string{"app"}},
		"firewall": {Values: []string{FirewallAny}}}}})
	confirm = ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(160, 200))
	if !strings.Contains(confirm, "ufw allow 5432/tcp") {
		t.Errorf("aturan ufw terbuka:\n%s", confirm)
	}
}

func TestWizardFirewallMenyesuaikanKondisiUfw(t *testing.T) {
	f := RemoteAccessForm(nil, nil, FirewallInfo{}, 5432)
	var q ask.Question
	for _, x := range f.Questions {
		if x.ID == "firewall" {
			q = x
		}
	}
	if q.ID == "" {
		t.Fatal("pertanyaan firewall tidak ada")
	}
	if q.Options[0].Disabled == "" || !strings.Contains(q.Options[0].Disabled, "belum terpasang") {
		t.Errorf("tanpa ufw, opsi harus dinonaktifkan: %+v", q.Options[0])
	}
	if !q.Options[2].Recommended {
		t.Error("tanpa ufw, pilihan \"atur sendiri\" yang disarankan")
	}
	f = RemoteAccessForm(nil, nil, FirewallInfo{Installed: true}, 5432)
	for _, x := range f.Questions {
		if x.ID == "firewall" {
			q = x
		}
	}
	if !strings.Contains(q.Help, "BELUM AKTIF") {
		t.Errorf("ufw tidak aktif harus disebut di bantuan: %q", q.Help)
	}
}

func TestPilihClusterSaatAdaBeberapaVersi(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, syspg.Client{LsBin: "/usr/bin/pg_lsclusters"})
	st := ready()
	st.Clusters = []syspg.Cluster{
		{Version: "16", Name: "main", Port: 5432, Status: "down"},
		{Version: "18", Name: "main", Port: 5433, Status: "online"},
	}
	st.Cluster = st.Clusters[1]
	m.Update(statusMsg{owner: m, status: st})
	view := ansi.Strip(m.View(160, 30))
	if !strings.Contains(view, "cluster 18/main") || !strings.Contains(view, "cluster lain: 16/main (down)") {
		t.Errorf("cluster lain tidak disebut:\n%s", view)
	}
	_, cmd := m.Update(testutil.Key("c"))
	msgs := testutil.Run(cmd)
	if len(msgs) == 0 {
		t.Fatal("tombol c tidak membuka pemilih cluster")
	}
	m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-pick", Answers: ask.Answers{"cluster": {Values: []string{"16/main"}}}}})
	if m.wanted != "16/main" {
		t.Errorf("cluster pilihan = %q", m.wanted)
	}
}

func TestBuatClusterSaatBelumAda(t *testing.T) {
	m := newModel(shared.Env{Now: time.Now}, syspg.Client{LsBin: "/usr/bin/pg_lsclusters"})
	m.Update(statusMsg{owner: m, status: syspg.Status{Avail: syspg.NoCluster}})
	if _, cmd := m.Update(testutil.Key("c")); len(testutil.Run(cmd)) == 0 {
		t.Fatal("tombol c tidak membuka wizard cluster")
	}
	_, cmd := m.Update(nav.ResumedMsg{Result: ask.Result{ID: "pg-cluster", Answers: ask.Answers{
		"versi": {Values: []string{"18"}}, "nama": {Text: "main"}}}})
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(120, 40))
	if !strings.Contains(confirm, "pg_createcluster 18 main --start") {
		t.Errorf("buat cluster:\n%s", confirm)
	}
}

// --- editor query dengan saran otomatis ---------------------------------------------------------

func skemaToko() syspg.Schema {
	return syspg.Schema{Database: "toko", Relations: []syspg.Relation{
		{Schema: "public", Name: "pelanggan", Kind: "r", Columns: []syspg.Column{
			{Name: "id", Type: "integer", PK: true, NotNull: true},
			{Name: "nama", Type: "text", NotNull: true},
			{Name: "email", Type: "character varying(120)"},
		}},
		{Schema: "public", Name: "pesanan", Kind: "r", Columns: []syspg.Column{
			{Name: "id", Type: "bigint", PK: true, NotNull: true},
			{Name: "total", Type: "numeric(12,2)", NotNull: true},
			{Name: "status", Type: "text"},
			{Name: "dibuat_pada", Type: "timestamp with time zone", NotNull: true},
		}},
	}}
}

func editor(t *testing.T) *queryModel {
	t.Helper()
	m := NewQuery(shared.Env{Now: time.Now}, syspg.Client{Port: 5432}, "toko")
	m.Update(nav.SizeMsg{Width: 120, Height: 40})
	m.Update(schemaMsg{owner: m, schema: skemaToko()})
	return m
}

// ketik mengirimkan setiap karakter sebagai penekanan tombol, seperti user sungguhan.
func ketik(m *queryModel, s string) {
	for _, r := range s {
		if r == ' ' {
			m.Update(testutil.Key("space"))
			continue
		}
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func saranTeks(m *queryModel) []string {
	var out []string
	for _, s := range m.comp.Suggestions {
		out = append(out, s.Text)
	}
	return out
}

func memuatSaran(m *queryModel, want string) bool {
	for _, s := range saranTeks(m) {
		if s == want {
			return true
		}
	}
	return false
}

func TestEditorMenampilkanSkema(t *testing.T) {
	m := editor(t)
	view := ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "2 tabel · 7 kolom dikenali") {
		t.Errorf("ringkasan skema:\n%s", view)
	}
}

func TestEditorMelengkapiNamaTabelDanKolom(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT * FROM pesa")
	if !memuatSaran(m, "pesanan") {
		t.Fatalf("saran tabel: %v", saranTeks(m))
	}
	view := ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "pesanan") || !strings.Contains(view, "tabel") {
		t.Errorf("daftar saran tidak tampil:\n%s", view)
	}

	// tab memakai saran: kata yang sedang diketik diganti utuh.
	m.Update(testutil.Key("tab"))
	if got := m.area.Value(); got != "SELECT * FROM pesanan" {
		t.Fatalf("setelah tab = %q", got)
	}

	// Lanjut mengetik WHERE: kolom tabel itu yang disarankan, lengkap dengan tipenya.
	ketik(m, " WHERE ")
	if !memuatSaran(m, "dibuat_pada") || memuatSaran(m, "email") {
		t.Errorf("kolom di WHERE: %v", saranTeks(m))
	}
	view = ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "timestamp with time zone") {
		t.Errorf("tipe kolom tidak ditampilkan:\n%s", view)
	}
}

// Inti permintaan: menyaring kolom waktu dituntun sampai bentuk nilainya.
func TestEditorMenuntunFilterWaktu(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT * FROM pesanan WHERE dibuat_pada ")
	if got := m.comp.Suggestions[0].Text; got != ">=" {
		t.Errorf("operator pertama = %q, ingin >=", got)
	}
	if !strings.Contains(m.comp.Context, "dibuat_pada") {
		t.Errorf("konteks = %q", m.comp.Context)
	}
	m.Update(testutil.Key("tab"))
	if got := m.area.Value(); !strings.HasSuffix(got, ">=") {
		t.Fatalf("setelah memakai operator = %q", got)
	}

	ketik(m, " ")
	for _, want := range []string{"now() - interval '7 days'", "date_trunc('month', now())", "current_date"} {
		if !memuatSaran(m, want) {
			t.Errorf("nilai waktu %q tidak ada: %v", want, saranTeks(m))
		}
	}
	// Penjelasan singkat ikut ditampilkan supaya user paham maksud nilainya.
	view := ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "zona waktu server") && !strings.Contains(view, "hari ini") {
		t.Errorf("penjelasan nilai tidak tampil:\n%s", view)
	}

	// Pilih "7 hari lalu" lalu pakai.
	for i, s := range m.comp.Suggestions {
		if s.Text == "now() - interval '7 days'" {
			m.sel = i
		}
	}
	m.Update(testutil.Key("tab"))
	if got := m.area.Value(); got != "SELECT * FROM pesanan WHERE dibuat_pada >= now() - interval '7 days'" {
		t.Fatalf("query akhir = %q", got)
	}
}

func TestEditorMemilihSaranDenganCtrlN(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT * FROM ")
	pertama := m.comp.Suggestions[0].Text
	m.Update(testutil.Key("ctrl+n"))
	if m.sel != 1 {
		t.Fatalf("ctrl+n tidak memindah pilihan: sel=%d", m.sel)
	}
	kedua := m.comp.Suggestions[1].Text
	m.Update(testutil.Key("tab"))
	if got := m.area.Value(); !strings.HasSuffix(got, kedua) || strings.HasSuffix(got, pertama) {
		t.Errorf("saran kedua tidak dipakai: %q", got)
	}
	m.Update(testutil.Key("ctrl+p"))
	if m.sel != 0 {
		t.Errorf("ctrl+p: sel=%d", m.sel)
	}
}

func TestEditorKursorBerhentiDiDalamTemplate(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT sum")
	for i, s := range m.comp.Suggestions {
		if s.Text == "sum()" {
			m.sel = i
		}
	}
	m.Update(testutil.Key("tab"))
	if got := m.area.Value(); got != "SELECT sum()" {
		t.Fatalf("setelah tab = %q", got)
	}
	// Kursor berada di dalam kurung: mengetik langsung mengisi argumennya.
	ketik(m, "total")
	if got := m.area.Value(); got != "SELECT sum(total)" {
		t.Errorf("kursor tidak di dalam kurung: %q", got)
	}
}

func TestEditorMenandaiQueryYangMengubahData(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT * FROM pesanan")
	view := ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "hanya membaca") || !strings.Contains(view, "READ ONLY") {
		t.Errorf("query baca:\n%s", view)
	}

	m.Update(testutil.Key("ctrl+l"))
	ketik(m, "UPDATE pesanan SET status = 'lunas'")
	view = ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "mengubah data") {
		t.Errorf("query tulis tidak ditandai:\n%s", view)
	}
	if !strings.Contains(view, "tidak punya WHERE") {
		t.Errorf("UPDATE tanpa WHERE harus diperingatkan:\n%s", view)
	}
}

func TestEditorMenjalankanQuery(t *testing.T) {
	// Query baca langsung membuka layar hasil berbentuk tabel.
	m := editor(t)
	ketik(m, "SELECT count(*) FROM pesanan")
	_, cmd := m.Update(testutil.Key("ctrl+r"))
	msgs := testutil.Run(cmd)
	if len(msgs) == 0 {
		t.Fatal("ctrl+r tidak membuka layar hasil")
	}
	if got := msgs[0].(nav.PushMsg).Screen.Title(); got != "Hasil" {
		t.Errorf("query baca membuka layar %q, ingin Hasil", got)
	}

	// Query yang mengubah data tetap lewat layar konfirmasi dengan command persisnya.
	m.Update(testutil.Key("ctrl+l"))
	ketik(m, "DELETE FROM pesanan WHERE id = 1")
	_, cmd = m.Update(testutil.Key("ctrl+r"))
	confirm := ansi.Strip(testutil.Run(cmd)[0].(nav.PushMsg).Screen.View(140, 50))
	for _, want := range []string{"DELETE FROM pesanan WHERE id = 1", "runuser -u postgres"} {
		if !strings.Contains(confirm, want) {
			t.Errorf("konfirmasi tidak memuat %q:\n%s", want, confirm)
		}
	}
	if strings.Contains(confirm, "READ ONLY") {
		t.Errorf("query tulis tidak boleh dibungkus READ ONLY:\n%s", confirm)
	}

	// Query kosong tidak dijalankan.
	m2 := editor(t)
	if _, cmd := m2.Update(testutil.Key("ctrl+r")); cmd != nil {
		t.Error("query kosong seharusnya tidak dijalankan")
	}
	if !strings.Contains(m2.message, "Tulis query") {
		t.Errorf("pesan = %q", m2.message)
	}
}

func TestEditorEscMenutupSaranLaluKeluar(t *testing.T) {
	m := editor(t)
	ketik(m, "SELECT * FROM pe")
	if !m.HandlesBack() {
		t.Fatal("saat saran tampil, esc harus ditangani layar")
	}
	m.Update(testutil.Key("esc"))
	if m.visible() {
		t.Error("esc pertama harus menutup daftar saran")
	}
	if m.HandlesBack() {
		t.Error("setelah saran tertutup, esc harus kembali ke layar sebelumnya")
	}
	_, cmd := m.Update(testutil.Key("esc"))
	if len(testutil.Run(cmd)) == 0 {
		t.Error("esc kedua harus keluar dari layar")
	}
	// Mengetik lagi memunculkan saran kembali.
	ketik(m, "s")
	if !m.visible() {
		t.Error("mengetik harus memunculkan saran lagi")
	}
}

func TestEditorSkemaGagalTetapBisaDipakai(t *testing.T) {
	m := NewQuery(shared.Env{Now: time.Now}, syspg.Client{}, "toko")
	m.Update(nav.SizeMsg{Width: 120, Height: 40})
	m.Update(schemaMsg{owner: m, schema: syspg.Schema{Database: "toko"}, err: errExitTest{}})
	view := ansi.Strip(m.View(120, 40))
	if !strings.Contains(view, "skema tidak terbaca") {
		t.Errorf("kegagalan skema tidak disebut:\n%s", view)
	}
	// Tetap bisa mengetik dan menjalankan query manual.
	ketik(m, "SELECT 1")
	if _, cmd := m.Update(testutil.Key("ctrl+r")); cmd == nil {
		t.Error("query manual harus tetap bisa dijalankan")
	}
}

type errExitTest struct{}

func (errExitTest) Error() string { return "izin ditolak" }

// --- layar hasil query ---------------------------------------------------------------------------

func hasilUji() syspg.Result {
	return syspg.Result{
		Columns: []string{"id", "pelanggan", "catatan", "total"},
		Rows: [][]string{
			{"1", "Budi Santoso", "kirim pagi", "150000"},
			{"2", "Siti Aminah", syspg.NullMarker, "99500"},
			{"3", "Andi Wijaya", "alamat:\nJl. Merdeka 10", "1250000"},
		},
		Elapsed: 42 * time.Millisecond,
	}
}

func hasil(t *testing.T, res syspg.Result, err error) *resultModel {
	t.Helper()
	m := NewResult(shared.Env{Now: time.Now}, syspg.Client{PsqlBin: "/usr/bin/psql"}, "toko",
		"SELECT id, pelanggan, catatan, total FROM pesanan")
	m.Update(resultMsg{owner: m, res: res, err: err})
	return m
}

func TestHasilQueryDitampilkanSebagaiTabel(t *testing.T) {
	m := hasil(t, hasilUji(), nil)
	view := ansi.Strip(m.View(120, 30))
	for _, want := range []string{"3 baris", "4 kolom", "42 ms", "pelanggan", "Budi Santoso", "Siti Aminah", "1250000"} {
		if !strings.Contains(view, want) {
			t.Errorf("tabel hasil tidak memuat %q:\n%s", want, view)
		}
	}
	// NULL harus terbaca sebagai NULL, bukan sel kosong yang menyesatkan.
	if !strings.Contains(view, "NULL") {
		t.Errorf("sel NULL tidak ditandai:\n%s", view)
	}
	// Kolom angka dirata-kanankan supaya digitnya sejajar dan mudah dibandingkan.
	if !m.res.NumericColumn(3) || m.res.NumericColumn(1) {
		t.Error("kolom angka tidak dikenali dengan benar")
	}
	if !m.table.Columns[3].Right || m.table.Columns[1].Right {
		t.Error("hanya kolom angka yang boleh rata kanan")
	}
}

func TestHasilQueryDisaring(t *testing.T) {
	m := hasil(t, hasilUji(), nil)
	m.Update(testutil.Key("/"))
	if !m.Typing() {
		t.Fatal("tombol / harus membuka mode saring")
	}
	for _, msg := range testutil.Type("siti") {
		m.Update(msg)
	}
	if len(m.rows) != 1 {
		t.Fatalf("saring 'siti' menyisakan %d baris, ingin 1", len(m.rows))
	}
	view := ansi.Strip(m.View(120, 30))
	if !strings.Contains(view, "1 dari 3 baris") || strings.Contains(view, "Budi Santoso") {
		t.Errorf("tampilan tersaring salah:\n%s", view)
	}
	// Saringan tanpa hasil tetap menjelaskan cara kembali.
	for _, msg := range testutil.Type("xyz") {
		m.Update(msg)
	}
	if got := ansi.Strip(m.View(120, 30)); !strings.Contains(got, "Tidak ada baris yang cocok") {
		t.Errorf("saringan kosong:\n%s", got)
	}
	// esc menutup mode saring sekaligus menghapus saringannya.
	m.Update(testutil.Key("esc"))
	if m.Typing() || len(m.rows) != 3 {
		t.Errorf("esc harus mengembalikan seluruh %d baris, dapat %d", 3, len(m.rows))
	}
	if m.HandlesBack() {
		t.Error("tanpa saringan aktif, esc harus kembali ke editor")
	}
}

func TestHasilQueryDetailBaris(t *testing.T) {
	m := hasil(t, hasilUji(), nil)
	m.Update(testutil.Key("down"))
	m.Update(testutil.Key("down"))
	_, cmd := m.Update(testutil.Key("enter"))
	msgs := testutil.Run(cmd)
	if len(msgs) == 0 {
		t.Fatal("enter tidak membuka detail baris")
	}
	s := msgs[0].(nav.PushMsg).Screen
	if got := s.Title(); got != "Baris 3" {
		t.Errorf("judul detail = %q", got)
	}
	view := ansi.Strip(s.View(120, 30))
	// Di tabel isi panjang dipotong; di detail harus utuh, termasuk baris barunya.
	for _, want := range []string{"catatan", "alamat:", "Jl. Merdeka 10", "Andi Wijaya", "1250000"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail baris tidak memuat %q:\n%s", want, view)
		}
	}
}

func TestHasilQueryDetailMenampilkanNull(t *testing.T) {
	res := hasilUji()
	d := newRowDetail(res.Columns, res.Rows[1], 2)
	view := ansi.Strip(d.View(120, 30))
	if !strings.Contains(view, "catatan") || !strings.Contains(view, "NULL") {
		t.Errorf("NULL tidak terlihat di detail:\n%s", view)
	}
}

func TestHasilQueryKosongDanTerpotong(t *testing.T) {
	kosong := ansi.Strip(hasil(t, syspg.Result{Columns: []string{"id"}}, nil).View(120, 30))
	if !strings.Contains(kosong, "tidak mengembalikan baris") {
		t.Errorf("hasil kosong:\n%s", kosong)
	}
	potong := ansi.Strip(hasil(t, syspg.Result{Columns: []string{"id"}, Rows: [][]string{{"1"}}, Truncated: true}, nil).View(120, 30))
	if !strings.Contains(potong, "LIMIT") {
		t.Errorf("peringatan pemotongan tidak menyarankan LIMIT:\n%s", potong)
	}
}

func TestHasilQueryGagalMenjelaskanError(t *testing.T) {
	m := hasil(t, syspg.Result{}, errKolomTest{})
	view := ansi.Strip(m.View(120, 30))
	for _, want := range []string{"Query gagal", `column "pelangan" does not exist`, "kembali ke editor"} {
		if !strings.Contains(view, want) {
			t.Errorf("layar error tidak memuat %q:\n%s", want, view)
		}
	}
	// Tombol r menjalankan ulang query yang sama.
	if _, cmd := m.Update(testutil.Key("r")); cmd == nil {
		t.Error("tombol r harus menjalankan ulang query")
	}
}

type errKolomTest struct{}

func (errKolomTest) Error() string { return `ERROR:  column "pelangan" does not exist` }

func TestHasilQueryNilaiBerbarisBanyakTidakMerusakTabel(t *testing.T) {
	m := hasil(t, hasilUji(), nil)
	view := ansi.Strip(m.View(120, 30))
	// Di tabel, baris baru dipipihkan jadi penanda supaya kolom tetap sejajar.
	if !strings.Contains(view, "alamat: ↵ Jl. Merdeka 10") {
		t.Errorf("baris baru tidak dipipihkan:\n%s", view)
	}
	// Satu baris data harus tetap satu baris layar: cari baris yang diawali isi lanjutan.
	for _, line := range strings.Split(view, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Jl. Merdeka") {
			t.Errorf("isi sel tumpah ke baris berikutnya:\n%s", view)
		}
	}
	// Di detail, isi panjang dilipat mengikuti lebar layar — bukan dipotong.
	d := newRowDetail(m.res.Columns, m.res.Rows[2], 3)
	narrow := ansi.Strip(d.View(46, 30))
	if !strings.Contains(narrow, "Jl. Merdeka 10") || strings.Contains(narrow, "…") {
		t.Errorf("detail di layar sempit:\n%s", narrow)
	}
}
