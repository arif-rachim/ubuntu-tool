package postgres

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// identRe membatasi nama database/role yang boleh dibuat ubt: huruf kecil, angka, garis bawah.
// Nama seperti "Data Saya" sah di PostgreSQL tetapi merepotkan (harus selalu dikutip), jadi ditolak.
var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// reservedNames adalah nama yang tidak boleh dipakai untuk database/role baru.
var reservedNames = map[string]bool{"postgres": true, "template0": true, "template1": true, "public": true}

// ValidIdent memeriksa nama database atau role.
func ValidIdent(s string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return errors.New("nama wajib diisi")
	case len(s) > 63:
		return errors.New("maksimal 63 karakter")
	case !identRe.MatchString(s):
		return errors.New("gunakan huruf kecil, angka, dan garis bawah; diawali huruf, mis. toko_online")
	case reservedNames[s]:
		return errors.New("nama " + s + " sudah dipakai PostgreSQL sendiri; pilih nama lain")
	}
	return nil
}

// QuoteIdent mengutip nama objek untuk dipakai di dalam SQL.
func QuoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// QuoteLiteral mengutip nilai teks untuk dipakai di dalam SQL.
func QuoteLiteral(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

// cmd membangun satu command lengkap dengan penjelasan.
func cmd(title string, lvl risk.Level, effect string, explain []run.Line, c run.Command) run.Command {
	c.Title, c.Risk, c.Effect, c.Explain = title, lvl, effect, explain
	return c
}

// InstallPlan memasang PostgreSQL dari repository Ubuntu.
func InstallPlan() run.Plan {
	return run.Plan{Title: "Install PostgreSQL", Steps: []run.Command{
		{Title: "Install server & alat bantu PostgreSQL", Argv: []string{"apt-get", "install", "-y", "postgresql", "postgresql-contrib"}, NeedsRoot: true, Risk: risk.Caution,
			Explain: []run.Line{
				{Token: "postgresql", Meaning: "server database versi bawaan Ubuntu ini, beserta perkakas psql/pg_dump"},
				{Token: "postgresql-contrib", Meaning: "ekstensi resmi tambahan, mis. pg_stat_statements untuk melihat query paling berat"},
			},
			Effect: "Satu cluster bernama \"main\" langsung dibuat dan berjalan di port 5432, hanya menerima koneksi dari server ini. " +
				"User sistem `postgres` menjadi pemiliknya."},
	}}
}

// ServiceUnit mengembalikan unit systemd cluster (dipakai modul Service).
func ServiceUnit(c Cluster) string { return c.Unit() }

// StartClusterPlan menyalakan cluster.
func StartClusterPlan(c Cluster) run.Plan {
	return run.Single(run.Command{
		Title: "Jalankan cluster " + c.ID(), Argv: []string{"systemctl", "enable", "--now", c.Unit()}, NeedsRoot: true, Risk: risk.Caution,
		Explain: []run.Line{
			{Token: "systemctl enable --now", Meaning: "jalankan sekarang dan otomatis setiap boot"},
			{Token: c.Unit(), Meaning: "unit systemd untuk cluster " + c.ID()},
		},
		Effect: "Bila gagal, alasannya ada di log cluster: " + c.LogFile,
	})
}

// RestartClusterPlan me-restart cluster (dibutuhkan setelah mengubah parameter tertentu).
func RestartClusterPlan(c Cluster) run.Plan {
	return run.Single(run.Command{
		Title: "Restart cluster " + c.ID(), Argv: []string{"systemctl", "restart", c.Unit()}, NeedsRoot: true, Risk: risk.Dangerous,
		Explain: []run.Line{{Token: "systemctl restart", Meaning: "hentikan lalu jalankan lagi server database"}},
		Effect:  "SEMUA koneksi aplikasi ke database ini terputus selama beberapa detik. Transaksi yang sedang berjalan dibatalkan.",
		Safer:   "Bila perubahan yang kamu buat hanya butuh reload, pakai reload: koneksi tidak terputus.",
	})
}

// ReloadClusterPlan meminta server membaca ulang konfigurasi tanpa memutus koneksi.
func ReloadClusterPlan(c Cluster) run.Plan {
	return run.Single(run.Command{
		Title: "Muat ulang konfigurasi " + c.ID(), Argv: []string{"systemctl", "reload", c.Unit()}, NeedsRoot: true, Risk: risk.Safe,
		Explain: []run.Line{{Token: "systemctl reload", Meaning: "server membaca ulang postgresql.conf & pg_hba.conf tanpa berhenti"}},
		Effect:  "Koneksi yang sedang berjalan tidak terputus. Parameter yang butuh restart tetap belum berlaku.",
	})
}

// --- database ---------------------------------------------------------------------------------------

// CreateDatabasePlan membuat database baru.
func (c Client) CreateDatabasePlan(name, owner string) run.Plan {
	argv := []string{"createdb", "-E", "UTF8"}
	explain := []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres yang berhak membuat database"},
		{Token: "createdb", Meaning: "pembungkus resmi untuk perintah SQL CREATE DATABASE"},
		{Token: "-E UTF8", Meaning: "encoding UTF-8 supaya semua huruf & emoji tersimpan benar"},
	}
	if owner != "" {
		argv = append(argv, "-O", owner)
		explain = append(explain, run.Line{Token: "-O " + owner, Meaning: "role pemilik database; pemilik boleh membuat & menghapus tabel di dalamnya"})
	}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, name)
	explain = append(explain, run.Line{Token: name, Meaning: "nama database baru"})
	return run.Single(cmd("Buat database "+name, risk.Safe,
		"Database kosong siap dipakai. Aplikasi menyambungnya dengan: postgresql://USER@localhost/"+name,
		explain, asPostgres(argv...)))
}

