package postgres

import (
	"fmt"
	"sort"
	"strings"
)

// Jenis saran yang bisa muncul di editor query.
const (
	KindKeyword  = "kata kunci"
	KindTable    = "tabel"
	KindColumn   = "kolom"
	KindFunction = "fungsi"
	KindValue    = "nilai"
	KindOperator = "operator"
	KindSnippet  = "template"
)

// Suggestion adalah satu saran yang bisa dimasukkan ke teks query.
type Suggestion struct {
	Text   string // teks yang disisipkan menggantikan kata yang sedang diketik
	Kind   string
	Detail string // keterangan singkat rata kanan: tipe kolom, tabel asal, dsb.
	Doc    string // penjelasan satu kalimat untuk baris bantuan
	// Cursor adalah posisi kursor relatif terhadap awal Text setelah disisipkan;
	// -1 berarti di akhir. Dipakai template supaya kursor berhenti di tempat yang perlu diisi.
	Cursor int
}

// CursorAfterInsert mengembalikan posisi kursor setelah saran disisipkan.
func (s Suggestion) CursorAfterInsert() int {
	if s.Cursor < 0 || s.Cursor > len([]rune(s.Text)) {
		return len([]rune(s.Text))
	}
	return s.Cursor
}

// Completion adalah hasil pemanggilan Complete.
type Completion struct {
	Prefix      string // kata yang sedang diketik dan akan digantikan saran
	Context     string // penjelasan konteks untuk ditampilkan di layar
	Suggestions []Suggestion
}

// --- pemecahan token ----------------------------------------------------------------------------

// token adalah satu satuan teks SQL: kata, angka, string, atau tanda baca.
type token struct {
	text  string
	start int // indeks rune awal
	quote byte
}

func (t token) word() string { return strings.ToLower(strings.Trim(t.text, `"`)) }

