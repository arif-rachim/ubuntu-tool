package postgres

import (
	"strings"
	"testing"
)

// skemaUji meniru database toko: dua tabel dan satu view, dengan kolom waktu, angka, teks, boolean.
func skemaUji() Schema {
	return Schema{Database: "toko", Relations: []Relation{
		{Schema: "public", Name: "pelanggan", Kind: "r", Rows: 120, Columns: []Column{
			{Name: "id", Type: "integer", NotNull: true, PK: true},
			{Name: "nama", Type: "text", NotNull: true},
			{Name: "email", Type: "character varying(120)"},
			{Name: "aktif", Type: "boolean", Default: "true"},
			{Name: "daftar_pada", Type: "timestamp with time zone", Default: "now()"},
		}},
		{Schema: "public", Name: "pesanan", Kind: "r", Rows: 5000, Columns: []Column{
			{Name: "id", Type: "bigint", NotNull: true, PK: true},
			{Name: "pelanggan_id", Type: "integer"},
			{Name: "total", Type: "numeric(12,2)", NotNull: true},
			{Name: "status", Type: "text"},
			{Name: "dibuat_pada", Type: "timestamp with time zone", NotNull: true},
			{Name: "tanggal_kirim", Type: "date"},
			{Name: "catatan", Type: "text"},
		}},
		{Schema: "penjualan", Name: "rekap", Kind: "v", Columns: []Column{
			{Name: "bulan", Type: "date"},
			{Name: "omzet", Type: "numeric"},
		}},
	}}
}

// at menjalankan Complete dengan kursor di posisi tanda | pada teks.
func at(t *testing.T, sql string) Completion {
	t.Helper()
	i := strings.Index(sql, "|")
	if i < 0 {
		t.Fatalf("teks uji harus memuat | sebagai posisi kursor: %q", sql)
	}
	clean := strings.Replace(sql, "|", "", 1)
	return Complete(skemaUji(), clean, len([]rune(clean[:i])))
}

func texts(c Completion) []string {
	var out []string
	for _, s := range c.Suggestions {
		out = append(out, s.Text)
	}
	return out
}

func has(c Completion, want string) bool {
	for _, s := range c.Suggestions {
		if s.Text == want {
			return true
		}
	}
	return false
}

func first(t *testing.T, c Completion) Suggestion {
	t.Helper()
	if len(c.Suggestions) == 0 {
		t.Fatalf("tidak ada saran (konteks %q)", c.Context)
	}
	return c.Suggestions[0]
}

func TestSaranAwalPerintah(t *testing.T) {
	c := at(t, "|")
	for _, want := range []string{"SELECT", "INSERT INTO", "UPDATE", "DELETE FROM"} {
		if !has(c, want) {
			t.Errorf("perintah %q tidak disarankan: %v", want, texts(c))
		}
	}
	// Template memakai tabel & kolom waktu yang benar-benar ada di database.
	var template string
	for _, s := range c.Suggestions {
		if s.Kind == KindSnippet && strings.Contains(s.Text, "WHERE") {
			template = s.Text
			break
		}
	}
	if !strings.Contains(template, "pelanggan") || !strings.Contains(template, "daftar_pada") {
		t.Errorf("template tidak memakai tabel/kolom nyata: %q", template)
	}
}

