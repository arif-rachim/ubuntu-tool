package schedule

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Kind adalah jenis jadwal.
type Kind string

const (
	KindTimer     Kind = "timer"    // systemd timer
	KindCron      Kind = "cron"     // baris cron (crontab user, /etc/crontab, /etc/cron.d)
	KindCronDir   Kind = "cron-dir" // script di /etc/cron.{hourly,daily,weekly,monthly}
	KindUserCron  Kind = "crontab"  // crontab milik user saat ini
	ManagedPrefix      = "ubt-"     // nama jadwal yang dibuat ubt
)

// Job adalah satu jadwal dari sumber mana pun.
type Job struct {
	Kind     Kind
	Name     string // unit timer, nama file cron.d, atau nama script
	Source   string // file asal
	Schedule string // ekspresi asli (cron atau OnCalendar)
	Human    string // penjelasan manusia
	User     string
	Command  string
	Next     time.Time
	Last     time.Time
	Unit     string // service yang dijalankan timer
	Problem  string // masalah yang terdeteksi
	Managed  bool   // dibuat oleh ubt (boleh dihapus dari ubt)
}

// ParseCronLines mem-parse isi file cron. withUser=true untuk /etc/crontab dan /etc/cron.d (ada kolom user).
func ParseCronLines(content, source string, withUser bool, defaultUser string, now time.Time) []Job {
	var jobs []Job
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Baris variabel (PATH=..., SHELL=...) dilewati.
		if eq := strings.Index(line, "="); eq > 0 && !strings.ContainsAny(line[:eq], " \t") {
			continue
		}
		f := strings.Fields(line)
		var expr string
		var rest []string
		if strings.HasPrefix(f[0], "@") {
			expr, rest = f[0], f[1:]
		} else {
			if len(f) < 6 {
				continue
			}
			expr, rest = strings.Join(f[:5], " "), f[5:]
		}
		user := defaultUser
		if withUser {
			if len(rest) < 2 {
				continue
			}
			user, rest = rest[0], rest[1:]
		}
		j := Job{Kind: KindCron, Name: filepath.Base(source), Source: source, Schedule: expr, User: user, Command: strings.Join(rest, " ")}
		if defaultUser != "" && !withUser {
			j.Kind = KindUserCron
		}
		if e, err := ParseCron(expr); err != nil {
			j.Problem = "ekspresi tidak valid: " + err.Error()
		} else {
			j.Human = e.Describe()
			if next := e.Next(now, 1); len(next) > 0 {
				j.Next = next[0]
			}
		}
		if p := commandProblem(j.Command); p != "" && j.Problem == "" {
			j.Problem = p
		}
		j.Managed = strings.HasPrefix(filepath.Base(source), ManagedPrefix)
		jobs = append(jobs, j)
	}
	return jobs
}

// commandProblem memeriksa apakah program pertama dalam command ada.
func commandProblem(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return "command kosong"
	}
	prog := f[0]
	switch prog {
	case "cd", "test", "[", "command", "if", "for", "true", "false", "echo", "exec", "{", "(":
		return ""
	}
	if strings.ContainsAny(prog, "$`") {
		return ""
	}
	if strings.HasPrefix(prog, "/") {
		st, err := os.Stat(prog)
		switch {
		case err != nil:
			return "program " + prog + " tidak ditemukan"
		case st.Mode()&0o111 == 0:
			return "program " + prog + " tidak executable (chmod +x)"
		}
		return ""
	}
	if _, err := exec.LookPath(prog); err != nil {
		return "program " + prog + " tidak ditemukan di PATH (cron memakai PATH minim; tulis path lengkap)"
	}
	return ""
}

// timerJSON adalah satu baris `systemctl list-timers --output=json`.
type timerJSON struct {
	Next      *int64 `json:"next"`
	Last      *int64 `json:"last"`
	Unit      string `json:"unit"`
	Activates string `json:"activates"`
}