// tokenize memecah SQL menjadi token sederhana. Cukup untuk menebak konteks; bukan parser SQL.
// String ('...') dan pengenal berkutip ("...") dijaga utuh supaya isinya tidak salah dibaca.
func tokenize(s string) []token {
	var out []token
	r := []rune(s)
	i := 0
	for i < len(r) {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '\'' || c == '"':
			q := c
			start := i
			i++
			for i < len(r) {
				if r[i] == q {
					// Kutip ganda di dalam string adalah escape, bukan penutup.
					if i+1 < len(r) && r[i+1] == q {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			out = append(out, token{text: string(r[start:i]), start: start, quote: byte(q)})
		case c == '-' && i+1 < len(r) && r[i+1] == '-':
			for i < len(r) && r[i] != '\n' {
				i++
			}
		case isIdentRune(c):
			start := i
			for i < len(r) && isIdentRune(r[i]) {
				i++
			}
			out = append(out, token{text: string(r[start:i]), start: start})
		default:
			out = append(out, token{text: string(c), start: i})
			i++
		}
	}
	return out
}

func isIdentRune(c rune) bool {
	return c == '_' || c == '$' || c == '.' || c == '*' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// --- konteks ------------------------------------------------------------------------------------

// relRef adalah tabel yang disebut di statement beserta aliasnya.
type relRef struct {
	rel   Relation
	alias string
}

// statementStart mencari awal perintah terakhir sebelum kursor (setelah titik koma terakhir).
func statementStart(text string, cursor int) int {
	r := []rune(text)
	if cursor > len(r) {
		cursor = len(r)
	}
	start := 0
	inStr := false
	for i := 0; i < cursor; i++ {
		switch {
		case r[i] == '\'':
			inStr = !inStr
		case r[i] == ';' && !inStr:
			start = i + 1
		}
	}
	return start
}

// relationsInScope mencari tabel yang disebut di seluruh statement (bukan hanya sebelum kursor),
// supaya saran kolom tetap muncul saat user kembali menyunting bagian SELECT.
func relationsInScope(s Schema, stmt string) []relRef {
	toks := tokenize(stmt)
	var out []relRef
	seen := map[string]bool{}
	for i, t := range toks {
		switch t.word() {
		case "from", "join", "update", "into", "table":
		default:
			continue
		}
		// Nama tabel selalu tepat setelah kata kunci ini. Bila yang menyusul bukan tabel yang
		// dikenal (mis. sub-query "FROM (SELECT …)"), cukup dilewati.
		if i+1 >= len(toks) {
			continue
		}
		name := toks[i+1]
		if name.text == "(" || isKeyword(name.word()) {
			continue
		}
		rel, ok := s.Find(name.text)
		if !ok {
			continue
		}
		ref := relRef{rel: rel}
		// Alias: "FROM pesanan p" atau "FROM pesanan AS p".
		if i+2 < len(toks) {
			alias := toks[i+2]
			if alias.word() == "as" && i+3 < len(toks) {
				alias = toks[i+3]
			}
			if !isKeyword(alias.word()) && isIdentRune([]rune(alias.text)[0]) && alias.text != "(" {
				ref.alias = strings.Trim(alias.text, `"`)
			}
		}
		if !seen[rel.Qualified()+"|"+ref.alias] {
			seen[rel.Qualified()+"|"+ref.alias] = true
			out = append(out, ref)
		}
	}
	return out
}

// findRef mencari relasi berdasarkan alias atau namanya.
func findRef(refs []relRef, name string) (Relation, bool) {
	for _, r := range refs {
		if strings.EqualFold(r.alias, name) {
			return r.rel, true
		}
	}
	for _, r := range refs {
		if strings.EqualFold(r.rel.Name, name) || strings.EqualFold(r.rel.Qualified(), name) {
			return r.rel, true
		}
	}
	return Relation{}, false
}

// prefixAt mengembalikan kata yang sedang diketik tepat sebelum kursor.
func prefixAt(text string, cursor int) string {
	r := []rune(text)
	if cursor > len(r) {
		cursor = len(r)
	}
	i := cursor
	for i > 0 && isIdentRune(r[i-1]) {
		i--
	}
	return string(r[i:cursor])
}

// Complete menghasilkan saran untuk posisi kursor tertentu di dalam teks query.
func Complete(s Schema, text string, cursor int) Completion {
	r := []rune(text)
	if cursor > len(r) {
		cursor = len(r)
	}
	prefix := prefixAt(text, cursor)
	stmtStart := statementStart(text, cursor)
	before := string(r[stmtStart:cursor])
	stmtEnd := len(r)
	for i := cursor; i < len(r); i++ {
		if r[i] == ';' {
			stmtEnd = i
			break
		}
	}
	refs := relationsInScope(s, string(r[stmtStart:stmtEnd]))

	c := Completion{Prefix: prefix}
	// Teks sebelum kursor tanpa kata yang sedang diketik, untuk melihat token sebelumnya.
	head := strings.TrimSuffix(before, prefix)
	toks := tokenize(head)
	prev, prev2 := lastWord(toks, 0), lastWord(toks, 1)

	// Di dalam string yang belum ditutup: jangan menyarankan apa pun.
	if unclosedString(before) {
		c.Context = "di dalam teks — tutup dengan tanda kutip tunggal"
		return c
	}

	// 1. Nama berkualifikasi: alias.kolom atau tabel.kolom.
	if base, field, ok := qualifiedPrefix(text, cursor); ok {
		if rel, found := findRef(refs, base); found {
			c.Prefix = field
			c.Context = "kolom " + rel.Qualified()
			c.Suggestions = filter(columnSuggestions(rel, ""), field)
			return c
		}
		if rel, found := s.Find(base); found {
			c.Prefix = field
			c.Context = "kolom " + rel.Qualified()
			c.Suggestions = filter(columnSuggestions(rel, ""), field)
			return c
		}
	}

	// 2. Nilai setelah operator pembanding: WHERE dibuat_pada >= …
	if col, op, ok := comparisonTarget(toks, refs, s); ok {
		c.Context = "nilai untuk " + col.Name + " (" + col.Type + ")"
		c.Suggestions = filter(valueSuggestions(col, op), prefix)
		return c
	}

	// 3. Operator setelah sebuah kolom: WHERE dibuat_pada …
	if col, ok := operatorTarget(toks, refs, s); ok && prefix == "" {
		c.Context = "operator untuk " + col.Name + " (" + col.Type + ")"
		c.Suggestions = operatorSuggestions(col)
		return c
	}

	// 4. Arah pengurutan setelah "ORDER BY kolom".
	if prev2 == "by" && prefix == "" {
		if _, ok := lookupColumn(prev, refs, s); ok {
			c.Context = "arah urutan"
			c.Suggestions = sortSuggestions()
			return c
		}
	}

	switch {
	// 5. Setelah FROM/JOIN/INTO/UPDATE → nama tabel.
	case isTableSlot(prev, prev2):
		c.Context = "nama tabel"
		c.Suggestions = filter(tableSuggestions(s), prefix)

	// 6. Awal perintah → template statement lengkap.
	case strings.TrimSpace(head) == "":
		c.Context = "perintah baru"
		c.Suggestions = filter(statementSuggestions(s), prefix)

	default:
		c.Context = "kolom & kata kunci"
		c.Suggestions = filter(contextSuggestions(s, refs, toks, prev), prefix)
	}
	return c
}

// lastWord mengembalikan kata ke-n dari belakang (0 = terakhir), tanda baca ikut dihitung.
func lastWord(toks []token, n int) string {
	if len(toks) <= n {
		return ""
	}
	return toks[len(toks)-1-n].word()
}

// unclosedString melaporkan apakah kursor berada di dalam string yang belum ditutup.
func unclosedString(s string) bool {
	count := 0
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		if r[i] != '\'' {
			continue
		}
		if i+1 < len(r) && r[i+1] == '\'' {
			i++
			continue
		}
		count++
	}
	return count%2 == 1
}

// qualifiedPrefix memecah "p.dib" menjadi base "p" dan field "dib".
func qualifiedPrefix(text string, cursor int) (base, field string, ok bool) {
	word := prefixAt(text, cursor)
	i := strings.LastIndex(word, ".")
	if i < 0 {
		return "", "", false
	}
	return word[:i], word[i+1:], true
}

// isTableSlot melaporkan apakah posisi ini menunggu nama tabel.
func isTableSlot(prev, prev2 string) bool {
	switch prev {
	case "from", "join", "into", "update", "table":
		return true
	case ",":
		return prev2 == "" // daftar tabel tanpa konteks lain jarang; biarkan konteks umum yang menangani
	}
	return false
}

// columnsInScope mengumpulkan kolom dari semua tabel yang disebut statement.
func columnsInScope(refs []relRef) []Suggestion {
	var out []Suggestion
	for _, ref := range refs {
		label := ref.rel.Qualified()
		if ref.alias != "" {
			label = ref.alias + " → " + label
		}
		for _, col := range ref.rel.Columns {
			out = append(out, columnSuggestion(col, label))
		}
	}
	return out
}

func columnSuggestion(col Column, from string) Suggestion {
	detail := col.Type
	if col.PK {
		detail += " · kunci utama"
	} else if col.NotNull {
		detail += " · wajib diisi"
	}
	doc := "kolom " + from
	if col.Temporal() {
		doc += " — bertipe waktu, bandingkan dengan now(), current_date, atau BETWEEN"
	}
	return Suggestion{Text: col.Name, Kind: KindColumn, Detail: detail, Doc: doc, Cursor: -1}
}

func columnSuggestions(rel Relation, _ string) []Suggestion {
	var out []Suggestion
	for _, col := range rel.Columns {
		out = append(out, columnSuggestion(col, rel.Qualified()))
	}
	return out
}

func tableSuggestions(s Schema) []Suggestion {
	var out []Suggestion
	for _, rel := range s.Relations {
		detail := fmt.Sprintf("%s · %d kolom", rel.KindLabel(), len(rel.Columns))
		if rel.Rows > 0 {
			detail += fmt.Sprintf(" · ±%d baris", rel.Rows)
		}
		out = append(out, Suggestion{Text: rel.Qualified(), Kind: KindTable, Detail: detail,
			Doc: kolomRingkas(rel), Cursor: -1})
	}
	return out
}

// kolomRingkas menyebut beberapa kolom pertama supaya user tahu isi tabelnya tanpa membukanya.
func kolomRingkas(rel Relation) string {
	var names []string
	for i, c := range rel.Columns {
		if i == 5 {
			names = append(names, "…")
			break
		}
		names = append(names, c.Name)
	}
	return "kolom: " + strings.Join(names, ", ")
}

// statementSuggestions adalah kerangka perintah lengkap untuk memulai query.
func statementSuggestions(s Schema) []Suggestion {
	tbl, cols := "nama_tabel", "kolom1, kolom2"
	var timeCol string
	for _, rel := range s.Relations {
		if rel.Kind != "r" || len(rel.Columns) == 0 {
			continue
		}
		tbl = rel.Qualified()
		var names []string
		for _, c := range rel.Columns {
			if len(names) < 2 {
				names = append(names, c.Name)
			}
			if timeCol == "" && c.Temporal() {
				timeCol = c.Name
			}
		}
		cols = strings.Join(names, ", ")
		break
	}
	if timeCol == "" {
		timeCol = "dibuat_pada"
	}
	snip := func(text, doc string) Suggestion {
		return Suggestion{Text: text, Kind: KindSnippet, Detail: "template", Doc: doc, Cursor: -1}
	}
	return []Suggestion{
		snip("SELECT * FROM "+tbl+" LIMIT 50", "lihat isi tabel, dibatasi 50 baris supaya aman"),
		snip("SELECT count(*) FROM "+tbl, "hitung jumlah baris"),
		snip("SELECT * FROM "+tbl+" WHERE "+timeCol+" >= current_date", "baris yang dibuat hari ini"),
		snip("SELECT * FROM "+tbl+" WHERE "+timeCol+" >= now() - interval '7 days' ORDER BY "+timeCol+" DESC", "tujuh hari terakhir, terbaru dulu"),
		snip("SELECT date_trunc('day', "+timeCol+") AS hari, count(*) FROM "+tbl+" GROUP BY 1 ORDER BY 1 DESC", "hitung per hari"),
		snip("INSERT INTO "+tbl+" ("+cols+") VALUES ()", "tambah satu baris"),
		snip("UPDATE "+tbl+" SET  WHERE id = ", "ubah baris tertentu — WHERE wajib supaya tidak kena semua baris"),
		snip("DELETE FROM "+tbl+" WHERE id = ", "hapus baris tertentu — WHERE wajib"),
		snip("EXPLAIN ANALYZE SELECT * FROM "+tbl, "lihat rencana & waktu eksekusi query"),
		{Text: "SELECT", Kind: KindKeyword, Detail: "baca data", Doc: "perintah membaca data", Cursor: -1},
		{Text: "INSERT INTO", Kind: KindKeyword, Detail: "tambah data", Doc: "perintah menambah baris", Cursor: -1},
		{Text: "UPDATE", Kind: KindKeyword, Detail: "ubah data", Doc: "perintah mengubah baris", Cursor: -1},
		{Text: "DELETE FROM", Kind: KindKeyword, Detail: "hapus data", Doc: "perintah menghapus baris", Cursor: -1},
		{Text: "WITH", Kind: KindKeyword, Detail: "query bertingkat", Doc: "beri nama pada sub-query (CTE)", Cursor: -1},
	}
}

// contextSuggestions adalah saran umum: kolom yang relevan, kata kunci lanjutan, dan fungsi.
func contextSuggestions(s Schema, refs []relRef, toks []token, prev string) []Suggestion {
	var out []Suggestion
	if len(refs) > 0 {
		out = append(out, columnsInScope(refs)...)
	}
	switch prev {
	case "select":
		out = append(out, Suggestion{Text: "*", Kind: KindKeyword, Detail: "semua kolom", Doc: "ambil seluruh kolom", Cursor: -1})
		out = append(out, Suggestion{Text: "count(*)", Kind: KindFunction, Detail: "jumlah baris", Doc: "hitung berapa baris yang cocok", Cursor: -1})
		out = append(out, Suggestion{Text: "DISTINCT", Kind: KindKeyword, Detail: "tanpa duplikat", Doc: "buang baris kembar", Cursor: -1})
	case "set":
		// UPDATE … SET kolom = nilai
		out = append(out, columnsInScope(refs)...)
	}
	if len(refs) == 0 && (prev == "" || prev == "select") {
		out = append(out, tableSuggestions(s)...)
	}
	out = append(out, keywordSuggestions(prev)...)
	out = append(out, functionSuggestions()...)
	return dedupe(out)
}

// keywordSuggestions memberi kata kunci lanjutan sesuai kata sebelumnya.
func keywordSuggestions(prev string) []Suggestion {
	kw := func(text, detail, doc string) Suggestion {
		return Suggestion{Text: text, Kind: KindKeyword, Detail: detail, Doc: doc, Cursor: -1}
	}
	common := []Suggestion{
		kw("WHERE", "saring baris", "syarat baris mana yang ikut"),
		kw("ORDER BY", "urutkan", "urutkan hasil; tambahkan DESC untuk terbesar/terbaru dulu"),
		kw("GROUP BY", "kelompokkan", "gabungkan baris per nilai kolom, dipakai bersama count/sum"),
		kw("HAVING", "saring kelompok", "seperti WHERE, tetapi untuk hasil GROUP BY"),
		kw("LIMIT", "batasi baris", "batasi jumlah baris yang dikembalikan"),
		kw("OFFSET", "lewati baris", "lewati sejumlah baris pertama"),
		kw("JOIN", "gabung tabel", "gabungkan dengan tabel lain lewat kolom penghubung"),
		kw("LEFT JOIN", "gabung, sisi kiri utuh", "semua baris tabel kiri tetap ada walau tidak ada pasangannya"),
		kw("INNER JOIN", "gabung, yang cocok saja", "hanya baris yang punya pasangan di kedua tabel"),
		kw("ON", "syarat gabungan", "kolom penghubung antar tabel"),
		kw("AS", "beri nama", "beri nama lain untuk kolom atau tabel"),
		kw("RETURNING", "kembalikan baris", "tampilkan baris hasil INSERT/UPDATE/DELETE"),
	}
	switch prev {
	case "where", "and", "or", "on", "having":
		return []Suggestion{
			kw("NOT", "kebalikan", "balikkan syarat di belakangnya"),
			kw("EXISTS", "ada barisnya", "benar bila sub-query menghasilkan minimal satu baris"),
		}
	case "order":
		return []Suggestion{kw("BY", "kolom pengurut", "sebutkan kolom yang dipakai mengurutkan")}
	case "group":
		return []Suggestion{kw("BY", "kolom pengelompok", "sebutkan kolom yang dipakai mengelompokkan")}
	case "by":
		return []Suggestion{
			kw("DESC", "besar → kecil", "urutkan menurun: terbaru atau terbesar dulu"),
			kw("ASC", "kecil → besar", "urutkan menaik (bawaan)"),
		}
	case "desc", "asc":
		return []Suggestion{
			kw("NULLS LAST", "kosong di akhir", "taruh baris bernilai NULL paling belakang"),
			kw("LIMIT", "batasi baris", "batasi jumlah baris"),
		}
	case "insert":
		return []Suggestion{kw("INTO", "tabel tujuan", "sebutkan tabel yang akan diisi")}
	case "delete":
		return []Suggestion{kw("FROM", "tabel sumber", "sebutkan tabel yang barisnya dihapus")}
	}
	return common
}

// sortSuggestions adalah pilihan setelah "ORDER BY kolom".
func sortSuggestions() []Suggestion {
	kw := func(text, detail, doc string) Suggestion {
		return Suggestion{Text: text, Kind: KindKeyword, Detail: detail, Doc: doc, Cursor: -1}
	}
	return []Suggestion{
		kw("DESC", "besar → kecil", "terbaru atau terbesar dulu"),
		kw("ASC", "kecil → besar", "urutan menaik (bawaan bila tidak ditulis)"),
		kw("DESC NULLS LAST", "besar → kecil, kosong terakhir", "baris bernilai NULL ditaruh paling belakang"),
		kw("LIMIT", "batasi baris", "batasi jumlah baris yang dikembalikan"),
	}
}

// functionSuggestions adalah fungsi bawaan yang paling sering dipakai.
func functionSuggestions() []Suggestion {
	fn := func(text, detail, doc string, cursor int) Suggestion {
		return Suggestion{Text: text, Kind: KindFunction, Detail: detail, Doc: doc, Cursor: cursor}
	}
	return []Suggestion{
		fn("count(*)", "jumlah baris", "hitung baris yang cocok", -1),
		fn("sum()", "jumlah nilai", "jumlahkan kolom angka", 4),
		fn("avg()", "rata-rata", "rata-rata kolom angka", 4),
		fn("min()", "terkecil", "nilai terkecil, juga untuk tanggal (paling lama)", 4),
		fn("max()", "terbesar", "nilai terbesar, juga untuk tanggal (paling baru)", 4),
		fn("coalesce()", "ganti NULL", "pakai nilai cadangan bila kolomnya NULL", 9),
		fn("now()", "waktu sekarang", "waktu saat query dijalankan, lengkap dengan zona waktu", -1),
		fn("current_date", "tanggal hari ini", "tanggal hari ini tanpa jam", -1),
		fn("date_trunc('day', )", "potong ke hari", "bulatkan waktu ke awal hari/bulan/tahun", 18),
		fn("extract(year FROM )", "ambil bagian waktu", "ambil tahun/bulan/hari dari sebuah waktu", 18),
		fn("to_char(, 'YYYY-MM-DD')", "format tanggal", "ubah waktu menjadi teks dengan format tertentu", 8),
		fn("age()", "selisih waktu", "selisih antara dua waktu dalam bentuk interval", 4),
		fn("lower()", "huruf kecil", "ubah teks jadi huruf kecil, berguna untuk perbandingan", 6),
		fn("length()", "panjang teks", "jumlah karakter", 7),
		fn("string_agg(, ', ')", "gabung teks", "gabungkan nilai beberapa baris jadi satu teks", 11),
	}
}

// --- nilai & operator per tipe kolom --------------------------------------------------------------

// comparisonTarget mencari kolom yang sedang dibandingkan tepat sebelum kursor:
// "... WHERE dibuat_pada >= |" menghasilkan kolom dibuat_pada dan operator ">=".
func comparisonTarget(toks []token, refs []relRef, s Schema) (Column, string, bool) {
	if len(toks) < 2 {
		return Column{}, "", false
	}
	last := toks[len(toks)-1].text
	op := last
	colTok := len(toks) - 2
	// Operator dua karakter ditulis sebagai dua token: ">" lalu "=".
	if len(toks) >= 3 && isOpChar(last) && isOpChar(toks[len(toks)-2].text) {
		op = toks[len(toks)-2].text + last
		colTok = len(toks) - 3
	}
	if !isComparison(op) {
		return Column{}, "", false
	}
	if colTok < 0 {
		return Column{}, "", false
	}
	if col, ok := lookupColumn(toks[colTok].text, refs, s); ok {
		return col, op, true
	}
	return Column{}, "", false
}

// operatorTarget mencari kolom yang baru saja diketik di posisi syarat, supaya operatornya
// bisa disarankan: "... WHERE dibuat_pada |".
func operatorTarget(toks []token, refs []relRef, s Schema) (Column, bool) {
	if len(toks) < 2 {
		return Column{}, false
	}
	prevWord := toks[len(toks)-2].word()
	switch prevWord {
	case "where", "and", "or", "on", "having", "set", ",", "(":
	default:
		return Column{}, false
	}
	return lookupColumn(toks[len(toks)-1].text, refs, s)
}

func lookupColumn(name string, refs []relRef, s Schema) (Column, bool) {
	name = strings.Trim(name, `"`)
	if base, field, ok := strings.Cut(name, "."); ok {
		if rel, found := findRef(refs, base); found {
			return rel.Column(field)
		}
		if rel, found := s.Find(base); found {
			return rel.Column(field)
		}
		return Column{}, false
	}
	for _, ref := range refs {
		if col, ok := ref.rel.Column(name); ok {
			return col, true
		}
	}
	return Column{}, false
}

func isOpChar(s string) bool {
	return s == "<" || s == ">" || s == "=" || s == "!" || s == "~"
}

func isComparison(op string) bool {
	switch strings.ToLower(op) {
	case "=", "<", ">", "<=", ">=", "<>", "!=", "like", "ilike", "in", "between", "~", "~*":
		return true
	}
	return false
}

// valueSuggestions menyarankan nilai yang masuk akal untuk sebuah kolom & operator.
// Untuk kolom waktu, daftarnya sengaja lengkap karena di situlah pemula paling sering tersesat.
func valueSuggestions(col Column, op string) []Suggestion {
	val := func(text, detail, doc string, cursor int) Suggestion {
		return Suggestion{Text: text, Kind: KindValue, Detail: detail, Doc: doc, Cursor: cursor}
	}
	switch {
	case col.Temporal():
		// Kolom hanya-tanggal tidak perlu saran berjam-jam; sebaliknya kolom timestamp justru
		// sering disaring per jam. Daftarnya dipisah supaya tiap tipe dapat yang benar-benar dipakai.
		var out []Suggestion
		if col.DateOnly() {
			out = []Suggestion{
				val("current_date", "hari ini", "tanggal hari ini menurut zona waktu server", -1),
				val("current_date - 1", "kemarin", "sehari sebelum hari ini", -1),
				val("current_date - interval '7 days'", "7 hari lalu", "sepekan ke belakang", -1),
				val("current_date - interval '30 days'", "30 hari lalu", "sebulan ke belakang", -1),
				val("date_trunc('month', now())::date", "awal bulan ini", "tanggal 1 bulan berjalan", -1),
				val("date_trunc('year', now())::date", "awal tahun ini", "1 Januari tahun berjalan", -1),
				val("DATE '2026-01-31'", "tanggal tertentu", "tulis tanggal apa adanya dengan format YYYY-MM-DD", 6),
				val("to_date('31-01-2026', 'DD-MM-YYYY')", "tanggal dari teks", "ubah teks berformat lain menjadi tanggal", 9),
			}
		} else {
			out = []Suggestion{
				val("current_date", "tanggal hari ini", "jam 00:00 hari ini menurut zona waktu server", -1),
				val("now()", "sekarang", "waktu saat query dijalankan", -1),
				val("now() - interval '1 hour'", "1 jam lalu", "gunakan 'x minutes', 'x hours', 'x days' sesuai kebutuhan", -1),
				val("now() - interval '1 day'", "24 jam lalu", "tepat 24 jam ke belakang dari sekarang", -1),
				val("now() - interval '7 days'", "7 hari lalu", "sepekan terakhir", -1),
				val("now() - interval '30 days'", "30 hari lalu", "sebulan terakhir", -1),
				val("current_date - 1", "kemarin", "tanggal kemarin, jam 00:00", -1),
				val("date_trunc('day', now())", "awal hari ini", "bulatkan waktu sekarang ke jam 00:00", -1),
				val("date_trunc('week', now())", "awal pekan ini", "Senin pekan ini jam 00:00", -1),
				val("date_trunc('month', now())", "awal bulan ini", "tanggal 1 bulan ini jam 00:00", -1),
				val("date_trunc('year', now())", "awal tahun ini", "1 Januari tahun ini", -1),
			}
			out = append(out,
				val("TIMESTAMP '2026-01-31 00:00'", "waktu tertentu", "tulis waktu apa adanya dengan format YYYY-MM-DD HH:MM", 11),
				val("DATE '2026-01-31'", "tanggal tertentu", "tanggal dibandingkan sebagai jam 00:00", 6))
			if col.WithTimeZone() {
				out = append(out,
					val("now() AT TIME ZONE 'Asia/Jakarta'", "waktu lokal", "ubah ke zona waktu tertentu sebelum dibandingkan", -1),
					val("(current_date + interval '17 hours')", "jam tertentu hari ini", "tambahkan jam ke tanggal untuk batas yang presisi", -1))
			}
		}
		if strings.EqualFold(op, "between") {
			out = append([]Suggestion{
				val("current_date - interval '7 days' AND now()", "7 hari terakhir", "rentang dari tujuh hari lalu sampai sekarang", -1),
				val("date_trunc('month', now()) AND now()", "bulan berjalan", "dari awal bulan sampai sekarang", -1),
			}, out...)
		}
		return out
	case col.Boolean():
		return []Suggestion{
			val("true", "benar", "nilai boolean benar", -1),
			val("false", "salah", "nilai boolean salah", -1),
			val("NULL", "kosong", "belum diisi — bandingkan dengan IS NULL, bukan = NULL", -1),
		}
	case col.Numeric():
		return []Suggestion{
			val("0", "angka", "tulis angka tanpa tanda kutip", -1),
			val("NULL", "kosong", "belum diisi — bandingkan dengan IS NULL", -1),
		}
	case col.Textual():
		if strings.EqualFold(op, "like") || strings.EqualFold(op, "ilike") {
			return []Suggestion{
				val("'%kata%'", "mengandung kata", "% berarti \"apa saja\"; ILIKE mengabaikan besar kecil huruf", 2),
				val("'kata%'", "diawali kata", "cocok bila teks dimulai dengan kata itu", 1),
			}
		}
		return []Suggestion{
			val("''", "teks", "tulis teks di antara tanda kutip tunggal", 1),
			val("NULL", "kosong", "belum diisi — bandingkan dengan IS NULL", -1),
		}
	}
	return []Suggestion{val("NULL", "kosong", "belum diisi", -1)}
}

// operatorSuggestions menyarankan pembanding yang cocok untuk tipe kolom.
func operatorSuggestions(col Column) []Suggestion {
	op := func(text, detail, doc string) Suggestion {
		return Suggestion{Text: text, Kind: KindOperator, Detail: detail, Doc: doc, Cursor: -1}
	}
	base := []Suggestion{
		op("=", "sama dengan", "cocok persis"),
		op("<>", "tidak sama dengan", "semua kecuali nilai itu"),
		op("IS NULL", "kosong", "belum diisi — NULL tidak pernah cocok dengan ="),
		op("IS NOT NULL", "terisi", "sudah ada isinya"),
		op("IN ()", "salah satu dari", "cocok bila sama dengan salah satu nilai dalam kurung"),
	}
	switch {
	case col.Temporal():
		return append([]Suggestion{
			op(">=", "mulai dari", "termasuk batas bawah — cara paling aman menyaring rentang waktu"),
			op("<", "sebelum", "batas atas eksklusif, mis. < current_date berarti sampai kemarin"),
			op("BETWEEN", "di antara", "BETWEEN a AND b: termasuk kedua ujungnya"),
			op(">", "setelah", "lebih baru daripada"),
			op("<=", "sampai dengan", "termasuk batas atas"),
		}, base...)
	case col.Numeric():
		return append([]Suggestion{
			op(">", "lebih dari", "lebih besar"),
			op(">=", "minimal", "lebih besar atau sama dengan"),
			op("<", "kurang dari", "lebih kecil"),
			op("<=", "maksimal", "lebih kecil atau sama dengan"),
			op("BETWEEN", "di antara", "BETWEEN a AND b: termasuk kedua ujungnya"),
		}, base...)
	case col.Textual():
		return append([]Suggestion{
			op("ILIKE", "mirip, abaikan huruf besar", "pencarian bebas: ILIKE '%kata%'"),
			op("LIKE", "mirip", "pencarian dengan pola %"),
		}, base...)
	}
	return base
}

// --- penyaringan & pengurutan ---------------------------------------------------------------------

// kindRank menentukan urutan tampil antar jenis saran.
func kindRank(k string) int {
	switch k {
	case KindColumn:
		return 0
	case KindValue, KindOperator:
		return 1
	case KindTable:
		return 2
	case KindSnippet:
		return 3
	case KindFunction:
		return 4
	}
	return 5
}

// filter menyaring saran berdasarkan kata yang sedang diketik: yang diawali kata itu lebih dulu,
// lalu yang sekadar mengandungnya.
func filter(in []Suggestion, prefix string) []Suggestion {
	p := strings.ToLower(strings.TrimSpace(prefix))
	type scored struct {
		s    Suggestion
		rank int
		pos  int
	}
	var out []scored
	for i, s := range in {
		text := strings.ToLower(s.Text)
		switch {
		case p == "":
			out = append(out, scored{s, 1, i})
		case strings.HasPrefix(text, p):
			out = append(out, scored{s, 0, i})
		case strings.Contains(text, p):
			out = append(out, scored{s, 2, i})
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].rank != out[b].rank {
			return out[a].rank < out[b].rank
		}
		if ka, kb := kindRank(out[a].s.Kind), kindRank(out[b].s.Kind); ka != kb {
			return ka < kb
		}
		return out[a].pos < out[b].pos
	})
	res := make([]Suggestion, 0, len(out))
	for _, x := range out {
		res = append(res, x.s)
	}
	return res
}

func dedupe(in []Suggestion) []Suggestion {
	seen := map[string]bool{}
	var out []Suggestion
	for _, s := range in {
		key := s.Kind + "|" + s.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func isKeyword(w string) bool {
	switch w {
	case "select", "from", "where", "and", "or", "not", "join", "inner", "left", "right", "full", "outer",
		"on", "group", "order", "by", "having", "limit", "offset", "insert", "into", "values", "update",
		"set", "delete", "returning", "with", "as", "distinct", "union", "all", "using", "asc", "desc",
		"between", "in", "is", "null", "like", "ilike", "exists", "case", "when", "then", "else", "end":
		return true
	}
	return false
}
