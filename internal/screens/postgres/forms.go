package postgres

import (
	"strconv"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// options mengubah daftar nama menjadi pilihan.
func options(names []string, desc func(string) string) []ask.Option {
	out := make([]ask.Option, 0, len(names))
	for _, n := range names {
		o := ask.Option{Value: n, Label: n}
		if desc != nil {
			o.Description = desc(n)
		}
		out = append(out, o)
	}
	return out
}

// DatabaseForm adalah wizard membuat database.
func DatabaseForm(roles []string) ask.Form {
	return ask.Form{ID: "db-add", Title: "Buat database", Questions: []ask.Question{
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama database baru?", Placeholder: "toko_online", Validate: syspg.ValidIdent,
			Help: "Pakai huruf kecil dan garis bawah. Nama dengan huruf besar atau spasi memaksa semua query menulisnya dalam tanda kutip ganda."},
		{ID: "owner", Header: "Pemilik", Kind: ask.Single, Optional: true, Other: true, Options: options(roles, func(string) string {
			return "role ini menjadi pemilik: boleh membuat, mengubah, dan menghapus tabel di dalamnya"
		}), Prompt: "Role mana yang jadi pemiliknya?", Validate: syspg.ValidIdent,
			Help: "Idealnya satu aplikasi punya satu role sendiri sebagai pemilik satu database. Kosongkan bila belum ada rolenya — pemiliknya menjadi postgres dan bisa dipindahkan nanti lewat menu akses."},
	}}
}

// DatabaseActionForm menawarkan aksi untuk satu database.
func DatabaseActionForm(d syspg.Database, roles []string) ask.Form {
	protected := ""
	if d.Name == "postgres" {
		protected = "database bawaan yang dipakai perkakas PostgreSQL sendiri"
	}
	return ask.Form{ID: "db-action", Title: d.Name, SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan database " + d.Name + "?",
		Options: []ask.Option{
			{Value: "tables", Label: "Lihat tabel & ukurannya", Description: "Tabel terbesar, jumlah baris, sampah hasil UPDATE/DELETE, dan kapan terakhir di-vacuum.", Recommended: true},
			{Value: "query", Label: "Jalankan query", Description: "Query baca dijalankan dalam transaksi hanya-baca, jadi tidak mungkin mengubah data tanpa sengaja."},
			{Value: "psql", Label: "Buka psql interaktif", Description: "Shell SQL penuh sebagai superuser. Di dalamnya ubt tidak bisa lagi menjaga.", Risk: risk.Caution},
			{Value: "grant", Label: "Beri akses ke role", Description: "Wizard hak akses: hanya baca, baca-tulis, atau jadikan pemilik."},
			{Value: "backup", Label: "Cadangkan / pulihkan", Description: "pg_dump & pg_restore, termasuk jadwal cadangan otomatis."},
			{Value: "ext", Label: "Pasang ekstensi", Description: "Fungsi tambahan seperti pgcrypto, uuid-ossp, atau pg_trgm untuk pencarian teks."},
			{Value: "drop", Label: "Hapus database", Description: "Seluruh tabel dan isinya hilang permanen.", Disabled: protected, Risk: risk.Dangerous},
		},
	}}}
}

// GrantForm adalah wizard memberi hak akses satu role ke satu database.
func GrantForm(db string, roles []string) ask.Form {
	return ask.Form{ID: "db-grant", Title: "Beri akses ke " + db, Questions: []ask.Question{
		{ID: "role", Header: "Role", Kind: ask.Single, Other: true, Options: options(roles, nil), Validate: syspg.ValidIdent,
			Prompt: "Role mana yang diberi akses?",
			Help:   "Belum ada rolenya? Buat dulu lewat menu role & akses (tombol u di layar PostgreSQL)."},
		{ID: "level", Header: "Tingkat", Kind: ask.Single, Prompt: "Sampai mana role ini boleh?", Options: []ask.Option{
			{Value: syspg.AccessRead, Label: "Hanya baca (SELECT)", Description: "Untuk laporan, dashboard, atau analis. Tidak bisa mengubah data sama sekali.", Recommended: true},
			{Value: syspg.AccessWrite, Label: "Baca & tulis data", Description: "SELECT, INSERT, UPDATE, DELETE pada tabel yang ada dan yang dibuat nanti. Untuk aplikasi yang tabelnya dikelola orang lain."},
			{Value: syspg.AccessOwner, Label: "Pemilik penuh", Description: "Boleh membuat & menghapus tabel. Untuk aplikasi yang membawa migrasi skemanya sendiri (Rails, Django, Prisma).", Risk: risk.Caution},
		}},
	}}
}

