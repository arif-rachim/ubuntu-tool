package postgres

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	syspg "github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/ui"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
)

// backupModel adalah layar cadangan: daftar berkas cadangan, membuat cadangan baru, memulihkan,
// dan memasang jadwal otomatis.
type backupModel struct {
	env      shared.Env
	client   syspg.Client
	dbs      []string
	dir      string
	files    []syspg.BackupFile
	table    ui.Table
	message  string
	selected syspg.BackupFile
}

// NewBackup membuka layar cadangan & pemulihan.
func NewBackup(env shared.Env, c syspg.Client, dbs []string) *backupModel {
	m := &backupModel{env: env, client: c, dbs: dbs, dir: syspg.DefaultBackupDir, table: ui.Table{Columns: []ui.Column{
		{Title: "Berkas", Width: 28, Flex: 3},
		{Title: "Database", Width: 16, Flex: 1},
		{Title: "Format", Width: 8},
		{Title: "Ukuran", Width: 10, Right: true},
		{Title: "Dibuat", Width: 16, Flex: 1},
	}}}
	m.load()
	return m
}

func (m *backupModel) load() {
	m.files = syspg.FindBackups(m.dir)
	rows := make([][]string, len(m.files))
	for i, f := range m.files {
		format := "custom"
		if !f.Custom() {
			format = "sql"
		}
		rows[i] = []string{f.Name, f.DB, format, shared.Bytes(f.Size), shared.Ago(f.ModTime, m.now())}
	}
	m.table.SetRows(rows)
	m.table.CellStyle = nil
}

func (m *backupModel) now() time.Time {
	if m.env.Now != nil {
		return m.env.Now()
	}
	return time.Now()
}

func (m *backupModel) Title() string { return "Cadangan" }
func (m *backupModel) Init() tea.Cmd { return nil }

func (m *backupModel) Keys() []key.Binding {
	return []key.Binding{b("c", "cadangkan sekarang"), b("enter", "pulihkan berkas"), b("o", "jadwal otomatis"), b("p", "cadangkan role")}
}

func (m *backupModel) HelpText() string {
	return "pg_dump menyalin isi SATU database pada satu titik waktu yang konsisten — aman dijalankan saat aplikasi berjalan. " +
		"Yang TIDAK ikut: role beserta passwordnya, karena itu milik seluruh cluster; cadangkan terpisah dengan pg_dumpall --globals-only (tombol p). " +
		"Format custom (.dump) lebih kecil dan saat dipulihkan bisa dipilih sebagian; format .sql bisa dibuka dengan editor teks. " +
		"Cadangan yang belum pernah diuji pulih belum bisa disebut cadangan — sesekali pulihkan ke database uji. " +
		"Daftar di bawah kosong bila direktori cadangan hanya bisa dibaca user postgres (izin 0750); isinya tetap ada. " +
		"Command setara: sudo -u postgres pg_dump -F c -f BERKAS NAMADB"
}

func (m *backupModel) confirm(p run.Plan) (nav.Screen, tea.Cmd) {
	return m, nav.Push(runflow.Confirm(p, m.env.Deps))
}

func (m *backupModel) Update(msg tea.Msg) (nav.Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case nav.RefreshMsg:
		m.load()
	case nav.ResumedMsg:
		return m.resumed(msg.Result)
	case tea.KeyPressMsg:
		if m.table.HandleKey(msg.String()) {
			return m, nil
		}
		m.message = ""
		switch msg.String() {
		case "c":
			return m, nav.Push(ask.New(BackupForm(m.dbs, m.dir, m.now())))
		case "p":
			return m.confirm(m.client.BackupRolesPlan(m.dir, m.now()))
		case "o":
			return m, nav.Push(ask.New(ScheduleForm(m.dbs, m.dir)))
		case "enter":
			if m.table.Cursor < len(m.files) {
				m.selected = m.files[m.table.Cursor]
				return m, nav.Push(ask.New(RestoreForm(m.selected, m.dbs)))
			}
		}
	}
	return m, nil
}

