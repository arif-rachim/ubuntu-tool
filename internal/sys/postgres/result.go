package postgres

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// MaxResultRows membatasi baris yang ditarik ke memori. Query tanpa LIMIT di tabel besar tidak
// boleh membuat ubt (atau server) kehabisan memori hanya karena user ingin "lihat isinya".
const MaxResultRows = 5000

// NullMarker adalah penanda NULL di dalam memori — bukan bagian dari command. Isinya satu byte NUL,
// yang tidak mungkin muncul sebagai nilai teks PostgreSQL, jadi NULL tidak pernah tertukar dengan
// teks biasa (termasuk teks kosong, yang artinya berbeda).
const NullMarker = "\x00"

// ResultTimeout membatasi lama query lewat statement_timeout di sisi server.
const ResultTimeout = "60s"

// Result adalah hasil query dalam bentuk tabel.
type Result struct {
	Columns   []string
	Rows      [][]string
	Truncated bool // baris melebihi MaxResultRows dan sisanya dibuang
	Elapsed   time.Duration
}

// Empty melaporkan apakah query tidak mengembalikan baris.
func (r Result) Empty() bool { return len(r.Rows) == 0 }

// IsNull melaporkan apakah sel bernilai NULL (bukan teks kosong).
func IsNull(cell string) bool { return cell == NullMarker }

// Display mengubah sel menjadi teks yang siap ditampilkan.
func Display(cell string) string {
	if IsNull(cell) {
		return "NULL"
	}
	return cell
}

// Flatten meratakan isi sel menjadi satu baris. Nilai berisi baris baru atau tab merusak tata letak
// tabel bila ditulis apa adanya, jadi di sini ditandai — isi utuhnya tetap bisa dilihat per baris.
func Flatten(cell string) string {
	if !strings.ContainsAny(cell, "\n\r\t\v\f") {
		return cell
	}
	cell = strings.ReplaceAll(cell, "\r\n", "\n")
	for _, r := range []string{"\r", "\v", "\f"} {
		cell = strings.ReplaceAll(cell, r, "\n")
	}
	cell = strings.ReplaceAll(cell, "\t", " ")
	return strings.ReplaceAll(cell, "\n", " ↵ ")
}

// NumericColumn melaporkan apakah seluruh nilai kolom berupa angka, supaya bisa dirata-kanankan.
func (r Result) NumericColumn(i int) bool {
	seen := false
	for _, row := range r.Rows {
		if i >= len(row) || IsNull(row[i]) || row[i] == "" {
			continue
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(row[i]), 64); err != nil {
			return false
		}
		seen = true
	}
	return seen
}

// Widest adalah panjang isi terpanjang pada satu kolom (termasuk judulnya).
func (r Result) Widest(i int) int {
	w := 0
	if i < len(r.Columns) {
		w = len([]rune(r.Columns[i]))
	}
	for _, row := range r.Rows {
		if i < len(row) {
			if n := len([]rune(Flatten(Display(row[i])))); n > w {
				w = n
			}
		}
	}
	return w
}

// ParseCopyText membaca keluaran COPY … TO STDOUT (format teks) menjadi tabel.
//
// Format teks COPY dipilih karena tidak ada nilai yang bisa menyamar: kolom dipisah tab, baris
// dipisah baris baru, dan tab/baris baru/backslash di dalam data selalu di-escape lebih dulu.
// NULL keluar sebagai \N sedangkan teks kosong keluar sebagai kolom kosong, jadi keduanya tidak
// pernah tertukar — dan teks "\N" yang sungguhan keluar sebagai \\N.
func ParseCopyText(out string) Result {
	var res Result
	if out == "" {
		return res
	}
	out = strings.TrimSuffix(out, "\n")
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if i == 0 {
			res.Columns = make([]string, len(fields))
			for j, f := range fields {
				res.Columns[j] = unescapeCopy(f)
			}
			continue
		}
		if len(res.Rows) >= MaxResultRows {
			res.Truncated = true
			break
		}
		row := make([]string, len(res.Columns))
		for j := range row {
			if j < len(fields) {
				row[j] = unescapeCopy(fields[j])
			}
		}
		res.Rows = append(res.Rows, row)
	}
	return res
}

// unescapeCopy mengembalikan satu kolom format teks COPY ke nilai aslinya.
func unescapeCopy(f string) string {
	if f == `\N` {
		return NullMarker
	}
	if !strings.Contains(f, `\`) {
		return f
	}
	var b strings.Builder
	b.Grow(len(f))
	for i := 0; i < len(f); i++ {
		if f[i] != '\\' || i+1 >= len(f) {
			b.WriteByte(f[i])
			continue
		}
		i++
		switch f[i] {
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		default:
			// Termasuk \\ : karakter setelah backslash dipakai apa adanya.
			b.WriteByte(f[i])
		}
	}
	return b.String()
}

// copyWrapper membungkus query user menjadi COPY … TO STDOUT supaya hasilnya keluar dalam format
// yang bisa diurai persis, apa pun isi datanya (tab, baris baru, backslash).
func copyWrapper(sql string) string {
	sql = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	return "COPY (" + sql + ") TO STDOUT WITH (HEADER true)"
}

// ResultCommand membangun command pembacaan hasil query. Query dijalankan dalam satu transaksi
// READ ONLY dengan batas waktu, jadi perintah yang ternyata menulis ditolak server.
func (c Client) ResultCommand(db, sql string) run.Command {
	argv := []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "--single-transaction"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, "-d", db,
		"-c", "SET TRANSACTION READ ONLY",
		"-c", "SET LOCAL statement_timeout = '"+ResultTimeout+"'",
		"-c", copyWrapper(sql))

	cmd := asPostgres(argv...)
	cmd.Title = "Baca hasil query di " + db
	cmd.Risk = risk.Safe
	cmd.Explain = []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"},
		{Token: "SET TRANSACTION READ ONLY", Meaning: "kunci pengaman: server menolak perintah apa pun yang mengubah data"},
		{Token: "SET LOCAL statement_timeout", Meaning: "batalkan otomatis bila query berjalan lebih dari " + ResultTimeout},
		{Token: "COPY (…) TO STDOUT WITH (HEADER true)", Meaning: "keluarkan hasil beserta nama kolomnya dalam format teks COPY, supaya nilai yang berisi tab atau baris baru tetap utuh"},
	}
	cmd.Effect = "Hanya membaca."
	return cmd
}

// RunQuery menjalankan query baca dan mengembalikan hasilnya sebagai tabel.
func (c Client) RunQuery(ctx context.Context, r run.Runner, db, sql string) (Result, error) {
	started := time.Now()
	out, stderr, err := r.Capture(ctx, c.ResultCommand(db, sql))
	if err != nil {
		return Result{}, queryError(stderr, err)
	}
	res := ParseCopyText(out)
	res.Elapsed = time.Since(started)
	return res, nil
}