// DropDatabasePlan menghapus database beserta seluruh isinya.
func (c Client) DropDatabasePlan(name string) run.Plan {
	argv := []string{"dropdb", "--if-exists"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, name)
	out := cmd("Hapus database "+name, risk.Dangerous,
		"SEMUA tabel dan data di dalam "+name+" hilang permanen. Tidak ada tong sampah dan tidak bisa dibatalkan.",
		[]run.Line{
			{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
			{Token: "dropdb", Meaning: "pembungkus resmi untuk DROP DATABASE"},
			{Token: "--if-exists", Meaning: "tidak error bila databasenya memang sudah tidak ada"},
			{Token: name, Meaning: "database yang dihapus"},
		}, asPostgres(argv...))
	out.Safer = "Backup dulu lewat menu backup (tombol b): satu file dump bisa dipulihkan kapan saja."
	return run.Single(out)
}

// --- role -------------------------------------------------------------------------------------------

// RoleOptions adalah pilihan hak untuk role baru.
type RoleOptions struct {
	Login      bool
	CreateDB   bool
	CreateRole bool
	Superuser  bool
}

// CreateRolePlan membuat role baru. Password diketik langsung ke createuser (interaktif) sehingga
// tidak pernah lewat ubt maupun tercatat di riwayat.
func (c Client) CreateRolePlan(name string, o RoleOptions, withPassword bool) run.Plan {
	argv := []string{"createuser"}
	explain := []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
		{Token: "createuser", Meaning: "pembungkus resmi untuk perintah SQL CREATE ROLE"},
	}
	add := func(flag, meaning string) {
		argv = append(argv, flag)
		explain = append(explain, run.Line{Token: flag, Meaning: meaning})
	}
	if o.Login {
		add("--login", "role ini boleh dipakai untuk konek (jadi \"user\"); tanpa ini role hanya jadi wadah hak akses")
	} else {
		add("--no-login", "role tidak bisa dipakai konek; gunanya menampung hak akses lalu diberikan ke user lain")
	}
	if o.CreateDB {
		add("--createdb", "boleh membuat database baru")
	}
	if o.CreateRole {
		add("--createrole", "boleh membuat & mengubah role lain")
	}
	if o.Superuser {
		add("--superuser", "HAK PENUH atas seluruh server database, melewati semua pemeriksaan hak akses")
	}
	if withPassword {
		add("--pwprompt", "minta password langsung di terminal ini; ubt tidak melihat dan tidak menyimpannya")
	}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, name)
	explain = append(explain, run.Line{Token: name, Meaning: "nama role baru"})

	lvl := risk.Safe
	effect := "Role baru bisa dipakai setelah diberi hak akses ke database yang dituju."
	if o.Superuser {
		lvl = risk.Dangerous
		effect = "Role ini bisa membaca, mengubah, dan menghapus APA PUN di server database, termasuk menghapus database lain."
	}
	out := cmd("Buat role "+name, lvl, effect, explain, asPostgres(argv...))
	out.Interactive = withPassword
	if o.Superuser {
		out.Safer = "Untuk aplikasi, buat role biasa lalu beri hak seperlunya ke satu database saja."
	}
	return run.Single(out)
}