// inputError adalah pesan validasi sederhana untuk pertanyaan wizard.
type inputError string

func (e inputError) Error() string { return string(e) }

// ExtensionForm menanyakan ekstensi yang akan dipasang.
func ExtensionForm(db string) ask.Form {
	return ask.Form{ID: "db-ext", Title: "Pasang ekstensi di " + db, SkipReview: true, Questions: []ask.Question{
		{ID: "ext", Header: "Ekstensi", Kind: ask.Single, Other: true, Prompt: "Ekstensi mana yang dipasang?", Validate: syspg.ValidIdent,
			Help: "Ekstensi menambah fungsi ke satu database. Yang tersedia berasal dari paket postgresql-contrib. " +
				"Lihat daftar lengkapnya di dalam psql dengan: SELECT name, comment FROM pg_available_extensions;",
			Options: []ask.Option{
				{Value: "pg_trgm", Label: "pg_trgm", Description: "Pencarian teks mirip (fuzzy) dan LIKE '%kata%' yang bisa memakai index.", Recommended: true},
				{Value: "pgcrypto", Label: "pgcrypto", Description: "Fungsi enkripsi & hash, mis. untuk menyimpan password aplikasi."},
				{Value: "uuid-ossp", Label: "uuid-ossp", Description: "Pembuat UUID sebagai id baris, alternatif angka berurutan."},
				{Value: "unaccent", Label: "unaccent", Description: "Abaikan tanda diakritik saat mencari, mis. \"José\" cocok dengan \"Jose\"."},
			}},
	}}
}

// RoleForm adalah wizard membuat role.
func RoleForm() ask.Form {
	isLogin := func(a ask.Answers) bool { return a["kind"].Value() != "group" }
	return ask.Form{ID: "role-add", Title: "Buat role", Questions: []ask.Question{
		{ID: "name", Header: "Nama", Kind: ask.Text, Prompt: "Nama role baru?", Placeholder: "app_toko", Validate: syspg.ValidIdent,
			Help: "Role adalah user database, berbeda dari user Linux. Buat satu role per aplikasi supaya mudah dicabut bila kredensialnya bocor."},
		{ID: "kind", Header: "Jenis", Kind: ask.Single, Prompt: "Role ini untuk apa?", Options: []ask.Option{
			{Value: "app", Label: "User aplikasi", Description: "Bisa login dengan password, lalu diberi hak akses ke satu database.", Recommended: true},
			{Value: "human", Label: "User orang", Description: "Untuk orang yang menyambung dengan psql atau aplikasi GUI."},
			{Value: "group", Label: "Grup hak akses", Description: "Tidak bisa login sendiri; menampung hak akses lalu diberikan ke role lain."},
			{Value: "admin", Label: "Superuser", Description: "Hak penuh atas seluruh server database, melewati semua pemeriksaan hak akses.", Risk: risk.Dangerous},
		}},
		{ID: "password", Header: "Password", Kind: ask.Confirm, When: isLogin, Default: []string{ask.ValueYes},
			Prompt: "Set password sekarang?",
			Options: []ask.Option{
				{Label: "Ya, minta password", Description: "Terminal akan meminta password dua kali. ubt tidak melihat dan tidak menyimpannya.", Recommended: true},
				{Label: "Belum", Description: "Role dibuat tanpa password; belum bisa login lewat jaringan sampai passwordnya diatur."},
			}},
		{ID: "createdb", Header: "Buat DB", Kind: ask.Confirm, When: isLogin, Default: []string{ask.ValueNo},
			Prompt: "Boleh membuat database baru sendiri?",
			Options: []ask.Option{
				{Label: "Ya", Description: "Berguna untuk role yang menjalankan test atau membuat database sementara."},
				{Label: "Tidak", Description: "Sesuai prinsip hak seperlunya.", Recommended: true},
			}},
	}}
}

