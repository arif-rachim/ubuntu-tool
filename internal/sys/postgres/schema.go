package postgres

import (
	"context"
	"sort"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Column adalah satu kolom tabel beserta tipenya.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	NotNull  bool   `json:"notnull"`
	PK       bool   `json:"pk"`
	Default  string `json:"default"`
	Position int    `json:"position"`
}

// Temporal melaporkan apakah kolom bertipe waktu (date, timestamp, timestamptz, time, interval).
func (c Column) Temporal() bool { return TemporalType(c.Type) }

// DateOnly melaporkan apakah kolom hanya menyimpan tanggal tanpa jam.
func (c Column) DateOnly() bool { return strings.HasPrefix(strings.ToLower(c.Type), "date") }

// WithTimeZone melaporkan apakah kolom menyimpan zona waktu (timestamptz/timetz).
func (c Column) WithTimeZone() bool {
	return strings.Contains(strings.ToLower(c.Type), "with time zone")
}

// Numeric melaporkan apakah kolom bertipe angka.
func (c Column) Numeric() bool { return NumericType(c.Type) }

// Boolean melaporkan apakah kolom bertipe boolean.
func (c Column) Boolean() bool { return strings.HasPrefix(strings.ToLower(c.Type), "bool") }

// Textual melaporkan apakah kolom bertipe teks.
func (c Column) Textual() bool { return TextType(c.Type) }

// TemporalType melaporkan apakah nama tipe PostgreSQL menyimpan waktu.
func TemporalType(t string) bool {
	t = strings.ToLower(t)
	for _, p := range []string{"timestamp", "date", "time", "interval"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// NumericType melaporkan apakah nama tipe PostgreSQL berupa angka.
func NumericType(t string) bool {
	t = strings.ToLower(t)
	for _, p := range []string{"int", "numeric", "decimal", "real", "double", "serial", "money", "float"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// TextType melaporkan apakah nama tipe PostgreSQL berupa teks.
func TextType(t string) bool {
	t = strings.ToLower(t)
	for _, p := range []string{"text", "character", "varchar", "char", "citext", "uuid", "name"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// Relation adalah satu tabel/view beserta kolomnya.
type Relation struct {
	Schema  string   `json:"schema"`
	Name    string   `json:"name"`
	Kind    string   `json:"kind"` // r, p, v, m, f (relkind PostgreSQL)
	Rows    int64    `json:"rows"`
	Columns []Column `json:"columns"`
}

// KindLabel menjelaskan jenis relasi dalam bahasa manusia.
func (r Relation) KindLabel() string {
	switch r.Kind {
	case "v":
		return "view"
	case "m":
		return "view termaterialisasi"
	case "p":
		return "tabel partisi"
	case "f":
		return "tabel asing"
	}
	return "tabel"
}

// Qualified adalah nama lengkap relasi; skema public dibiarkan tanpa awalan karena selalu
// ada di search_path bawaan.
func (r Relation) Qualified() string {
	if r.Schema == "public" || r.Schema == "" {
		return r.Name
	}
	return r.Schema + "." + r.Name
}

// Column mencari kolom bernama name (tanpa memperhatikan besar kecil huruf).
func (r Relation) Column(name string) (Column, bool) {
	for _, c := range r.Columns {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Column{}, false
}

// Schema adalah seluruh relasi yang bisa dibaca di satu database.
type Schema struct {
	Database  string
	Relations []Relation
}

// Find mencari relasi berdasarkan nama, dengan atau tanpa awalan skema.
func (s Schema) Find(name string) (Relation, bool) {
	name = strings.Trim(strings.TrimSpace(name), `"`)
	schema, bare, qualified := "", name, strings.Contains(name, ".")
	if qualified {
		schema, bare, _ = strings.Cut(name, ".")
		schema, bare = strings.Trim(schema, `"`), strings.Trim(bare, `"`)
	}
	for _, r := range s.Relations {
		if !strings.EqualFold(r.Name, bare) {
			continue
		}
		if qualified && !strings.EqualFold(r.Schema, schema) {
			continue
		}
		return r, true
	}
	return Relation{}, false
}

// Empty melaporkan apakah tidak ada relasi sama sekali.
func (s Schema) Empty() bool { return len(s.Relations) == 0 }

// ColumnCount adalah jumlah seluruh kolom di database (untuk ditampilkan di layar).
func (s Schema) ColumnCount() int {
	n := 0
	for _, r := range s.Relations {
		n += len(r.Columns)
	}
	return n
}

// schemaSQL membaca semua tabel & view yang boleh dilihat user beserta kolomnya. Dibatasi ke
// skema milik user (pg_catalog & information_schema dilewati) supaya saran tetap relevan.
const schemaSQL = `SELECT n.nspname AS schema, c.relname AS name, c.relkind::text AS kind,
  greatest(c.reltuples, 0)::bigint AS rows,
  coalesce((
    SELECT json_agg(json_build_object(
             'name', a.attname,
             'type', format_type(a.atttypid, a.atttypmod),
             'notnull', a.attnotnull,
             'pk', coalesce(i.indisprimary, false),
             'default', coalesce(left(pg_get_expr(d.adbin, d.adrelid), 40), ''),
             'position', a.attnum)
           ORDER BY a.attnum)
    FROM pg_attribute a
    LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
    LEFT JOIN pg_index i ON i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY (i.indkey)
    WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
  ), '[]'::json) AS columns
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg!_%' ESCAPE '!'
  AND has_table_privilege(c.oid, 'SELECT')
ORDER BY n.nspname, c.relname`

// ReadSchema membaca daftar tabel/view beserta kolomnya, untuk saran otomatis di editor query.
func (c Client) ReadSchema(ctx context.Context, r run.Runner, db string) (Schema, error) {
	s := Schema{Database: db}
	if err := c.Query(ctx, r, db, schemaSQL, &s.Relations); err != nil {
		return s, err
	}
	sort.SliceStable(s.Relations, func(i, j int) bool {
		// Tabel biasa lebih dulu daripada view, lalu urut nama — yang paling sering dipakai di atas.
		if (s.Relations[i].Kind == "r") != (s.Relations[j].Kind == "r") {
			return s.Relations[i].Kind == "r"
		}
		return s.Relations[i].Qualified() < s.Relations[j].Qualified()
	})
	return s, nil
}