func TestSaranNamaTabel(t *testing.T) {
	c := at(t, "SELECT * FROM |")
	if c.Context != "nama tabel" {
		t.Errorf("konteks = %q", c.Context)
	}
	for _, want := range []string{"pelanggan", "pesanan", "penjualan.rekap"} {
		if !has(c, want) {
			t.Errorf("tabel %q tidak disarankan: %v", want, texts(c))
		}
	}
	// Tabel biasa lebih dulu daripada view.
	if got := first(t, c); got.Kind != KindTable || got.Text != "pelanggan" {
		t.Errorf("saran pertama = %+v", got)
	}
	// Skema selain public ikut awalan skemanya supaya query-nya benar.
	c = at(t, "SELECT * FROM rek|")
	if !has(c, "penjualan.rekap") {
		t.Errorf("view di skema lain: %v", texts(c))
	}
	// Penyaringan mengikuti huruf yang sudah diketik.
	c = at(t, "SELECT * FROM pesa|")
	if got := first(t, c); got.Text != "pesanan" {
		t.Errorf("filter \"pesa\" = %v", texts(c))
	}
	if has(c, "pelanggan") {
		t.Errorf("pelanggan tidak cocok dengan \"pesa\": %v", texts(c))
	}
	// Keterangan menyebut jumlah kolom & perkiraan baris supaya tabel mudah dikenali.
	for _, s := range c.Suggestions {
		if s.Text == "pesanan" && (!strings.Contains(s.Detail, "7 kolom") || !strings.Contains(s.Detail, "5000")) {
			t.Errorf("keterangan tabel = %q", s.Detail)
		}
	}
}

func TestSaranKolomSetelahTabelDikenal(t *testing.T) {
	c := at(t, "SELECT | FROM pesanan")
	for _, want := range []string{"id", "total", "dibuat_pada", "*", "count(*)"} {
		if !has(c, want) {
			t.Errorf("%q tidak disarankan di SELECT: %v", want, texts(c))
		}
	}
	// Kolom tabel lain tidak ikut muncul.
	if has(c, "email") {
		t.Errorf("kolom pelanggan bocor ke query pesanan: %v", texts(c))
	}
	// Kolom didahulukan daripada kata kunci.
	if got := first(t, c); got.Kind != KindColumn {
		t.Errorf("saran pertama harus kolom: %+v", got)
	}

	// WHERE juga menyarankan kolom.
	c = at(t, "SELECT * FROM pesanan WHERE |")
	if !has(c, "status") || !has(c, "dibuat_pada") {
		t.Errorf("kolom di WHERE: %v", texts(c))
	}
	// Tipe kolom ikut ditampilkan.
	for _, s := range c.Suggestions {
		if s.Text == "dibuat_pada" && !strings.Contains(s.Detail, "timestamp") {
			t.Errorf("keterangan kolom waktu = %q", s.Detail)
		}
		if s.Text == "id" && !strings.Contains(s.Detail, "kunci utama") {
			t.Errorf("kolom kunci utama tidak ditandai: %q", s.Detail)
		}
	}
}

func TestSaranKolomDenganAlias(t *testing.T) {
	c := at(t, "SELECT p.| FROM pesanan p")
	if c.Prefix != "" {
		t.Errorf("prefix = %q", c.Prefix)
	}
	if !has(c, "dibuat_pada") || has(c, "SELECT") {
		t.Errorf("alias p. harus memberi kolom pesanan saja: %v", texts(c))
	}
	// Alias dengan AS.
	if c := at(t, "SELECT x.| FROM pelanggan AS x"); !has(c, "email") {
		t.Errorf("alias AS: %v", texts(c))
	}
	// Nama tabel penuh juga bisa dipakai sebagai kualifikasi.
	if c := at(t, "SELECT pesanan.| FROM pesanan"); !has(c, "catatan") {
		t.Errorf("kualifikasi nama tabel: %v", texts(c))
	}
	// Dua tabel dengan JOIN: kolom keduanya tersedia, dan alias memilih yang tepat.
	c = at(t, "SELECT | FROM pesanan p JOIN pelanggan c ON c.id = p.pelanggan_id")
	if !has(c, "total") || !has(c, "email") {
		t.Errorf("kolom dua tabel: %v", texts(c))
	}
	c = at(t, "SELECT * FROM pesanan p JOIN pelanggan c ON c.| = p.pelanggan_id")
	if !has(c, "email") || has(c, "total") {
		t.Errorf("alias c. harus kolom pelanggan: %v", texts(c))
	}
}