// SetPasswordPlan mengganti password role lewat perintah \password psql, yang mengirim password
// sudah dalam bentuk hash — jadi password aslinya tidak pernah masuk ke log server maupun riwayat ubt.
func (c Client) SetPasswordPlan(role string) run.Plan {
	argv := append(c.psqlArgs(""), "-c", `\password `+QuoteIdent(role))
	out := cmd("Ganti password role "+role, risk.Caution,
		"Aplikasi yang masih memakai password lama akan gagal konek sampai diperbarui.",
		[]run.Line{
			{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
			{Token: `\password ` + role, Meaning: "psql meminta password dua kali di terminal, lalu mengirimkannya ke server sebagai hash (bukan teks polos)"},
		}, asPostgres(argv...))
	out.Interactive = true
	return run.Single(out)
}

// DropRolePlan menghapus role.
func (c Client) DropRolePlan(name string) run.Plan {
	argv := []string{"dropuser", "--if-exists"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, name)
	out := cmd("Hapus role "+name, risk.Dangerous,
		"Aplikasi yang konek memakai role ini langsung gagal login. PostgreSQL menolak bila role masih memiliki objek (tabel/database).",
		[]run.Line{
			{Token: "dropuser", Meaning: "pembungkus resmi untuk DROP ROLE"},
			{Token: "--if-exists", Meaning: "tidak error bila role memang sudah tidak ada"},
			{Token: name, Meaning: "role yang dihapus"},
		}, asPostgres(argv...))
	out.Safer = "Bila hanya ingin memblokir sementara: cabut hak login-nya, datanya tetap utuh."
	return run.Single(out)
}

// --- hak akses --------------------------------------------------------------------------------------

// Tingkat akses yang ditawarkan wizard.
const (
	AccessRead  = "read"  // hanya SELECT
	AccessWrite = "write" // SELECT, INSERT, UPDATE, DELETE
	AccessOwner = "owner" // pemilik database: bebas membuat & menghapus tabel
)

// sqlStep membangun satu langkah psql yang menjalankan perintah SQL.
func (c Client) sqlStep(db, title, sql, meaning, effect string, lvl risk.Level) run.Command {
	argv := append(c.psqlArgs(db), "-c", sql)
	return cmd(title, lvl, effect, []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
		{Token: "psql -d " + db + " -c …", Meaning: "jalankan satu perintah SQL di database " + db},
		{Token: sql, Meaning: meaning},
	}, asPostgres(argv...))
}

