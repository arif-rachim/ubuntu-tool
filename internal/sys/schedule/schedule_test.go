package schedule

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestDescribe(t *testing.T) {
	tests := map[string]string{
		"0 2 * * 1":         "setiap Senin pukul 02:00",
		"*/15 * * * *":      "setiap 15 menit",
		"30 * * * *":        "setiap jam pada menit ke-30",
		"0 6,18 * * *":      "setiap hari pukul 06:00, 18:00",
		"0 9 * * 1-5":       "setiap hari kerja (Senin–Jumat) pukul 09:00",
		"0 0 1 * *":         "setiap tanggal 1 pukul 00:00",
		"@daily":            "setiap hari pukul 00:00",
		"@reboot":           "setiap kali server dinyalakan",
		"* * * * *":         "setiap menit",
		"0 3 * jan,jul sun": "setiap Minggu pukul 03:00 di bulan Januari, Juli",
		"5 */4 * * *":       "setiap 4 jam pada menit ke-5",
		"0 0 * * 7":         "setiap Minggu pukul 00:00",
		"5-55/10 * * * *":   "setiap jam pada menit 5, 15, 25, 35, 45, 55",
		"30 7-23 * * *":     "setiap jam pada menit ke-30, dari pukul 07:30 sampai 23:30",
	}
	for in, want := range tests {
		e, err := ParseCron(in)
		if err != nil {
			t.Errorf("ParseCron(%q): %v", in, err)
			continue
		}
		if got := e.Describe(); got != want {
			t.Errorf("Describe(%q) = %q, ingin %q", in, got, want)
		}
	}
	for _, bad := range []string{"* * * *", "60 * * * *", "0 25 * * *", "*/0 * * * *", "0 0 * * funday", "@kadang"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("ParseCron(%q) harus error", bad)
		}
	}
}

func TestNext(t *testing.T) {
	loc := time.UTC
	from := time.Date(2026, 9, 16, 21, 40, 30, 0, loc) // Rabu
	e, _ := ParseCron("0 2 * * 1")
	next := e.Next(from, 2)
	if len(next) != 2 || !next[0].Equal(time.Date(2026, 9, 21, 2, 0, 0, 0, loc)) || !next[1].Equal(time.Date(2026, 9, 28, 2, 0, 0, 0, loc)) {
		t.Errorf("next Senin 02:00: %v", next)
	}
	e, _ = ParseCron("*/15 * * * *")
	if n := e.Next(from, 1); !n[0].Equal(time.Date(2026, 9, 16, 21, 45, 0, 0, loc)) {
		t.Errorf("next 15 menit: %v", n)
	}
	// Tanggal ATAU hari: 1 Okt (Kamis) cocok lewat tanggal, Senin 21 Sep cocok lewat hari.
	e, _ = ParseCron("0 0 1 * mon")
	if n := e.Next(from, 1); !n[0].Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, loc)) {
		t.Errorf("tanggal ATAU hari: %v", n)
	}
	e, _ = ParseCron("0 0 29 2 *")
	if n := e.Next(from, 1); len(n) != 1 || n[0].Year() != 2028 {
		t.Errorf("29 Februari berikutnya 2028: %v", n)
	}
}

func TestParseCronLines(t *testing.T) {
	now := time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC)
	content := `SHELL=/bin/sh
PATH=/usr/bin:/bin
# komentar
17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly
@reboot root /tidak/ada/script.sh
5-55/10 * * * * root command -v debian-sa1 > /dev/null && debian-sa1 1 1
0 3 * * * www-data program-yang-tidak-ada-ubt --x
`
	jobs := ParseCronLines(content, "/etc/crontab", true, "", now)
	if len(jobs) != 4 {
		t.Fatalf("%d job: %+v", len(jobs), jobs)
	}
	if jobs[0].User != "root" || jobs[0].Human != "setiap jam pada menit ke-17" || jobs[0].Problem != "" {
		t.Errorf("job 1: %+v", jobs[0])
	}
	if !strings.Contains(jobs[1].Problem, "tidak ditemukan") || jobs[1].Human != "setiap kali server dinyalakan" {
		t.Errorf("@reboot dengan script hilang: %+v", jobs[1])
	}
	if !strings.Contains(jobs[3].Problem, "tidak ditemukan di PATH") || jobs[3].User != "www-data" {
		t.Errorf("program tidak ada: %+v", jobs[3])
	}
	user := ParseCronLines("*/5 * * * * /usr/bin/true\n", "crontab developer", false, "developer", now)
	if len(user) != 1 || user[0].Kind != KindUserCron || user[0].User != "developer" || user[0].Command != "/usr/bin/true" {
		t.Errorf("crontab user: %+v", user)
	}
}

func TestParseTimersDanShow(t *testing.T) {
	jobs := ParseTimers(`[{"next":1789574400000000,"left":1,"last":1789573807864541,"passed":1,"unit":"sysstat-collect.timer","activates":"sysstat-collect.service"},{"next":null,"left":null,"last":0,"passed":0,"unit":"ubt-backup.timer","activates":"ubt-backup.service"}]`)
	if len(jobs) != 2 || jobs[0].Next.IsZero() || !jobs[1].Next.IsZero() || !jobs[1].Managed {
		t.Fatalf("%+v", jobs)
	}
	props := ParseTimerShow("Id=apt-daily.timer\nTimersCalendar={ OnCalendar=*-*-* 06,18:00:00 ; next_elapse=Thu 2026-09-17 06:00:00 +04 }\n\nId=apt-daily.service\nResult=exit-code\n")
	if OnCalendar(props["apt-daily.timer"]["TimersCalendar"]) != "*-*-* 06,18:00:00" || props["apt-daily.service"]["Result"] != "exit-code" {
		t.Errorf("%+v", props)
	}
	for in, want := range map[string]string{
		"*-*-* *:00/10:00":   "setiap 10 menit",
		"daily":              "setiap hari pukul 00:00",
		"*-*-* 02:30:00":     "setiap hari pukul 02:30",
		"Sun *-*-* 03:10:00": "setiap Minggu pukul 03:10",
		"*-*-* 07..23:30:00": "setiap jam pada menit ke-30, dari pukul 07:30 sampai 23:30",
	} {
		if got := DescribeCalendar(in); got != want {
			t.Errorf("DescribeCalendar(%q) = %q, ingin %q", in, got, want)
		}
	}
}

