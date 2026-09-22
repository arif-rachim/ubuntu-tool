package postgres

import (
	"context"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Summary adalah ringkasan kondisi server untuk baris atas layar.
type Summary struct {
	Version     string `json:"version"`
	Uptime      int64  `json:"uptime_secs"`
	Connections int    `json:"connections"`
	MaxConn     int    `json:"max_conn"`
	Active      int    `json:"active"`
	Idle        int    `json:"idle_in_tx"`
	Databases   int    `json:"databases"`
	DataSize    int64  `json:"data_size"`
	Statements  bool   `json:"statements"`
	Encoding    string `json:"encoding"`
}

const summarySQL = `SELECT
  current_setting('server_version') AS version,
  extract(epoch from (now() - pg_postmaster_start_time()))::bigint AS uptime_secs,
  (SELECT count(*) FROM pg_stat_activity)::int AS connections,
  current_setting('max_connections')::int AS max_conn,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'active')::int AS active,
  (SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction')::int AS idle_in_tx,
  (SELECT count(*) FROM pg_database WHERE NOT datistemplate)::int AS databases,
  (SELECT coalesce(sum(pg_database_size(oid)), 0)::bigint FROM pg_database) AS data_size,
  (SELECT count(*) > 0 FROM pg_extension WHERE extname = 'pg_stat_statements') AS statements,
  current_setting('server_encoding') AS encoding`

// ReadSummary membaca ringkasan server.
func (c Client) ReadSummary(ctx context.Context, r run.Runner) (Summary, error) {
	var rows []Summary
	if err := c.Query(ctx, r, "postgres", summarySQL, &rows); err != nil {
		return Summary{}, err
	}
	if len(rows) == 0 {
		return Summary{}, nil
	}
	return rows[0], nil
}

// Database adalah satu database di cluster.
type Database struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Encoding    string `json:"encoding"`
	Size        int64  `json:"size_bytes"`
	Connections int    `json:"connections"`
	AllowConn   bool   `json:"allow_conn"`
	Tables      int    `json:"tables"`
}

const databaseSQL = `SELECT d.datname AS name,
  pg_catalog.pg_get_userbyid(d.datdba) AS owner,
  pg_catalog.pg_encoding_to_char(d.encoding) AS encoding,
  pg_database_size(d.oid)::bigint AS size_bytes,
  (SELECT count(*) FROM pg_stat_activity a WHERE a.datid = d.oid)::int AS connections,
  d.datallowconn AS allow_conn,
  0 AS tables
FROM pg_database d
WHERE NOT d.datistemplate
ORDER BY pg_database_size(d.oid) DESC`

// ReadDatabases membaca daftar database beserta ukurannya.
func (c Client) ReadDatabases(ctx context.Context, r run.Runner) ([]Database, error) {
	var out []Database
	err := c.Query(ctx, r, "postgres", databaseSQL, &out)
	return out, err
}

// Role adalah satu role (user) PostgreSQL.
type Role struct {
	Name        string   `json:"name"`
	Superuser   bool     `json:"superuser"`
	CreateDB    bool     `json:"createdb"`
	CreateRole  bool     `json:"createrole"`
	Login       bool     `json:"login"`
	Replication bool     `json:"replication"`
	ConnLimit   int      `json:"conn_limit"`
	ValidUntil  string   `json:"valid_until"`
	MemberOf    []string `json:"member_of"`
	HasPassword bool     `json:"has_password"`
}

// Kind menjelaskan jenis role dalam satu kata.
func (r Role) Kind() string {
	switch {
	case r.Superuser:
		return "superuser"
	case !r.Login:
		return "grup"
	default:
		return "user"
	}
}