func (m *backupModel) resumed(res any) (nav.Screen, tea.Cmd) {
	switch r := res.(type) {
	case ask.Result:
		if r.Cancelled {
			return m, nil
		}
		a := r.Answers
		switch r.ID {
		case "pg-backup":
			db := strings.TrimSpace(a["db"].Value())
			dir := strings.TrimSpace(a["dir"].Value())
			format := a["format"].Value()
			m.dir = dir
			return m.confirm(m.client.BackupPlan(db, dir, syspg.BackupName(db, format, m.now()), format))
		case "pg-restore":
			db := strings.TrimSpace(a["db"].Value())
			return m.confirm(m.client.RestorePlan(m.selected.Path, db, m.selected.Custom(), a["clean"].Yes()))
		case "pg-schedule":
			return m.scheduleBackup(a)
		}
	case run.Outcome:
		if r.Approved {
			if r.OK() {
				m.message = "✓ " + r.Plan.Title + " selesai."
			} else {
				m.message = "✗ " + r.Plan.Title + " gagal. Lihat output untuk penyebabnya."
			}
		}
		m.load()
	}
	return m, nil
}

// scheduleBackup menggabungkan penulisan script cadangan dengan pemasangan jadwal cron.
func (m *backupModel) scheduleBackup(a ask.Answers) (nav.Screen, tea.Cmd) {
	dbs := a["dbs"].Values
	if len(dbs) == 0 {
		dbs = m.dbs
	}
	keep, _ := strconv.Atoi(strings.TrimSpace(a["keep"].Value()))
	if keep <= 0 {
		keep = 7
	}
	hour, _ := strconv.Atoi(a["hour"].Value())
	spec := syspg.BackupSchedule{Databases: dbs, Dir: strings.TrimSpace(a["dir"].Value()), KeepDays: keep, Hour: hour}

	cron := schedule.CreateCronPlan(schedule.Spec{
		Name:        "pg-backup",
		Description: fmt.Sprintf("Cadangan PostgreSQL harian (%s)", strings.Join(dbs, ", ")),
		Command:     syspg.BackupScriptPath,
		User:        syspg.SuperUser,
		Freq:        schedule.Frequency{Kind: "daily", Hour: hour},
	})
	steps := append([]run.Command{spec.ScriptCommand()}, cron.Steps...)
	return m.confirm(run.Plan{Title: "Pasang cadangan otomatis harian", Steps: steps})
}

func (m *backupModel) View(width, height int) string {
	t := ui.Current
	lines := []string{"", " " + t.Title.Render(fmt.Sprintf("Cadangan (%d berkas)", len(m.files))) + "   " + t.Muted.Render(m.dir)}
	if m.message != "" {
		lines = append(lines, ui.Wrap(t.Accent.Render(m.message), width, " "))
	}
	top := strings.Join(lines, "\n")
	if len(m.files) == 0 {
		return top + "\n" + ui.EmptyState("Belum ada berkas cadangan di direktori ini",
			"Direktori cadangan hanya bisa dibaca user postgres, jadi daftar ini bisa kosong walau berkasnya ada. "+
				"Periksa dengan: sudo ls -l "+m.dir,
			"tekan c untuk mencadangkan sekarang, atau o untuk memasang jadwal harian", width)
	}
	return ui.FitHeight(top+"\n"+m.table.View(width, height-lipgloss.Height(top)), width, height)
}

// BackupForm menanyakan database dan tujuan cadangan.
func BackupForm(dbs []string, dir string, now time.Time) ask.Form {
	return ask.Form{ID: "pg-backup", Title: "Cadangkan database", Questions: []ask.Question{
		{ID: "db", Header: "Database", Kind: ask.Single, Other: true, Options: options(dbs, nil), Validate: syspg.ValidIdent,
			Prompt: "Database mana yang dicadangkan?",
			Help:   "Satu berkas cadangan berisi satu database. Untuk mencadangkan semuanya sekaligus tiap hari, pakai jadwal otomatis (tombol o)."},
		{ID: "format", Header: "Format", Kind: ask.Single, Prompt: "Format cadangan?", Options: []ask.Option{
			{Value: syspg.FormatCustom, Label: "Custom (.dump)", Description: "Terkompres dan bisa dipulihkan sebagian (pilih tabel tertentu) dengan pg_restore.", Recommended: true},
			{Value: syspg.FormatPlain, Label: "SQL teks (.sql)", Description: "Berisi perintah SQL biasa; bisa dibaca & diedit, tetapi lebih besar dan pemulihannya selalu utuh."},
		}},
		{ID: "dir", Header: "Direktori", Kind: ask.Text, Prompt: "Simpan di direktori mana?", Default: []string{dir}, Validate: syspg.ValidBackupDir,
			Help: "Direktori dibuat dengan izin 0750 milik user postgres bila belum ada. Salin berkasnya ke server lain supaya cadangan tidak ikut hilang bila server ini rusak."},
	}}
}

