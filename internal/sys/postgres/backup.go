package postgres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// DefaultBackupDir adalah tempat cadangan bawaan: di luar direktori data, tetapi tetap milik postgres.
const DefaultBackupDir = "/var/backups/postgresql"

// BackupScriptPath adalah script cadangan otomatis yang dipasang ubt.
const BackupScriptPath = "/usr/local/bin/ubt-pg-backup"

// Format cadangan yang didukung.
const (
	FormatCustom = "custom" // pg_dump -F c: bisa dipulihkan sebagian, bisa dikompres
	FormatPlain  = "plain"  // .sql biasa: bisa dibaca & diedit teksnya
)

// BackupFile adalah satu berkas cadangan yang ditemukan di direktori cadangan.
type BackupFile struct {
	Path    string
	Name    string
	Size    int64
	ModTime time.Time
	Format  string
	DB      string // nama database ditebak dari nama berkas
}

// Custom melaporkan apakah berkas ini format custom (dipulihkan dengan pg_restore).
func (b BackupFile) Custom() bool { return b.Format == FormatCustom }

// BackupName membangun nama berkas cadangan: namadb-2026-09-22-1430.dump.
func BackupName(db string, format string, now time.Time) string {
	ext := ".dump"
	if format == FormatPlain {
		ext = ".sql"
	}
	return run.FileName(db) + "-" + now.Format("2006-01-02-1504") + ext
}

// FindBackups mencari berkas cadangan di sebuah direktori, terbaru dulu.
// Direktori yang tidak bisa dibaca user biasa (mis. 0750 milik postgres) menghasilkan daftar kosong.
func FindBackups(dir string) []BackupFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []BackupFile
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if e.IsDir() || (ext != ".dump" && ext != ".sql") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := BackupFile{Path: filepath.Join(dir, name), Name: name, Size: info.Size(), ModTime: info.ModTime(), Format: FormatCustom}
		if ext == ".sql" {
			f.Format = FormatPlain
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if i := strings.LastIndex(base, "-20"); i > 0 {
			base = base[:i]
		}
		f.DB = base
		out = append(out, f)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ModTime.After(out[b].ModTime) })
	return out
}

// ValidBackupDir memeriksa direktori cadangan: harus path absolut.
func ValidBackupDir(s string) error {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "/") {
		return errors.New("tulis path lengkap, mis. " + DefaultBackupDir)
	}
	if strings.HasPrefix(s, "/etc/") || strings.HasPrefix(s, "/var/lib/postgresql") {
		return errors.New("jangan simpan cadangan di direktori konfigurasi atau direktori data PostgreSQL")
	}
	return nil
}

// ValidBackupFile memeriksa berkas cadangan yang akan dipulihkan.
func ValidBackupFile(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("path berkas cadangan wajib diisi")
	}
	fi, err := os.Stat(s)
	switch {
	case err != nil:
		return errors.New("berkas tidak ditemukan: " + s)
	case fi.IsDir():
		return errors.New(s + " adalah direktori, bukan berkas cadangan")
	}
	return nil
}

// ensureDirCommand membuat direktori cadangan milik user postgres.
func ensureDirCommand(dir string) run.Command {
	return run.Command{
		Title: "Siapkan direktori cadangan " + dir, Argv: []string{"install", "-d", "-m", "0750", "-o", SuperUser, "-g", SuperUser, dir}, NeedsRoot: true,
		Explain: []run.Line{
			{Token: "install -d", Meaning: "buat direktori bila belum ada (tidak error bila sudah ada)"},
			{Token: "-m 0750 -o postgres", Meaning: "hanya user postgres dan grupnya yang bisa membaca isinya — cadangan berisi seluruh data"},
		},
		Effect: "Direktori siap ditulis oleh proses pg_dump yang berjalan sebagai postgres.",
		Risk:   risk.Safe,
	}
}

// BackupPlan membuat cadangan satu database.
func (c Client) BackupPlan(db, dir, file, format string) run.Plan {
	path := filepath.Join(dir, file)
	argv := []string{"pg_dump"}
	explain := []run.Line{
		{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres supaya berhak membaca seluruh isi database"},
		{Token: "pg_dump", Meaning: "salin isi satu database ke berkas; server tetap melayani aplikasi selama proses ini"},
	}
	if format == FormatCustom {
		argv = append(argv, "-F", "c")
		explain = append(explain, run.Line{Token: "-F c", Meaning: "format custom: terkompres, dan saat dipulihkan bisa dipilih tabel tertentu saja"})
	} else {
		explain = append(explain, run.Line{Token: "(format teks)", Meaning: "berkas .sql berisi perintah SQL biasa yang bisa dibuka dengan editor"})
	}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, "-f", path, db)
	explain = append(explain,
		run.Line{Token: "-f " + path, Meaning: "berkas tujuan"},
		run.Line{Token: db, Meaning: "database yang dicadangkan"})

	dump := cmd("Cadangkan database "+db, risk.Caution,
		"Hasilnya berkas "+path+" milik user postgres. Isi cadangan konsisten pada satu titik waktu, jadi aman diambil saat aplikasi berjalan. "+
			"Role & password TIDAK ikut tercadang — pakai cadangan role terpisah untuk itu.",
		explain, asPostgres(argv...))
	dump.Safer = "Salin berkas cadangan ke server/penyimpanan lain: cadangan yang hanya ada di server yang sama ikut hilang bila servernya rusak."
	return run.Plan{Title: "Cadangkan database " + db, Steps: []run.Command{ensureDirCommand(dir), dump}}
}