// GrantPlan memberi hak akses satu role ke satu database.
func (c Client) GrantPlan(db, role, level string) run.Plan {
	r, d := QuoteIdent(role), QuoteIdent(db)
	steps := []run.Command{
		c.sqlStep("postgres", "Izinkan "+role+" menyambung ke "+db,
			"GRANT CONNECT ON DATABASE "+d+" TO "+r+";",
			"tanpa ini, role bahkan tidak bisa membuka koneksi ke database tersebut",
			"Role sudah boleh konek, tetapi belum boleh membaca tabel apa pun.", risk.Safe),
		c.sqlStep(db, "Izinkan "+role+" memakai skema public",
			"GRANT USAGE ON SCHEMA public TO "+r+";",
			"skema adalah \"folder\" tempat tabel berada; USAGE artinya boleh melihat isinya",
			"Role bisa melihat daftar tabel di skema public.", risk.Safe),
	}
	switch level {
	case AccessOwner:
		return run.Plan{Title: "Jadikan " + role + " pemilik database " + db, Steps: []run.Command{
			c.sqlStep("postgres", "Pindahkan kepemilikan "+db+" ke "+role,
				"ALTER DATABASE "+d+" OWNER TO "+r+";",
				"pemilik database boleh membuat, mengubah, dan menghapus apa pun di dalamnya",
				"Role ini menjadi pemilik penuh database "+db+". Cocok untuk satu aplikasi yang punya databasenya sendiri.", risk.Caution),
			c.sqlStep(db, "Pindahkan kepemilikan skema public ke "+role,
				"ALTER SCHEMA public OWNER TO "+r+";",
				"sejak PostgreSQL 15, skema public tidak lagi bisa ditulis semua orang; pemiliknya perlu diatur",
				"Role bisa membuat tabel baru di database ini.", risk.Caution),
		}}
	case AccessWrite:
		steps = append(steps,
			c.sqlStep(db, "Izinkan "+role+" membaca & mengubah data",
				"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO "+r+";",
				"hak pada semua tabel yang ADA SEKARANG di skema public",
				"Berlaku untuk tabel yang sudah ada saat ini.", risk.Caution),
			c.sqlStep(db, "Berlaku juga untuk tabel yang dibuat nanti",
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "+r+";",
				"aturan bawaan untuk tabel yang dibuat SETELAH ini, supaya tidak perlu grant ulang tiap ada tabel baru",
				"Tabel baru otomatis bisa diakses role ini (berlaku untuk tabel yang dibuat oleh role yang menjalankan perintah ini).", risk.Caution),
			c.sqlStep(db, "Izinkan memakai sequence (kolom id otomatis)",
				"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO "+r+";",
				"INSERT ke tabel ber-kolom id otomatis butuh hak pada sequence-nya",
				"Tanpa ini, INSERT ke tabel dengan kolom serial/identity akan ditolak.", risk.Safe))
	default:
		steps = append(steps,
			c.sqlStep(db, "Izinkan "+role+" membaca semua tabel",
				"GRANT SELECT ON ALL TABLES IN SCHEMA public TO "+r+";",
				"hanya SELECT: role tidak bisa mengubah apa pun",
				"Role bisa membaca data di tabel yang ada sekarang.", risk.Safe),
			c.sqlStep(db, "Berlaku juga untuk tabel yang dibuat nanti",
				"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO "+r+";",
				"aturan bawaan untuk tabel yang dibuat SETELAH ini",
				"Tabel baru otomatis bisa dibaca role ini.", risk.Safe))
	}
	return run.Plan{Title: "Beri akses " + level + " untuk " + role + " di " + db, Steps: steps}
}

// RevokePlan mencabut seluruh hak satu role di satu database.
func (c Client) RevokePlan(db, role string) run.Plan {
	r, d := QuoteIdent(role), QuoteIdent(db)
	return run.Plan{Title: "Cabut akses " + role + " dari " + db, Steps: []run.Command{
		c.sqlStep(db, "Cabut hak pada semua tabel",
			"REVOKE ALL ON ALL TABLES IN SCHEMA public FROM "+r+";",
			"hapus semua hak baca/tulis role ini pada tabel yang ada",
			"Role langsung kehilangan akses ke tabel; koneksi yang sedang berjalan bisa gagal di query berikutnya.", risk.Caution),
		c.sqlStep(db, "Cabut hak pada skema public",
			"REVOKE ALL ON SCHEMA public FROM "+r+";", "hapus hak memakai skema", "", risk.Caution),
		c.sqlStep("postgres", "Cabut hak menyambung ke "+db,
			"REVOKE CONNECT ON DATABASE "+d+" FROM "+r+";",
			"role tidak bisa lagi membuka koneksi baru ke database ini",
			"Koneksi yang sedang terbuka tidak langsung putus; yang baru akan ditolak.", risk.Caution),
	}}
}

// --- psql & query -----------------------------------------------------------------------------------