// Inti permintaan: menyaring kolom waktu harus dituntun selengkap mungkin.
func TestSaranOperatorDanNilaiWaktu(t *testing.T) {
	// Setelah kolom waktu, operator rentang didahulukan.
	c := at(t, "SELECT * FROM pesanan WHERE dibuat_pada |")
	if !strings.Contains(c.Context, "dibuat_pada") {
		t.Errorf("konteks = %q", c.Context)
	}
	if got := first(t, c); got.Text != ">=" {
		t.Errorf("operator pertama untuk kolom waktu = %q, ingin >=", got.Text)
	}
	for _, want := range []string{">=", "<", "BETWEEN", "IS NULL", "IN ()"} {
		if !has(c, want) {
			t.Errorf("operator %q tidak ada: %v", want, texts(c))
		}
	}

	// Setelah operator, nilai waktu yang siap pakai.
	c = at(t, "SELECT * FROM pesanan WHERE dibuat_pada >= |")
	for _, want := range []string{
		"current_date", "now()", "now() - interval '1 day'", "now() - interval '7 days'",
		"now() - interval '30 days'", "date_trunc('day', now())", "date_trunc('month', now())",
		"TIMESTAMP '2026-01-31 00:00'", "DATE '2026-01-31'",
	} {
		if !has(c, want) {
			t.Errorf("nilai waktu %q tidak disarankan: %v", want, texts(c))
		}
	}
	// Kolom timestamptz mendapat saran zona waktu.
	if !has(c, "now() AT TIME ZONE 'Asia/Jakarta'") {
		t.Errorf("kolom bertimezone harus menyarankan AT TIME ZONE: %v", texts(c))
	}

	// BETWEEN menawarkan rentang lengkap sekaligus.
	c = at(t, "SELECT * FROM pesanan WHERE dibuat_pada BETWEEN |")
	if got := first(t, c); !strings.Contains(got.Text, "AND") {
		t.Errorf("BETWEEN harus menawarkan rentang: %q", got.Text)
	}

	// Kolom date (tanpa jam) menawarkan bentuk tanggal, bukan timestamp.
	c = at(t, "SELECT * FROM pesanan WHERE tanggal_kirim = |")
	if !has(c, "DATE '2026-01-31'") || has(c, "TIMESTAMP '2026-01-31 00:00'") {
		t.Errorf("kolom date: %v", texts(c))
	}
	if !has(c, "to_date('31-01-2026', 'DD-MM-YYYY')") {
		t.Errorf("kolom date harus menawarkan to_date: %v", texts(c))
	}

	// Operator dua karakter terbaca sebagai satu (">" + "=").
	if c := at(t, "SELECT * FROM pesanan WHERE dibuat_pada <= |"); !has(c, "now()") {
		t.Errorf("operator <=: %v", texts(c))
	}
}

func TestSaranNilaiPerTipe(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
		no   []string
	}{
		{"SELECT * FROM pelanggan WHERE aktif = |", []string{"true", "false", "NULL"}, []string{"now()"}},
		{"SELECT * FROM pesanan WHERE total > |", []string{"0"}, []string{"''"}},
		{"SELECT * FROM pesanan WHERE status = |", []string{"''", "NULL"}, []string{"true"}},
		{"SELECT * FROM pesanan WHERE status ILIKE |", []string{"'%kata%'", "'kata%'"}, nil},
	}
	for _, tc := range cases {
		c := at(t, tc.sql)
		for _, want := range tc.want {
			if !has(c, want) {
				t.Errorf("%q: %q tidak ada: %v", tc.sql, want, texts(c))
			}
		}
		for _, no := range tc.no {
			if has(c, no) {
				t.Errorf("%q: %q seharusnya tidak muncul: %v", tc.sql, no, texts(c))
			}
		}
	}
}

func TestSaranOperatorPerTipe(t *testing.T) {
	c := at(t, "SELECT * FROM pesanan WHERE status |")
	if got := first(t, c); got.Text != "ILIKE" {
		t.Errorf("kolom teks harus menawarkan ILIKE lebih dulu: %q", got.Text)
	}
	c = at(t, "SELECT * FROM pesanan WHERE total |")
	if got := first(t, c); got.Text != ">" {
		t.Errorf("kolom angka: %q", got.Text)
	}
	// Semua tipe tetap mendapat penanganan NULL, karena "= NULL" adalah jebakan klasik.
	for _, sql := range []string{"…WHERE status |", "…WHERE total |", "…WHERE dibuat_pada |"} {
		full := strings.Replace(sql, "…", "SELECT * FROM pesanan ", 1)
		if c := at(t, full); !has(c, "IS NULL") {
			t.Errorf("%q tidak menawarkan IS NULL", full)
		}
	}
}