func TestReadAll(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "crontab"), []byte("17 * * * * root cd / && run-parts --report /etc/cron.hourly\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "cron.d"), 0o755)
	os.WriteFile(filepath.Join(dir, "cron.d", "ubt-backup"), []byte("0 2 * * * root /usr/bin/true\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "cron.d", "diabaikan.dpkg-old"), []byte("* * * * * root /usr/bin/true\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "cron.daily"), 0o755)
	os.WriteFile(filepath.Join(dir, "cron.daily", "logrotate"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(dir, "cron.daily", "rusak"), []byte("#!/bin/sh\n"), 0o644)
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"crontab -l": {Stderr: "no crontab for developer", Err: os.ErrNotExist},
		"systemctl list-timers --all --output=json --no-pager": {Stdout: `[{"next":1789574400000000,"last":0,"unit":"apt-daily.timer","activates":"apt-daily.service"}]`},
		"systemctl show -p Id,TimersCalendar,Result,Description,Persistent --no-pager apt-daily.timer apt-daily.service": {
			Stdout: "Id=apt-daily.timer\nTimersCalendar={ OnCalendar=daily ; next_elapse=x }\n\nId=apt-daily.service\nDescription=Daily apt download activities\nResult=success\n",
		},
	}}
	jobs, err := ReadAll(context.Background(), fake, Sources{Crontab: filepath.Join(dir, "crontab"), CronD: filepath.Join(dir, "cron.d"), CronDir: dir}, "developer", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, j := range jobs {
		names = append(names, j.Name)
	}
	if len(jobs) != 5 || jobs[0].Name != "rusak" || jobs[1].Name != "ubt-backup" || !jobs[1].Managed {
		t.Errorf("urutan (bermasalah, buatan ubt, lalu jadwal terdekat): %v", names)
	}
	for _, j := range jobs {
		if j.Name == "apt-daily.timer" && (j.Human != "setiap hari pukul 00:00" || j.Command != "Daily apt download activities") {
			t.Errorf("timer: %+v", j)
		}
	}
}

func TestPlansDanFile(t *testing.T) {
	spec := Spec{Name: "backup-db", Description: "Backup database", Command: `pg_dump app > "/backup/app-$(date +%F).sql"`, User: "postgres",
		Freq: Frequency{Kind: "daily", Hour: 2, Minute: 30}}
	if got := spec.Freq.CronExpr(); got != "30 2 * * *" {
		t.Errorf("cron: %s", got)
	}
	cal, _ := spec.Freq.OnCalendar()
	if cal != "*-*-* 02:30:00" {
		t.Errorf("calendar: %s", cal)
	}
	cf := CronFile(spec)
	if !strings.Contains(cf, "30 2 * * * postgres ( pg_dump") || !strings.Contains(cf, "| logger -t ubt-backup-db") || !strings.Contains(cf, "PATH=") {
		t.Errorf("file cron:\n%s", cf)
	}
	if svc := ServiceFile(spec); !strings.Contains(svc, `ExecStart=/bin/bash -c "pg_dump app > \"/backup/app-$(date +%%F).sql\""`) || !strings.Contains(svc, "User=postgres") {
		t.Errorf("service (escape %% dan kutip):\n%s", svc)
	}

	p, err := CreateTimerPlan(spec)
	if err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, s := range p.Sequence() {
		cmds = append(cmds, s.Command.Preview(false))
	}
	want := "systemd-analyze calendar '*-*-* 02:30:00'|sudo install -m 0644 /dev/stdin /etc/systemd/system/ubt-backup-db.service|sudo install -m 0644 /dev/stdin /etc/systemd/system/ubt-backup-db.timer|sudo systemctl daemon-reload|systemd-analyze verify /etc/systemd/system/ubt-backup-db.service /etc/systemd/system/ubt-backup-db.timer|sudo systemctl enable --now ubt-backup-db.timer"
	if strings.Join(cmds, "|") != want {
		t.Errorf("plan timer:\n%s", strings.Join(cmds, "\n"))
	}

	weekly := Frequency{Kind: "weekly", Weekday: 1, Hour: 3}
	if c, _ := weekly.OnCalendar(); c != "Mon *-*-* 03:00:00" {
		t.Errorf("weekly: %s", c)
	}
	custom := Frequency{Kind: "custom", Custom: "*/10 9-17 * * mon-fri"}
	if c, err := custom.OnCalendar(); err != nil || c != "Mon,Tue,Wed,Thu,Fri *-*-* 09,10,11,12,13,14,15,16,17:0/10:00" {
		t.Errorf("custom: %s %v", c, err)
	}
	if _, err := (Frequency{Kind: "custom", Custom: "0 0 1 * mon"}).OnCalendar(); err == nil {
		t.Error("tanggal+hari tidak didukung timer")
	}
	if n, err := SlugName("  Backup DB Harian!! "); err != nil || n != "backup-db-harian" {
		t.Errorf("slug: %q %v", n, err)
	}
	if rm := RemovePlan(Job{Kind: KindTimer, Name: "ubt-backup-db.timer"}); len(rm.Steps) != 3 {
		t.Errorf("hapus timer: %+v", rm)
	}
}