const roleSQL = `SELECT r.rolname AS name,
  r.rolsuper AS superuser, r.rolcreatedb AS createdb, r.rolcreaterole AS createrole,
  r.rolcanlogin AS login, r.rolreplication AS replication, r.rolconnlimit::int AS conn_limit,
  coalesce(to_char(r.rolvaliduntil, 'YYYY-MM-DD'), '') AS valid_until,
  coalesce((SELECT array_agg(b.rolname ORDER BY b.rolname) FROM pg_auth_members m
            JOIN pg_roles b ON m.roleid = b.oid WHERE m.member = r.oid), '{}') AS member_of,
  (SELECT s.rolpassword IS NOT NULL FROM pg_authid s WHERE s.oid = r.oid) AS has_password
FROM pg_roles r
WHERE r.rolname NOT LIKE 'pg!_%' ESCAPE '!'
ORDER BY r.rolsuper DESC, r.rolname`

// ReadRoles membaca daftar role.
func (c Client) ReadRoles(ctx context.Context, r run.Runner) ([]Role, error) {
	var out []Role
	err := c.Query(ctx, r, "postgres", roleSQL, &out)
	return out, err
}

// Activity adalah satu koneksi yang sedang terbuka.
type Activity struct {
	PID       int    `json:"pid"`
	User      string `json:"usename"`
	Database  string `json:"datname"`
	Client    string `json:"client"`
	State     string `json:"state"`
	QuerySecs int    `json:"query_secs"`
	StateSecs int    `json:"state_secs"`
	Wait      string `json:"wait"`
	BlockedBy []int  `json:"blocked_by"`
	Query     string `json:"query"`
}

// Problem menjelaskan kenapa koneksi ini perlu diperhatikan (kosong bila wajar).
func (a Activity) Problem() string {
	switch {
	case len(a.BlockedBy) > 0:
		return "menunggu lock yang dipegang koneksi lain"
	case a.State == "idle in transaction" && a.StateSecs > 60:
		return "transaksi dibuka tetapi tidak dipakai — menahan lock & menghambat autovacuum"
	case a.State == "active" && a.QuerySecs > 300:
		return "query berjalan lebih dari 5 menit"
	}
	return ""
}

const activitySQL = `SELECT a.pid::int AS pid,
  coalesce(a.usename, '') AS usename,
  coalesce(a.datname, '') AS datname,
  coalesce(host(a.client_addr), 'socket lokal') AS client,
  coalesce(a.state, '') AS state,
  coalesce(extract(epoch from (now() - a.query_start))::int, 0) AS query_secs,
  coalesce(extract(epoch from (now() - a.state_change))::int, 0) AS state_secs,
  coalesce(a.wait_event_type, '') AS wait,
  coalesce(pg_blocking_pids(a.pid), '{}') AS blocked_by,
  left(regexp_replace(coalesce(a.query, ''), '\s+', ' ', 'g'), 160) AS query
FROM pg_stat_activity a
WHERE a.backend_type = 'client backend' AND a.pid <> pg_backend_pid()
ORDER BY (a.state = 'active') DESC, query_secs DESC`

// ReadActivity membaca koneksi yang sedang terbuka.
func (c Client) ReadActivity(ctx context.Context, r run.Runner) ([]Activity, error) {
	var out []Activity
	err := c.Query(ctx, r, "postgres", activitySQL, &out)
	return out, err
}

// Table adalah satu tabel beserta ukuran dan statistik vacuum-nya.
type Table struct {
	Name        string `json:"name"`
	TotalBytes  int64  `json:"total_bytes"`
	TableBytes  int64  `json:"table_bytes"`
	IndexBytes  int64  `json:"index_bytes"`
	LiveRows    int64  `json:"live_rows"`
	DeadRows    int64  `json:"dead_rows"`
	SeqScan     int64  `json:"seq_scan"`
	IdxScan     int64  `json:"idx_scan"`
	LastVacuum  string `json:"last_vacuum"`
	LastAnalyze string `json:"last_analyze"`
}

// Bloat memperkirakan persentase baris mati (sampah hasil UPDATE/DELETE).
func (t Table) Bloat() float64 {
	if t.LiveRows+t.DeadRows == 0 {
		return 0
	}
	return float64(t.DeadRows) / float64(t.LiveRows+t.DeadRows) * 100
}