// PsqlPlan membuka psql interaktif di satu database.
func (c Client) PsqlPlan(db string) run.Plan {
	argv := []string{"psql"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	if db != "" {
		argv = append(argv, "-d", db)
	}
	out := cmd("Buka psql di database "+db, risk.Caution,
		"Kamu masuk ke shell SQL sebagai superuser postgres — perintah apa pun di sini langsung berlaku tanpa konfirmasi ubt. Ketik \\q untuk keluar.",
		[]run.Line{
			{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres (superuser database)"},
			{Token: "psql -d " + db, Meaning: "shell SQL interaktif di database " + db},
			{Token: "\\dt", Meaning: "di dalam psql: daftar tabel. \\l daftar database, \\du daftar role, \\q keluar"},
		}, asPostgres(argv...))
	out.Interactive = true
	out.Safer = "Untuk sekadar melihat data, pakai menu query (tombol q) yang menjalankannya dalam transaksi hanya-baca."
	return run.Single(out)
}

// Jenis query hasil klasifikasi.
const (
	QueryRead  = "read"
	QueryWrite = "write"
	QueryDDL   = "ddl"
)

var (
	readPrefix  = []string{"select", "with", "show", "explain", "table", "values"}
	ddlPrefix   = []string{"drop", "truncate", "alter", "create", "grant", "revoke", "reindex", "vacuum", "cluster"}
	writePrefix = []string{"insert", "update", "delete", "merge", "copy", "refresh"}
)

// ClassifyQuery menebak jenis query dan memberi peringatan untuk pola yang sering bikin celaka.
func ClassifyQuery(sql string) (kind, warning string) {
	s := strings.ToLower(strings.TrimSpace(sql))
	first, _, _ := strings.Cut(s, " ")
	kind = QueryRead
	switch {
	case hasPrefix(first, ddlPrefix):
		kind = QueryDDL
	case hasPrefix(first, writePrefix):
		kind = QueryWrite
	case !hasPrefix(first, readPrefix):
		kind = QueryWrite // tidak dikenali: perlakukan sebagai perubahan
	}
	switch {
	case (strings.HasPrefix(s, "update") || strings.HasPrefix(s, "delete")) && !strings.Contains(s, " where "):
		warning = "Query ini tidak punya WHERE, jadi berlaku untuk SEMUA baris di tabel."
	case strings.HasPrefix(s, "drop"):
		warning = "DROP menghapus objek beserta isinya dan tidak bisa dibatalkan."
	case strings.HasPrefix(s, "truncate"):
		warning = "TRUNCATE mengosongkan seluruh isi tabel dan tidak bisa dibatalkan."
	}
	return kind, warning
}

func hasPrefix(word string, set []string) bool {
	for _, p := range set {
		if word == p {
			return true
		}
	}
	return false
}

// QueryPlan menjalankan satu query yang diketik user. Query baca dibungkus transaksi READ ONLY,
// sehingga perintah yang ternyata mengubah data akan ditolak server, bukan diam-diam berjalan.
func (c Client) QueryPlan(db, sql string) run.Plan {
	kind, warning := ClassifyQuery(sql)
	sql = strings.TrimSpace(sql)
	// Berbeda dari pembacaan internal ubt, hasil query milik user ditampilkan lengkap dengan nama kolom.
	argv := []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "--single-transaction", "-P", "pager=off"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, "-d", db)
	explain := []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
		{Token: "psql -d " + db, Meaning: "jalankan query di database " + db},
		{Token: "--single-transaction", Meaning: "semua perintah di bawah dijalankan dalam satu transaksi; bila ada yang gagal, semuanya dibatalkan"},
	}
	lvl, effect := risk.Caution, "Perubahan yang dilakukan query ini langsung berlaku setelah transaksi selesai."
	if kind == QueryRead {
		argv = append(argv, "-c", "SET TRANSACTION READ ONLY")
		explain = append(explain, run.Line{Token: "SET TRANSACTION READ ONLY", Meaning: "kunci pengaman: server akan MENOLAK perintah apa pun yang mengubah data di transaksi ini"})
		lvl, effect = risk.Safe, "Tidak mengubah apa pun: transaksi dikunci hanya-baca."
	}
	argv = append(argv, "-c", sql)
	explain = append(explain, run.Line{Token: sql, Meaning: "query yang kamu tulis"})
	if kind == QueryDDL {
		lvl = risk.Dangerous
		effect = "Query ini mengubah struktur database (tabel, index, hak akses), bukan sekadar isinya."
	}
	out := cmd("Jalankan query di "+db, lvl, effect, explain, asPostgres(argv...))
	if warning != "" {
		out.Effect = warning + " " + out.Effect
		out.Risk = risk.Dangerous
		out.Safer = "Coba dulu sebagai SELECT dengan WHERE yang sama untuk melihat baris mana yang akan terkena."
	}
	return run.Single(out)
}