func TestSaranInsertUpdateDelete(t *testing.T) {
	if c := at(t, "INSERT INTO |"); !has(c, "pesanan") {
		t.Errorf("INSERT INTO: %v", texts(c))
	}
	if c := at(t, "UPDATE |"); !has(c, "pelanggan") {
		t.Errorf("UPDATE: %v", texts(c))
	}
	if c := at(t, "DELETE FROM |"); !has(c, "pesanan") {
		t.Errorf("DELETE FROM: %v", texts(c))
	}
	// Setelah UPDATE tabel … SET, kolom tabel itu yang disarankan.
	c := at(t, "UPDATE pesanan SET |")
	if !has(c, "status") || has(c, "email") {
		t.Errorf("kolom SET: %v", texts(c))
	}
	// WHERE pada UPDATE tetap mengenali kolomnya.
	c = at(t, "UPDATE pesanan SET status = 'lunas' WHERE |")
	if !has(c, "id") || !has(c, "dibuat_pada") {
		t.Errorf("WHERE pada UPDATE: %v", texts(c))
	}
	// DELETE juga mengenali kolom tabelnya.
	c = at(t, "DELETE FROM pesanan WHERE tanggal_kirim <= |")
	if !has(c, "current_date") {
		t.Errorf("nilai pada DELETE: %v", texts(c))
	}
}

func TestSaranKataKunciLanjutan(t *testing.T) {
	c := at(t, "SELECT * FROM pesanan |")
	for _, want := range []string{"WHERE", "ORDER BY", "GROUP BY", "LIMIT", "LEFT JOIN"} {
		if !has(c, want) {
			t.Errorf("kata kunci %q tidak ada: %v", want, texts(c))
		}
	}
	if c := at(t, "SELECT * FROM pesanan ORDER |"); !has(c, "BY") {
		t.Errorf("setelah ORDER: %v", texts(c))
	}
	c = at(t, "SELECT * FROM pesanan ORDER BY dibuat_pada |")
	if !has(c, "DESC") || !has(c, "ASC") {
		t.Errorf("setelah kolom pengurut: %v", texts(c))
	}
	if c := at(t, "SELECT * FROM pesanan ORDER BY dibuat_pada DESC |"); !has(c, "LIMIT") {
		t.Errorf("setelah DESC: %v", texts(c))
	}
	if c := at(t, "INSERT |"); !has(c, "INTO") {
		t.Errorf("setelah INSERT: %v", texts(c))
	}
}

func TestSaranFungsi(t *testing.T) {
	c := at(t, "SELECT cou| FROM pesanan")
	if !has(c, "count(*)") {
		t.Errorf("fungsi count: %v", texts(c))
	}
	c = at(t, "SELECT date_| FROM pesanan")
	var trunc Suggestion
	for _, s := range c.Suggestions {
		if strings.HasPrefix(s.Text, "date_trunc") {
			trunc = s
		}
	}
	if trunc.Text == "" {
		t.Fatalf("date_trunc tidak disarankan: %v", texts(c))
	}
	// Kursor berhenti di dalam kurung supaya langsung bisa diisi kolomnya.
	if got := trunc.CursorAfterInsert(); got != 18 || string([]rune(trunc.Text)[got-1]) != " " {
		t.Errorf("posisi kursor date_trunc = %d pada %q", got, trunc.Text)
	}
}