// RestoreForm menanyakan ke database mana cadangan dipulihkan.
func RestoreForm(f syspg.BackupFile, dbs []string) ask.Form {
	return ask.Form{ID: "pg-restore", Title: "Pulihkan " + f.Name, Questions: []ask.Question{
		{ID: "db", Header: "Tujuan", Kind: ask.Single, Other: true, Options: options(dbs, nil), Default: def(f.DB), Validate: syspg.ValidIdent,
			Prompt: "Pulihkan ke database mana?",
			Help: "Database tujuan harus sudah ada. Paling aman: buat database baru yang kosong (mis. " + f.DB + "_uji), pulihkan ke sana, " +
				"periksa isinya, baru dipakai."},
		{ID: "clean", Header: "Timpa", Kind: ask.Confirm, Default: []string{ask.ValueNo},
			Prompt: "Hapus dulu tabel yang namanya sama di database tujuan?",
			Options: []ask.Option{
				{Label: "Ya, kembalikan persis seperti cadangan", Description: "Tabel dengan nama sama dihapus lalu dibuat ulang dari cadangan. Data yang masuk setelah cadangan dibuat hilang.", Risk: risk.Dangerous},
				{Label: "Tidak, tambahkan saja", Description: "Cocok untuk database tujuan yang masih kosong. Bila tabelnya sudah ada, pemulihan akan gagal — itu justru pengaman.", Recommended: true},
			}},
	}}
}

// ScheduleForm adalah wizard cadangan otomatis harian.
func ScheduleForm(dbs []string, dir string) ask.Form {
	hours := []ask.Option{
		{Value: "2", Label: "02:00", Description: "Jam sepi untuk kebanyakan aplikasi.", Recommended: true},
		{Value: "3", Label: "03:00", Description: "Bila jam 2 sudah dipakai jadwal lain."},
		{Value: "23", Label: "23:00", Description: "Sebelum tengah malam, mis. agar masuk hari kerja yang sama."},
	}
	return ask.Form{ID: "pg-schedule", Title: "Cadangan otomatis", Questions: []ask.Question{
		{ID: "dbs", Header: "Database", Kind: ask.Multi, Options: options(dbs, nil), Prompt: "Database mana saja yang dicadangkan tiap hari?",
			Help: "Role & password ikut dicadangkan otomatis (pg_dumpall --globals-only), karena tanpa itu database yang dipulihkan tidak bisa diakses aplikasi."},
		{ID: "dir", Header: "Direktori", Kind: ask.Text, Prompt: "Simpan di direktori mana?", Default: []string{dir}, Validate: syspg.ValidBackupDir,
			Help: "Pastikan disknya cukup: cadangan harian dikali jumlah hari yang disimpan. Lihat sisa disk di modul Disk & Storage."},
		{ID: "keep", Header: "Simpan", Kind: ask.Text, Prompt: "Cadangan disimpan berapa hari?", Default: []string{"7"}, Validate: validDays,
			Help: "Cadangan yang lebih tua dari ini dihapus otomatis supaya disk tidak penuh."},
		{ID: "hour", Header: "Jam", Kind: ask.Single, Options: hours, Other: true, Validate: validHour, Prompt: "Dijalankan jam berapa tiap hari?",
			Help: "Pilih jam paling sepi. Cadangan memakai I/O disk, jadi hindari jam sibuk aplikasi."},
	}}
}

func validDays(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 365 {
		return inputError("isi angka 1–365")
	}
	return nil
}

func validHour(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 23 {
		return inputError("isi jam 0–23")
	}
	return nil
}

func def(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
