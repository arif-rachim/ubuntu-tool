// Package postgres membaca kondisi PostgreSQL yang terpasang langsung di Ubuntu (paket apt):
// cluster mana yang ada, versinya, portnya, isi database & role-nya — dan membangun command untuk
// mengelolanya. Murni pengumpul data: semua perubahan dikembalikan sebagai run.Plan.
//
// Semua pembacaan dilakukan sebagai user sistem `postgres` lewat `runuser`, karena itulah pemilik
// cluster bawaan Ubuntu (autentikasi `peer` di socket lokal).
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Availability adalah hasil pengecekan apakah PostgreSQL bisa dipakai.
type Availability int

const (
	Ready        Availability = iota
	NotInstalled              // paket postgresql belum terpasang
	NoCluster                 // server terpasang tetapi belum ada cluster
	Down                      // cluster ada tetapi tidak berjalan
	NoPermission              // butuh sudo untuk membaca (ubt belum diizinkan)
	Unknown
)

// SuperUser adalah user sistem pemilik cluster PostgreSQL di Ubuntu.
const SuperUser = "postgres"

// Cluster adalah satu baris `pg_lsclusters`.
type Cluster struct {
	Version string
	Name    string
	Port    int
	Status  string // online, down, ...
	Owner   string
	DataDir string
	LogFile string
}

// Online melaporkan apakah cluster sedang berjalan.
func (c Cluster) Online() bool { return c.Status == "online" }

// Major adalah nomor versi mayor cluster (16, 17, 18, …); 0 bila tidak terbaca.
func (c Cluster) Major() int {
	major, _, _ := strings.Cut(c.Version, ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 0
	}
	return n
}

// ID adalah penanda singkat cluster: "17/main".
func (c Cluster) ID() string { return c.Version + "/" + c.Name }

// Unit adalah nama unit systemd cluster ini.
func (c Cluster) Unit() string { return "postgresql@" + c.Version + "-" + c.Name + ".service" }

// ConfDir adalah direktori konfigurasi cluster.
func (c Cluster) ConfDir() string { return "/etc/postgresql/" + c.Version + "/" + c.Name }

// ConfFile adalah postgresql.conf cluster ini.
func (c Cluster) ConfFile() string { return c.ConfDir() + "/postgresql.conf" }

// HBAFile adalah pg_hba.conf cluster ini (aturan siapa boleh konek dari mana).
func (c Cluster) HBAFile() string { return c.ConfDir() + "/pg_hba.conf" }

// Nama berkas konfigurasi tambahan milik ubt di dalam conf.d cluster. Memakai berkas terpisah per
// keperluan berarti postgresql.conf bawaan tidak pernah diubah dan tiap wizard tidak saling menimpa.
const (
	DropInTuning     = "10-ubt-tuning.conf"
	DropInListen     = "20-ubt-listen.conf"
	DropInStatements = "30-ubt-statements.conf"
)

// DropIn adalah path berkas konfigurasi tambahan milik ubt di cluster ini.
func (c Cluster) DropIn(name string) string { return c.ConfDir() + "/conf.d/" + name }

// ParseClusters membaca output `pg_lsclusters --no-header`.
// Kolom: Ver Cluster Port Status Owner Data-directory Log-file
func ParseClusters(out string) []Cluster {
	var cs []Cluster
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 6 || strings.EqualFold(f[0], "Ver") {
			continue
		}
		port, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		c := Cluster{Version: f[0], Name: f[1], Port: port, Status: f[3], Owner: f[4], DataDir: f[5]}
		if len(f) > 6 {
			c.LogFile = f[6]
		}
		cs = append(cs, c)
	}
	return cs
}

// Status adalah kondisi PostgreSQL secara keseluruhan.
type Status struct {
	Avail         Availability
	ServerBin     string // pg_lsclusters
	ClientBin     string // psql
	Clusters      []Cluster
	Cluster       Cluster // cluster yang sedang dipakai layar
	Error         string
	NeedsSudo     bool // pembacaan gagal karena sudo belum diizinkan di sesi ini
	Summary       Summary
	HasStatements bool // ekstensi pg_stat_statements aktif
}

// Selected mengembalikan cluster aktif, atau cluster pertama.
func (s Status) Selected() Cluster {
	if s.Cluster.Version != "" {
		return s.Cluster
	}
	if len(s.Clusters) > 0 {
		return s.Clusters[0]
	}
	return Cluster{}
}

// Client menjalankan command PostgreSQL sebagai user postgres.
type Client struct {
	LsBin   string // path pg_lsclusters
	PsqlBin string // path psql
	Port    int    // port cluster yang dipakai (0 = bawaan)
}

// NewClient mencari binary PostgreSQL di PATH dan lokasi paket Ubuntu.
func NewClient() Client {
	c := Client{}
	if p, err := exec.LookPath("pg_lsclusters"); err == nil {
		c.LsBin = p
	}
	if p, err := exec.LookPath("psql"); err == nil {
		c.PsqlBin = p
	}
	return c
}

// Installed melaporkan apakah server PostgreSQL terpasang.
func (c Client) Installed() bool { return c.LsBin != "" }

// asPostgres membungkus command supaya berjalan sebagai user sistem postgres.
// runuser dipilih (bukan `sudo -u`) karena lapisan eksekusi ubt yang menambahkan sudo bila perlu.
func asPostgres(argv ...string) run.Command {
	return run.Command{Argv: append([]string{"runuser", "-u", SuperUser, "--"}, argv...), NeedsRoot: true}
}