// BackupRolesPlan mencadangkan role & password (yang tidak ikut di pg_dump per database).
func (c Client) BackupRolesPlan(dir string, now time.Time) run.Plan {
	path := filepath.Join(dir, "roles-"+now.Format("2006-01-02-1504")+".sql")
	argv := []string{"pg_dumpall", "--globals-only"}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	argv = append(argv, "-f", path)
	dump := cmd("Cadangkan role & hak akses", risk.Caution,
		"Berkas ini berisi definisi role beserta hash password-nya — perlakukan seperti berkas rahasia.",
		[]run.Line{
			{Token: "pg_dumpall --globals-only", Meaning: "cadangkan hal yang berlaku untuk seluruh cluster: role, password, tablespace — tanpa isi database"},
			{Token: "-f " + path, Meaning: "berkas tujuan"},
		}, asPostgres(argv...))
	return run.Plan{Title: "Cadangkan role & hak akses", Steps: []run.Command{ensureDirCommand(dir), dump}}
}

// RestorePlan memulihkan cadangan ke sebuah database.
// dropFirst menghapus objek yang ada lebih dulu (--clean), supaya hasilnya persis seperti cadangan.
func (c Client) RestorePlan(file, db string, custom, dropFirst bool) run.Plan {
	var argv []string
	explain := []run.Line{{Token: "runuser -u postgres --", Meaning: "jalankan sebagai user sistem postgres"}}
	if custom {
		argv = []string{"pg_restore", "-d", db}
		explain = append(explain,
			run.Line{Token: "pg_restore -d " + db, Meaning: "tuangkan isi cadangan format custom ke database " + db},
			run.Line{Token: "--single-transaction", Meaning: "semua atau tidak sama sekali: bila ada yang gagal, database kembali seperti semula"})
		argv = append(argv, "--single-transaction")
		if dropFirst {
			argv = append(argv, "--clean", "--if-exists")
			explain = append(explain, run.Line{Token: "--clean --if-exists", Meaning: "hapus dulu tabel/objek yang namanya sama sebelum dituangkan ulang"})
		}
	} else {
		argv = []string{"psql", "-X", "-v", "ON_ERROR_STOP=1", "--single-transaction", "-d", db}
		explain = append(explain,
			run.Line{Token: "psql -f …", Meaning: "jalankan berkas .sql berisi perintah pembentuk tabel & isinya"},
			run.Line{Token: "ON_ERROR_STOP=1", Meaning: "berhenti di error pertama, bukan melanjutkan dengan data setengah jadi"})
	}
	if c.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(c.Port))
	}
	if custom {
		argv = append(argv, file)
	} else {
		argv = append(argv, "-f", file)
	}
	explain = append(explain, run.Line{Token: file, Meaning: "berkas cadangan sumber"})

	lvl, effect := risk.Caution, "Isi cadangan ditambahkan ke database "+db+"."
	if dropFirst {
		lvl = risk.Dangerous
		effect = "Tabel di " + db + " yang namanya sama dengan isi cadangan DIHAPUS lalu dibuat ulang dari cadangan. Data yang masuk setelah cadangan dibuat akan hilang."
	}
	out := cmd("Pulihkan "+filepath.Base(file)+" ke "+db, lvl, effect, explain, asPostgres(argv...))
	out.Safer = "Pulihkan ke database baru yang kosong dulu (mis. " + db + "_uji), periksa isinya, baru pakai untuk menggantikan yang asli."
	return run.Single(out)
}

// BackupSchedule adalah isi wizard cadangan otomatis.
type BackupSchedule struct {
	Databases []string
	Dir       string
	KeepDays  int
	Hour      int
}

// Script merender isi script cadangan otomatis.
func (s BackupSchedule) Script() string {
	var b strings.Builder
	b.WriteString(`#!/bin/bash
# Dibuat oleh ubt — cadangan otomatis PostgreSQL.
# Dijalankan sebagai user postgres lewat jadwal di /etc/cron.d.
set -euo pipefail
umask 077

DIR=` + run.QuoteShell(s.Dir) + `
KEEP=` + strconv.Itoa(s.KeepDays) + `
STAMP=$(date +%Y-%m-%d-%H%M)

mkdir -p "$DIR"
`)
	for _, db := range s.Databases {
		q := run.QuoteShell(db)
		b.WriteString("pg_dump -F c -f \"$DIR/" + run.FileName(db) + "-$STAMP.dump\" " + q + "\n")
	}
	b.WriteString(`pg_dumpall --globals-only -f "$DIR/roles-$STAMP.sql"

# Buang cadangan yang lebih tua dari $KEEP hari.
find "$DIR" -maxdepth 1 -name '*.dump' -mtime +$KEEP -delete
find "$DIR" -maxdepth 1 -name 'roles-*.sql' -mtime +$KEEP -delete

echo "cadangan selesai: $(ls -1 "$DIR" | wc -l) berkas di $DIR"
`)
	return b.String()
}

// ScriptCommand menulis script cadangan otomatis.
func (s BackupSchedule) ScriptCommand() run.Command {
	return run.Command{
		Title: "Tulis script cadangan " + BackupScriptPath, Argv: []string{"install", "-m", "0755", "/dev/stdin", BackupScriptPath}, NeedsRoot: true,
		Stdin: s.Script(), StdinLabel: fmt.Sprintf("(script bash, %d database)", len(s.Databases)),
		Explain: []run.Line{
			{Token: "install -m 0755", Meaning: "tulis script dan jadikan bisa dijalankan"},
			{Token: BackupScriptPath, Meaning: "script yang nanti dipanggil jadwal; bisa juga dijalankan manual untuk menguji"},
		},
		Effect: "Script belum berjalan; langkah berikutnya memasang jadwalnya.",
		Risk:   risk.Caution,
	}
}