func TestTidakMenyarankanDiDalamTeks(t *testing.T) {
	c := at(t, "SELECT * FROM pesanan WHERE status = 'lun|")
	if len(c.Suggestions) != 0 {
		t.Errorf("di dalam string tidak boleh ada saran: %v", texts(c))
	}
	if !strings.Contains(c.Context, "teks") {
		t.Errorf("konteks = %q", c.Context)
	}
	// String yang sudah ditutup tidak menghalangi saran berikutnya.
	c = at(t, "SELECT * FROM pesanan WHERE status = 'lunas' |")
	if !has(c, "ORDER BY") {
		t.Errorf("setelah string ditutup: %v", texts(c))
	}
}

func TestPerintahKeduaSetelahTitikKoma(t *testing.T) {
	c := at(t, "SELECT * FROM pelanggan; SELECT * FROM |")
	if !has(c, "pesanan") {
		t.Errorf("perintah kedua: %v", texts(c))
	}
	// Tabel perintah pertama tidak ikut mempengaruhi kolom perintah kedua.
	c = at(t, "SELECT * FROM pelanggan; SELECT | FROM pesanan")
	if has(c, "email") {
		t.Errorf("kolom perintah sebelumnya bocor: %v", texts(c))
	}
}

func TestPrefixDanPenggantian(t *testing.T) {
	c := at(t, "SELECT * FROM pesanan WHERE dib|")
	if c.Prefix != "dib" {
		t.Errorf("prefix = %q", c.Prefix)
	}
	if got := first(t, c); got.Text != "dibuat_pada" {
		t.Errorf("saran untuk \"dib\" = %q", got.Text)
	}
	// Pencocokan tidak membedakan huruf besar/kecil.
	if c := at(t, "SELECT * FROM pesanan WHERE DIB|"); !has(c, "dibuat_pada") {
		t.Errorf("huruf besar: %v", texts(c))
	}
	// Kata yang tidak cocok apa pun menghasilkan daftar kosong, bukan daftar penuh.
	if c := at(t, "SELECT * FROM pesanan WHERE zzqq|"); len(c.Suggestions) != 0 {
		t.Errorf("kata tak dikenal: %v", texts(c))
	}
}

func TestSkemaKosongTetapMemberiKerangka(t *testing.T) {
	c := Complete(Schema{Database: "baru"}, "", 0)
	if !has(c, "SELECT") {
		t.Errorf("database kosong tetap harus memberi perintah dasar: %v", texts(c))
	}
	// Tanpa tabel, template memakai nama contoh yang jelas.
	var tmpl string
	for _, s := range c.Suggestions {
		if s.Kind == KindSnippet {
			tmpl = s.Text
			break
		}
	}
	if !strings.Contains(tmpl, "nama_tabel") {
		t.Errorf("template tanpa tabel = %q", tmpl)
	}
}

func TestCursorAfterInsert(t *testing.T) {
	if got := (Suggestion{Text: "sum()", Cursor: 4}).CursorAfterInsert(); got != 4 {
		t.Errorf("= %d", got)
	}
	if got := (Suggestion{Text: "nama", Cursor: -1}).CursorAfterInsert(); got != 4 {
		t.Errorf("tanpa posisi khusus = %d", got)
	}
	if got := (Suggestion{Text: "ab", Cursor: 99}).CursorAfterInsert(); got != 2 {
		t.Errorf("posisi di luar teks = %d", got)
	}
}

func TestTokenizeDanRelasi(t *testing.T) {
	// Komentar -- diabaikan supaya tidak mengacaukan konteks.
	c := at(t, "SELECT * FROM pesanan -- catatan\nWHERE |")
	if !has(c, "status") {
		t.Errorf("setelah komentar: %v", texts(c))
	}
	// Tanda kutip ganda pada nama tetap dikenali.
	s := skemaUji()
	if _, ok := s.Find(`"pesanan"`); !ok {
		t.Error("nama berkutip ganda tidak dikenali")
	}
	if _, ok := s.Find("penjualan.rekap"); !ok {
		t.Error("nama berkualifikasi tidak dikenali")
	}
	if _, ok := s.Find("tidak_ada"); ok {
		t.Error("nama asing seharusnya tidak ditemukan")
	}
}