// psqlArgs membangun argumen psql untuk membaca data: tanpa .psqlrc, tanpa header, berhenti di error.
func (c Client) psqlArgs(db string) []string {
	args := []string{"psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1"}
	if c.Port != 0 {
		args = append(args, "-p", strconv.Itoa(c.Port))
	}
	if db != "" {
		args = append(args, "-d", db)
	}
	return args
}

// jsonQuery membungkus query menjadi satu baris JSON, supaya hasilnya aman dibaca apa pun isinya
// (nama berisi spasi, kutip, baris baru) tanpa menebak pemisah kolom.
func jsonQuery(sql string) string {
	return "SELECT coalesce(json_agg(t)::text, '[]') FROM (" + sql + ") t"
}

// ListCommand membangun command pembacaan daftar cluster.
func (c Client) ListCommand() run.Command {
	cmd := run.Command{Argv: []string{"pg_lsclusters", "--no-header"}}
	cmd.Title, cmd.Effect = "Daftar cluster PostgreSQL", "Hanya membaca."
	cmd.Explain = []run.Line{{Token: "pg_lsclusters", Meaning: "daftar cluster PostgreSQL di server ini beserta versi, port, dan statusnya"}}
	return cmd
}

// QueryCommand membangun command psql read-only yang mengembalikan JSON.
func (c Client) QueryCommand(db, sql string) run.Command {
	cmd := asPostgres(append(c.psqlArgs(db), "-c", jsonQuery(sql))...)
	cmd.Title = "Baca data dari PostgreSQL"
	cmd.Explain = []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres, pemilik cluster"},
		{Token: "psql -X -A -t", Meaning: "psql tanpa file konfigurasi pribadi, keluaran polos tanpa header"},
		{Token: "-c SELECT …", Meaning: "satu query pembacaan; hasilnya dikembalikan sebagai JSON"},
	}
	cmd.Effect = "Hanya membaca."
	return cmd
}

// Query menjalankan query pembacaan dan menaruh hasilnya (array JSON) ke dest.
func (c Client) Query(ctx context.Context, r run.Runner, db, sql string, dest any) error {
	out, stderr, err := r.Capture(ctx, c.QueryCommand(db, sql))
	if err != nil {
		return queryError(stderr, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = "[]"
	}
	if err := json.Unmarshal([]byte(out), dest); err != nil {
		return fmt.Errorf("hasil query tidak terbaca: %w", err)
	}
	return nil
}

// ErrNeedsSudo menandakan pembacaan gagal karena butuh hak root yang belum diizinkan.
var ErrNeedsSudo = errors.New("butuh sudo untuk membaca PostgreSQL")

func queryError(stderr string, err error) error {
	s := strings.ToLower(stderr + " " + err.Error())
	switch {
	case strings.Contains(s, "sudo"), strings.Contains(s, "must be run as root"), strings.Contains(s, "permission denied"):
		return ErrNeedsSudo
	case strings.Contains(s, "could not connect"), strings.Contains(s, "connection refused"), strings.Contains(s, "no such file or directory"):
		return errors.New("tidak bisa konek ke server PostgreSQL: " + firstLine(stderr))
	}
	if msg := firstLine(stderr); msg != "" {
		return errors.New(msg)
	}
	return err
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// Read membaca kondisi PostgreSQL: cluster, ringkasan, database, dan role.
func (c Client) Read(ctx context.Context, r run.Runner, wanted string) Status {
	st := Status{ServerBin: c.LsBin, ClientBin: c.PsqlBin}
	if c.LsBin == "" {
		st.Avail = NotInstalled
		return st
	}
	out, stderr, err := r.Capture(ctx, c.ListCommand())
	if err != nil {
		st.Avail, st.Error = Unknown, firstLine(stderr)
		return st
	}
	st.Clusters = ParseClusters(out)
	if len(st.Clusters) == 0 {
		st.Avail = NoCluster
		return st
	}
	// Tanpa pilihan user, ambil cluster pertama yang berjalan — di server yang punya beberapa
	// versi (mis. 16 sisa bawaan Ubuntu + 18 dari PGDG), yang lama sering sengaja dimatikan.
	st.Cluster = st.Clusters[0]
	for _, cl := range st.Clusters {
		if cl.Online() {
			st.Cluster = cl
			break
		}
	}
	for _, cl := range st.Clusters {
		if cl.ID() == wanted {
			st.Cluster = cl
		}
	}
	if !st.Cluster.Online() {
		st.Avail = Down
		return st
	}
	cc := c
	cc.Port = st.Cluster.Port
	sum, err := cc.ReadSummary(ctx, r)
	switch {
	case errors.Is(err, ErrNeedsSudo):
		st.Avail, st.NeedsSudo = NoPermission, true
		return st
	case err != nil:
		st.Avail, st.Error = Unknown, err.Error()
		return st
	}
	st.Avail, st.Summary, st.HasStatements = Ready, sum, sum.Statements
	return st
}

// InstalledVersions mencari versi PostgreSQL yang paketnya sudah terpasang, dari direktori binary
// per versi yang dipakai Debian/Ubuntu (/usr/lib/postgresql/VERSI/bin). Terbaru lebih dulu.
func InstalledVersions() []string {
	entries, err := os.ReadDir("/usr/lib/postgresql")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}