// RoleActionForm menawarkan aksi untuk satu role.
func RoleActionForm(r syspg.Role, dbs []string) ask.Form {
	protected := ""
	if r.Name == syspg.SuperUser {
		protected = "role bawaan pemilik cluster; menghapusnya membuat PostgreSQL tidak bisa dikelola"
	}
	return ask.Form{ID: "role-action", Title: r.Name, SkipReview: true, Questions: []ask.Question{{
		ID: "action", Header: "Aksi", Kind: ask.Single, Prompt: "Apa yang ingin dilakukan dengan role " + r.Name + "?",
		Options: []ask.Option{
			{Value: "password", Label: "Ganti password", Description: "Password dikirim ke server dalam bentuk hash, tidak pernah muncul di log maupun riwayat.", Recommended: true},
			{Value: "grant", Label: "Beri akses ke database", Description: "Pilih database dan tingkat akses: baca, baca-tulis, atau pemilik."},
			{Value: "revoke", Label: "Cabut akses dari database", Description: "Mencabut hak role ini pada satu database."},
			{Value: "drop", Label: "Hapus role", Description: "Aplikasi yang memakainya langsung gagal login. Ditolak bila role masih memiliki tabel atau database.", Disabled: protected, Risk: risk.Dangerous},
		},
	}}}
}

// DatabasePickForm menanyakan database untuk aksi role.
func DatabasePickForm(id, title, prompt string, dbs []string, withLevel bool) ask.Form {
	qs := []ask.Question{{
		ID: "db", Header: "Database", Kind: ask.Single, Other: true, Prompt: prompt, Validate: syspg.ValidIdent,
		Options: options(dbs, nil),
	}}
	if withLevel {
		qs = append(qs, ask.Question{ID: "level", Header: "Tingkat", Kind: ask.Single, Prompt: "Sampai mana role ini boleh?", Options: []ask.Option{
			{Value: syspg.AccessRead, Label: "Hanya baca (SELECT)", Description: "Untuk laporan & dashboard.", Recommended: true},
			{Value: syspg.AccessWrite, Label: "Baca & tulis data", Description: "Untuk aplikasi biasa."},
			{Value: syspg.AccessOwner, Label: "Pemilik penuh", Description: "Boleh membuat & menghapus tabel.", Risk: risk.Caution},
		}})
	}
	return ask.Form{ID: id, Title: title, Questions: qs}
}

// Nilai jawaban pertanyaan firewall di wizard akses jaringan.
const (
	FirewallFrom = "from" // izinkan hanya dari alamat yang sama dengan aturan pg_hba
	FirewallAny  = "any"  // izinkan dari mana saja
	FirewallNo   = "no"   // jangan sentuh firewall
)

// FirewallInfo adalah kondisi ufw yang dipakai wizard akses jaringan.
type FirewallInfo struct {
	Installed bool
	Active    bool
}