// ParseTimers mem-parse output JSON list-timers.
func ParseTimers(out string) []Job {
	var raw []timerJSON
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &raw) != nil {
		return nil
	}
	jobs := make([]Job, 0, len(raw))
	for _, r := range raw {
		j := Job{Kind: KindTimer, Name: r.Unit, Unit: r.Activates, User: "root", Managed: strings.HasPrefix(r.Unit, ManagedPrefix)}
		if r.Next != nil && *r.Next > 0 {
			j.Next = time.UnixMicro(*r.Next)
		}
		if r.Last != nil && *r.Last > 0 {
			j.Last = time.UnixMicro(*r.Last)
		}
		jobs = append(jobs, j)
	}
	return jobs
}

// ParseTimerShow mengambil OnCalendar & Result service dari `systemctl show` beberapa unit.
func ParseTimerShow(out string) map[string]map[string]string {
	props := map[string]map[string]string{}
	cur := map[string]string{}
	flush := func() {
		if id := cur["Id"]; id != "" {
			props[id] = cur
		}
		cur = map[string]string{}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	flush()
	return props
}

// OnCalendar mengambil ekspresi dari TimersCalendar ("{ OnCalendar=*-*-* 06,18:00:00 ; next_elapse=... }").
func OnCalendar(timersCalendar string) string {
	i := strings.Index(timersCalendar, "OnCalendar=")
	if i < 0 {
		return ""
	}
	rest := timersCalendar[i+len("OnCalendar="):]
	if j := strings.Index(rest, " ;"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// DescribeCalendar menjelaskan OnCalendar yang umum.
func DescribeCalendar(expr string) string {
	switch expr {
	case "":
		return "berdasarkan waktu sejak boot/aktivasi"
	case "hourly", "*-*-* *:00:00":
		return "setiap jam"
	case "daily", "*-*-* 00:00:00":
		return "setiap hari pukul 00:00"
	case "weekly", "Mon *-*-* 00:00:00":
		return "setiap Senin pukul 00:00"
	case "monthly", "*-*-01 00:00:00":
		return "setiap tanggal 1 pukul 00:00"
	}
	days := map[string]string{"Mon": "Senin", "Tue": "Selasa", "Wed": "Rabu", "Thu": "Kamis", "Fri": "Jumat", "Sat": "Sabtu", "Sun": "Minggu"}
	if dow, rest, ok := strings.Cut(expr, " "); ok && strings.HasPrefix(rest, "*-*-* ") {
		var names []string
		for _, d := range strings.Split(dow, ",") {
			if n, ok := days[d]; ok {
				names = append(names, n)
			}
		}
		if len(names) > 0 && len(names) == len(strings.Split(dow, ",")) {
			return "setiap " + strings.Join(names, ", ") + " pukul " + strings.TrimSuffix(strings.TrimPrefix(rest, "*-*-* "), ":00")
		}
	}
	if strings.HasPrefix(expr, "*-*-* ") {
		t := strings.TrimPrefix(expr, "*-*-* ")
		if h, m, ok := strings.Cut(strings.TrimSuffix(t, ":00"), ":"); ok && strings.Contains(h, "..") {
			from, to, _ := strings.Cut(h, "..")
			return fmt.Sprintf("setiap jam pada menit ke-%s, dari pukul %s:%s sampai %s:%s", strings.TrimLeft(m, "0"), from, m, to, m)
		}
		if m := strings.TrimSuffix(strings.TrimPrefix(t, "*:"), ":00"); strings.HasPrefix(t, "*:") {
			if base, step, ok := strings.Cut(m, "/"); ok && (base == "00" || base == "0") {
				return "setiap " + strings.TrimLeft(step, "0") + " menit"
			}
		}
		return "setiap hari pukul " + strings.TrimSuffix(t, ":00")
	}
	return expr
}

// Sources adalah lokasi yang dibaca (diganti saat test).
type Sources struct {
	Crontab string // /etc/crontab
	CronD   string // /etc/cron.d
	CronDir string // /etc (untuk cron.hourly, dll.)
}

// DefaultSources untuk sistem sungguhan.
var DefaultSources = Sources{Crontab: "/etc/crontab", CronD: "/etc/cron.d", CronDir: "/etc"}

// ReadAll membaca semua jadwal.
func ReadAll(ctx context.Context, r run.Runner, src Sources, username string, now time.Time) ([]Job, error) {
	var jobs []Job
	if out, stderr, err := r.Capture(ctx, run.Command{Argv: []string{"crontab", "-l"}}); err == nil {
		jobs = append(jobs, ParseCronLines(out, "crontab "+username, false, username, now)...)
	} else if !strings.Contains(stderr, "no crontab") {
		_ = stderr // crontab tidak terpasang: dilewati
	}
	if data, err := os.ReadFile(src.Crontab); err == nil {
		jobs = append(jobs, ParseCronLines(string(data), src.Crontab, true, "", now)...)
	}
	files, _ := filepath.Glob(filepath.Join(src.CronD, "*"))
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") || strings.Contains(base, ".") || strings.HasSuffix(base, "~") {
			continue // cron mengabaikan file berisi titik
		}
		if data, err := os.ReadFile(f); err == nil {
			jobs = append(jobs, ParseCronLines(string(data), f, true, "", now)...)
		}
	}
	for _, period := range []string{"hourly", "daily", "weekly", "monthly"} {
		dir := filepath.Join(src.CronDir, "cron."+period)
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") || e.Name() == "0anacron" {
				continue
			}
			j := Job{Kind: KindCronDir, Name: e.Name(), Source: dir, User: "root", Command: filepath.Join(dir, e.Name()),
				Schedule: "@" + period, Human: map[string]string{"hourly": "setiap jam", "daily": "setiap hari", "weekly": "setiap minggu", "monthly": "setiap bulan"}[period] + " (lewat run-parts/anacron)"}
			if info, err := e.Info(); err == nil && info.Mode()&0o111 == 0 {
				j.Problem = "script tidak executable, run-parts melewatinya (chmod +x)"
			}
			jobs = append(jobs, j)
		}
	}

	out, _, err := r.Capture(ctx, run.Command{Argv: []string{"systemctl", "list-timers", "--all", "--output=json", "--no-pager"}})
	if err == nil {
		timers := ParseTimers(out)
		if len(timers) > 0 {
			args := []string{"systemctl", "show", "-p", "Id,TimersCalendar,Result,Description,Persistent", "--no-pager"}
			for _, t := range timers {
				args = append(args, t.Name, t.Unit)
			}
			show, _, _ := r.Capture(ctx, run.Command{Argv: args})
			props := ParseTimerShow(show)
			for i := range timers {
				t := &timers[i]
				t.Schedule = OnCalendar(props[t.Name]["TimersCalendar"])
				t.Human = DescribeCalendar(t.Schedule)
				t.Command = props[t.Unit]["Description"]
				if res := props[t.Unit]["Result"]; res != "" && res != "success" {
					t.Problem = "eksekusi terakhir gagal (" + res + ")"
				}
			}
		}
		jobs = append(jobs, timers...)
	}

	sort.SliceStable(jobs, func(i, j int) bool {
		if (jobs[i].Problem != "") != (jobs[j].Problem != "") {
			return jobs[i].Problem != ""
		}
		if jobs[i].Managed != jobs[j].Managed {
			return jobs[i].Managed
		}
		ni, nj := jobs[i].Next, jobs[j].Next
		if ni.IsZero() != nj.IsZero() {
			return !ni.IsZero()
		}
		return ni.Before(nj)
	})
	return jobs, nil
}

// SlugName mengubah deskripsi bebas menjadi nama jadwal yang aman ("Backup DB" → "backup-db").
func SlugName(s string) (string, error) {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) < 2 {
		return "", fmt.Errorf("nama minimal 2 huruf/angka")
	}
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	return name, nil
}