const tableSQL = `SELECT s.schemaname || '.' || s.relname AS name,
  pg_total_relation_size(s.relid)::bigint AS total_bytes,
  pg_relation_size(s.relid)::bigint AS table_bytes,
  (pg_total_relation_size(s.relid) - pg_relation_size(s.relid))::bigint AS index_bytes,
  s.n_live_tup::bigint AS live_rows, s.n_dead_tup::bigint AS dead_rows,
  coalesce(s.seq_scan, 0)::bigint AS seq_scan, coalesce(s.idx_scan, 0)::bigint AS idx_scan,
  coalesce(to_char(greatest(s.last_vacuum, s.last_autovacuum), 'YYYY-MM-DD HH24:MI'), 'belum pernah') AS last_vacuum,
  coalesce(to_char(greatest(s.last_analyze, s.last_autoanalyze), 'YYYY-MM-DD HH24:MI'), 'belum pernah') AS last_analyze
FROM pg_stat_user_tables s
ORDER BY pg_total_relation_size(s.relid) DESC
LIMIT 40`

// ReadTables membaca tabel terbesar di satu database.
func (c Client) ReadTables(ctx context.Context, r run.Runner, db string) ([]Table, error) {
	var out []Table
	err := c.Query(ctx, r, db, tableSQL, &out)
	return out, err
}

// Index adalah satu index beserta berapa kali dipakai.
type Index struct {
	Name   string `json:"name"`
	Table  string `json:"table"`
	Scans  int64  `json:"scans"`
	Bytes  int64  `json:"bytes"`
	Unique bool   `json:"is_unique"`
}

const unusedIndexSQL = `SELECT s.schemaname || '.' || s.indexrelname AS name,
  s.relname AS table, s.idx_scan::bigint AS scans,
  pg_relation_size(s.indexrelid)::bigint AS bytes,
  i.indisunique AS is_unique
FROM pg_stat_user_indexes s
JOIN pg_index i ON i.indexrelid = s.indexrelid
WHERE s.idx_scan < 50 AND NOT i.indisprimary
ORDER BY pg_relation_size(s.indexrelid) DESC
LIMIT 20`

// ReadUnusedIndexes membaca index yang hampir tidak pernah dipakai.
func (c Client) ReadUnusedIndexes(ctx context.Context, r run.Runner, db string) ([]Index, error) {
	var out []Index
	err := c.Query(ctx, r, db, unusedIndexSQL, &out)
	return out, err
}

// Statement adalah satu query teragregasi dari pg_stat_statements.
type Statement struct {
	Calls   int64   `json:"calls"`
	TotalMs float64 `json:"total_ms"`
	MeanMs  float64 `json:"mean_ms"`
	Rows    int64   `json:"rows"`
	Query   string  `json:"query"`
}

const statementSQL = `SELECT s.calls::bigint AS calls,
  round(s.total_exec_time)::float8 AS total_ms,
  round(s.mean_exec_time, 1)::float8 AS mean_ms,
  s.rows::bigint AS rows,
  left(regexp_replace(s.query, '\s+', ' ', 'g'), 200) AS query
FROM pg_stat_statements s
JOIN pg_database d ON d.oid = s.dbid
WHERE s.query NOT LIKE '%pg_stat_statements%'
ORDER BY s.total_exec_time DESC
LIMIT 20`

// ReadStatements membaca query paling memakan waktu (butuh ekstensi pg_stat_statements).
func (c Client) ReadStatements(ctx context.Context, r run.Runner) ([]Statement, error) {
	var out []Statement
	err := c.Query(ctx, r, "postgres", statementSQL, &out)
	return out, err
}

// Setting adalah satu parameter konfigurasi server.
type Setting struct {
	Name           string `json:"name"`
	Value          string `json:"setting"`
	Unit           string `json:"unit"`
	Source         string `json:"source"`
	SourceFile     string `json:"sourcefile"`
	Desc           string `json:"short_desc"`
	PendingRestart bool   `json:"pending_restart"`
	Context        string `json:"context"`
}

// Display menampilkan nilai beserta satuannya.
func (s Setting) Display() string {
	if s.Unit == "" {
		return s.Value
	}
	return s.Value + " " + s.Unit
}

