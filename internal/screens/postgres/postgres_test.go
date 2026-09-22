package postgres

import (
	"strings"
	"testing"
	"time"

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

func TestQueryValidatorMemberiPeringatan(t *testing.T) {
	err := validQuery("DELETE FROM pesanan")
	if err == nil {
		t.Fatal("DELETE tanpa WHERE harus memberi peringatan")
	}
	var w *ask.Warning
	if !strings.Contains(err.Error(), "WHERE") {
		t.Errorf("peringatan = %v", err)
	}
	if _, ok := err.(*ask.Warning); !ok {
		_ = w
		t.Errorf("peringatan harus tidak memblokir (ask.Warning), dapat %T", err)
	}
	if err := validQuery("SELECT 1"); err != nil {
		t.Errorf("query baca ditolak: %v", err)
	}
	if err := validQuery("   "); err == nil {
		t.Error("query kosong harus ditolak")
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