// RemoteAccessForm adalah wizard membuka akses PostgreSQL dari jaringan.
func RemoteAccessForm(dbs, roles []string, fw FirewallInfo, port int) ask.Form {
	needsCIDR := func(a ask.Answers) bool { return a["mode"].Value() != syspg.ListenLocal }
	noUfw := ""
	if !fw.Installed {
		noUfw = "ufw belum terpasang di server ini — pasang lewat modul Firewall"
	}
	fwHelp := "Docker dan PostgreSQL sama-sama tidak mengurus firewall sendiri. Tanpa aturan ufw, port " +
		strconv.Itoa(port) + " terbuka untuk siapa pun yang bisa mencapai server ini."
	if fw.Installed && !fw.Active {
		fwHelp += " Catatan: ufw terpasang tetapi BELUM AKTIF, jadi aturannya tersimpan dan baru berlaku setelah ufw dinyalakan (modul Firewall)."
	}
	return ask.Form{ID: "pg-remote", Title: "Akses dari jaringan", Questions: []ask.Question{
		{ID: "mode", Header: "Dari mana", Kind: ask.Single, Prompt: "Siapa yang perlu menyambung ke database ini?",
			Help: "Bawaan Ubuntu: PostgreSQL hanya menerima koneksi dari server ini (listen_addresses = localhost). " +
				"Aplikasi yang berjalan di server yang sama TIDAK perlu perubahan apa pun.",
			Options: []ask.Option{
				{Value: syspg.ListenLocal, Label: "Tetap hanya dari server ini", Description: "Cuma menambah aturan pg_hba untuk koneksi lokal. Paling aman.", Recommended: true},
				{Value: syspg.ListenSpecific, Label: "Dari alamat tertentu", Description: "Mis. satu server aplikasi di jaringan privat. Port 5432 mulai terbuka di jaringan.", Risk: risk.Caution},
				{Value: syspg.ListenAll, Label: "Dari mana saja yang bisa mencapai port 5432", Description: "Hanya bila kamu benar-benar paham risikonya dan sudah memasang firewall.", Risk: risk.Dangerous},
			}},
		{ID: "cidr", Header: "Alamat", Kind: ask.Text, When: needsCIDR, Prompt: "Alamat atau jaringan mana yang diizinkan?",
			Placeholder: "10.8.0.4/32", Validate: syspg.ValidCIDR,
			Help: "Tulis /32 untuk satu alamat (mis. 10.8.0.4/32), atau /24 untuk satu jaringan (mis. 192.168.1.0/24). " +
				"Makin sempit makin aman. 0.0.0.0/0 berarti seluruh internet."},
		{ID: "db", Header: "Database", Kind: ask.Single, Other: true, Prompt: "Boleh menyambung ke database mana?",
			Options: append([]ask.Option{{Value: "all", Label: "Semua database", Description: "Aturan berlaku untuk seluruh database di cluster ini."}}, options(dbs, nil)...),
			Help:    "Sebaiknya sebutkan satu database saja, sesuai aplikasi yang menyambung."},
		{ID: "role", Header: "Role", Kind: ask.Single, Other: true, Prompt: "Role mana yang boleh dipakai dari sana?",
			Options: append([]ask.Option{{Value: "all", Label: "Semua role", Description: "Termasuk superuser postgres — tidak disarankan dari jaringan.", Risk: risk.Dangerous}}, options(roles, nil)...),
			Help:    "Pilih role aplikasi, bukan postgres. Autentikasinya scram-sha-256, jadi role tersebut harus sudah punya password."},
		{ID: "firewall", Header: "Firewall", Kind: ask.Single, When: needsCIDR, Prompt: "Buka port " + strconv.Itoa(port) + " di firewall (ufw) juga?",
			Help: fwHelp, Options: []ask.Option{
				{Value: FirewallFrom, Label: "Ya, hanya dari alamat yang sama", Description: "ufw allow from ALAMAT to any port " + strconv.Itoa(port) + " proto tcp — sejalan dengan aturan pg_hba di atas.", Recommended: true, Disabled: noUfw},
				{Value: FirewallAny, Label: "Ya, dari mana saja", Description: "Port terbuka untuk semua alamat yang bisa mencapai server ini. Hanya untuk jaringan yang memang tertutup.", Risk: risk.Dangerous, Disabled: noUfw},
				{Value: FirewallNo, Label: "Tidak, saya atur sendiri", Description: "ubt tidak menyentuh firewall. Pakai ini bila portnya sudah dibuka, atau firewallnya di luar server (security group cloud).", Recommended: !fw.Installed},
			}},
	}}
}