// TerminatePlan menghentikan satu koneksi yang bermasalah.
func (c Client) TerminatePlan(pid int, who string) run.Plan {
	sql := "SELECT pg_terminate_backend(" + strconv.Itoa(pid) + ");"
	out := c.sqlStep("postgres", "Putuskan koneksi PID "+strconv.Itoa(pid),
		sql, "putuskan satu koneksi; transaksi yang sedang berjalan di koneksi itu dibatalkan (rollback)",
		"Aplikasi pemilik koneksi ("+who+") akan melihat error koneksi terputus dan biasanya menyambung lagi. Data yang belum di-commit hilang.", risk.Dangerous)
	out.Safer = "Bila hanya ingin menghentikan query-nya tanpa memutus koneksi, pakai pg_cancel_backend(" + strconv.Itoa(pid) + ")."
	return run.Single(out)
}

// CancelPlan membatalkan query yang sedang berjalan tanpa memutus koneksinya.
func (c Client) CancelPlan(pid int) run.Plan {
	sql := "SELECT pg_cancel_backend(" + strconv.Itoa(pid) + ");"
	return run.Single(c.sqlStep("postgres", "Batalkan query di PID "+strconv.Itoa(pid),
		sql, "hentikan query yang sedang berjalan; koneksinya tetap terbuka",
		"Aplikasi menerima error \"canceling statement due to user request\" dan bisa mencoba lagi.", risk.Caution))
}

// CreateExtensionPlan memasang ekstensi di satu database.
func (c Client) CreateExtensionPlan(db, ext string) run.Plan {
	return run.Single(c.sqlStep(db, "Pasang ekstensi "+ext+" di "+db,
		"CREATE EXTENSION IF NOT EXISTS "+QuoteIdent(ext)+";",
		"aktifkan fungsi tambahan bawaan paket postgresql-contrib",
		"Ekstensi siap dipakai di database "+db+".", risk.Caution))
}

// EnableStatementsPlan menyiapkan pg_stat_statements: butuh shared_preload_libraries + restart.
func EnableStatementsPlan(cl Cluster) run.Plan {
	path := cl.DropIn(DropInStatements)
	content := "# Dibuat oleh ubt: rekam statistik query\nshared_preload_libraries = 'pg_stat_statements'\npg_stat_statements.max = 10000\npg_stat_statements.track = top\n"
	return run.Plan{Title: "Aktifkan pencatat query pg_stat_statements", Steps: []run.Command{
		{Title: "Tulis " + path, Argv: []string{"install", "-D", "-m", "0644", "-o", SuperUser, "-g", SuperUser, "/dev/stdin", path}, NeedsRoot: true,
			Stdin: content, StdinLabel: "(konfigurasi tambahan)", Risk: risk.Caution,
			Explain: []run.Line{
				{Token: "install -D -m 0644 -o postgres", Meaning: "tulis berkas konfigurasi tambahan milik postgres (postgresql.conf bawaan tidak diubah)"},
				{Token: "shared_preload_libraries", Meaning: "pustaka yang dimuat saat server start; karena itu perlu restart, bukan reload"},
			},
			Effect: "Berkas ini dibaca lewat include_dir 'conf.d' di postgresql.conf bawaan Ubuntu."},
		{Title: "Restart cluster " + cl.ID(), Argv: []string{"systemctl", "restart", cl.Unit()}, NeedsRoot: true, Risk: risk.Dangerous,
			Explain: []run.Line{{Token: "systemctl restart", Meaning: "shared_preload_libraries hanya dibaca saat server start"}},
			Effect:  "SEMUA koneksi aplikasi terputus selama beberapa detik.",
			Safer:   "Lakukan di jam sepi; aplikasi biasanya menyambung sendiri setelahnya."},
	}}
}