const settingSQL = `SELECT name, setting, coalesce(unit, '') AS unit, source,
  coalesce(sourcefile, '') AS sourcefile, short_desc, pending_restart, context
FROM pg_settings
WHERE name = ANY (ARRAY['listen_addresses','port','max_connections','shared_buffers','work_mem',
  'maintenance_work_mem','effective_cache_size','wal_buffers','min_wal_size','max_wal_size',
  'checkpoint_completion_target','random_page_cost','effective_io_concurrency','default_statistics_target',
  'max_worker_processes','max_parallel_workers','max_parallel_workers_per_gather','ssl',
  'password_encryption','log_min_duration_statement','autovacuum','shared_preload_libraries','data_directory'])
ORDER BY name`

// ReadSettings membaca parameter penting beserta asal nilainya.
func (c Client) ReadSettings(ctx context.Context, r run.Runner) ([]Setting, error) {
	var out []Setting
	err := c.Query(ctx, r, "postgres", settingSQL, &out)
	return out, err
}

// HBARule adalah satu baris pg_hba.conf seperti dibaca server.
type HBARule struct {
	Line     int      `json:"line_number"`
	Type     string   `json:"type"`
	Database []string `json:"database"`
	User     []string `json:"user_name"`
	Address  string   `json:"address"`
	Method   string   `json:"auth_method"`
	Error    string   `json:"error"`
}

// Describe menjelaskan satu aturan dalam bahasa manusia.
func (h HBARule) Describe() string {
	from := "socket lokal (program di server ini)"
	switch {
	case h.Error != "":
		return "BARIS RUSAK: " + h.Error
	case h.Type != "local" && h.Address != "":
		from = "alamat " + h.Address
	case h.Type != "local":
		from = "jaringan"
	}
	return "user " + join(h.User) + " ke database " + join(h.Database) + " dari " + from + ", autentikasi " + MethodMeaning(h.Method)
}

func join(xs []string) string {
	if len(xs) == 0 {
		return "(semua)"
	}
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// MethodMeaning menjelaskan metode autentikasi pg_hba.
func MethodMeaning(m string) string {
	switch m {
	case "peer":
		return "peer (nama user sistem harus sama dengan nama role; hanya untuk socket lokal)"
	case "trust":
		return "trust (TANPA password — siapa pun yang bisa konek langsung masuk)"
	case "scram-sha-256":
		return "scram-sha-256 (password, cara paling aman saat ini)"
	case "md5":
		return "md5 (password dengan hash lama; sebaiknya diganti scram-sha-256)"
	case "reject":
		return "reject (koneksi ditolak)"
	case "ident":
		return "ident (bertanya ke server ident di sisi klien; jarang dipakai)"
	}
	return m
}

const hbaSQL = `SELECT line_number::int AS line_number, coalesce(type, '') AS type,
  coalesce(database, '{}') AS database, coalesce(user_name, '{}') AS user_name,
  coalesce(address, '') AS address, coalesce(auth_method, '') AS auth_method,
  coalesce(error, '') AS error
FROM pg_hba_file_rules ORDER BY line_number`

// ReadHBA membaca aturan pg_hba.conf lewat server (sudah termasuk pesan error bila ada baris rusak).
func (c Client) ReadHBA(ctx context.Context, r run.Runner) ([]HBARule, error) {
	var out []HBARule
	err := c.Query(ctx, r, "postgres", hbaSQL, &out)
	return out, err
}

// Extension adalah ekstensi yang terpasang di satu database.
type Extension struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Comment string `json:"comment"`
}

const extensionSQL = `SELECT e.extname AS name, e.extversion AS version,
  coalesce(left(a.comment, 80), '') AS comment
FROM pg_extension e
LEFT JOIN pg_available_extensions a ON a.name = e.extname
ORDER BY e.extname`

// ReadExtensions membaca ekstensi yang aktif di sebuah database.
func (c Client) ReadExtensions(ctx context.Context, r run.Runner, db string) ([]Extension, error) {
	var out []Extension
	err := c.Query(ctx, r, db, extensionSQL, &out)
	return out, err
}