// InstallForm menanyakan versi PostgreSQL yang dipasang.
func InstallForm() ask.Form {
	return ask.Form{ID: "pg-install", Title: "Install PostgreSQL", Questions: []ask.Question{
		{ID: "versi", Header: "Versi", Kind: ask.Single, Other: true, Validate: syspg.ValidMajor,
			Prompt: "Versi PostgreSQL mana yang dipasang?",
			Help: "Repository Ubuntu 24.04 hanya menyediakan PostgreSQL " + strconv.Itoa(syspg.UbuntuMajor) + ". " +
				"Versi yang lebih baru diambil dari apt.postgresql.org (PGDG), repository resmi tim PostgreSQL — " +
				"ubt menambahkannya memakai skrip resmi yang sudah ikut dalam paket postgresql-common Ubuntu, bukan dengan memasang kunci GPG manual. " +
				"Pilih \"Lainnya…\" untuk menulis nomor versi lain.",
			Options: []ask.Option{
				{Value: "ubuntu", Label: "Bawaan Ubuntu (PostgreSQL " + strconv.Itoa(syspg.UbuntuMajor) + ")",
					Description: "Paling sederhana: ikut dukungan keamanan Ubuntu sampai akhir masa dukungan rilis ini. Tanpa repository tambahan.", Recommended: true},
				{Value: "18", Label: "PostgreSQL 18 (PGDG)",
					Description: "Versi stabil terbaru: I/O asinkron, statistik per-backend, skip scan pada index. Menambahkan repository apt.postgresql.org.", Risk: risk.Caution},
				{Value: "17", Label: "PostgreSQL 17 (PGDG)",
					Description: "Satu versi di bawah yang terbaru. Menambahkan repository apt.postgresql.org.", Risk: risk.Caution},
			}},
	}}
}

// ClusterForm menanyakan cluster baru yang akan dibuat.
func ClusterForm(versions []string) ask.Form {
	return ask.Form{ID: "pg-cluster", Title: "Buat cluster", Questions: []ask.Question{
		{ID: "versi", Header: "Versi", Kind: ask.Single, Other: true, Options: options(versions, func(v string) string {
			return "paket PostgreSQL " + v + " terpasang di server ini"
		}), Prompt: "Cluster untuk versi PostgreSQL mana?", Validate: syspg.ValidMajor,
			Help: "Satu server bisa menjalankan beberapa versi berdampingan; masing-masing punya port dan direktori data sendiri."},
		{ID: "nama", Header: "Nama", Kind: ask.Text, Default: []string{"main"}, Validate: syspg.ValidIdent,
			Prompt: "Nama cluster?",
			Help:   "Bawaan Debian/Ubuntu adalah \"main\". Nama lain berguna bila kamu ingin beberapa cluster pada versi yang sama, mis. untuk uji coba."},
	}}
}

// ClusterPickForm menanyakan cluster mana yang ditampilkan layar PostgreSQL.
func ClusterPickForm(clusters []syspg.Cluster) ask.Form {
	opts := make([]ask.Option, 0, len(clusters))
	for _, c := range clusters {
		desc := "port " + strconv.Itoa(c.Port) + " · " + c.Status
		if !c.Online() {
			desc += " — jalankan dulu untuk melihat isinya"
		}
		opts = append(opts, ask.Option{Value: c.ID(), Label: "PostgreSQL " + c.ID(), Description: desc, Recommended: c.Online()})
	}
	return ask.Form{ID: "pg-pick", Title: "Pilih cluster", SkipReview: true, Questions: []ask.Question{
		{ID: "cluster", Header: "Cluster", Kind: ask.Single, Options: opts, Prompt: "Cluster mana yang dikelola?",
			Help: "Server ini menjalankan lebih dari satu cluster PostgreSQL. Semua aksi di layar berikutnya berlaku untuk cluster yang kamu pilih di sini."},
	}}
}
