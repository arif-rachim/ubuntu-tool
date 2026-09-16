package schedule

import (
	"fmt"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Frequency adalah pilihan frekuensi di wizard.
type Frequency struct {
	Kind    string // minutes, hourly, daily, weekly, custom
	Minutes int    // untuk minutes
	Hour    int    // untuk daily/weekly
	Minute  int
	Weekday int    // 0=Minggu, untuk weekly
	Custom  string // ekspresi cron 5 kolom, untuk custom
}

// CronExpr mengubah frekuensi menjadi ekspresi cron.
func (f Frequency) CronExpr() string {
	switch f.Kind {
	case "minutes":
		return fmt.Sprintf("*/%d * * * *", f.Minutes)
	case "hourly":
		return fmt.Sprintf("%d * * * *", f.Minute)
	case "daily":
		return fmt.Sprintf("%d %d * * *", f.Minute, f.Hour)
	case "weekly":
		return fmt.Sprintf("%d %d * * %d", f.Minute, f.Hour, f.Weekday)
	}
	return strings.TrimSpace(f.Custom)
}

// OnCalendar mengubah frekuensi menjadi ekspresi systemd timer.
func (f Frequency) OnCalendar() (string, error) {
	switch f.Kind {
	case "minutes":
		return fmt.Sprintf("*:0/%d", f.Minutes), nil
	case "hourly":
		return fmt.Sprintf("*-*-* *:%02d:00", f.Minute), nil
	case "daily":
		return fmt.Sprintf("*-*-* %02d:%02d:00", f.Hour, f.Minute), nil
	case "weekly":
		return fmt.Sprintf("%s *-*-* %02d:%02d:00", []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[f.Weekday], f.Hour, f.Minute), nil
	}
	e, err := ParseCron(f.Custom)
	if err != nil {
		return "", err
	}
	return cronToCalendar(e)
}

// cronToCalendar mengubah ekspresi cron sederhana (tanpa kombinasi tanggal+hari) menjadi OnCalendar.
func cronToCalendar(e CronExpr) (string, error) {
	f := e.fields
	if !f[2].any && !f[4].any {
		return "", fmt.Errorf("kombinasi tanggal dan hari tidak didukung untuk timer; pakai cron")
	}
	list := func(fl field, pad bool) string {
		if fl.any {
			return "*"
		}
		if step := stepOf(fl.raw); step > 0 {
			return fmt.Sprintf("0/%d", step)
		}
		var parts []string
		for _, v := range fl.sorted() {
			if pad {
				parts = append(parts, fmt.Sprintf("%02d", v))
			} else {
				parts = append(parts, fmt.Sprint(v))
			}
		}
		return strings.Join(parts, ",")
	}
	dow := ""
	if !f[4].any {
		var names []string
		for _, d := range f[4].sorted() {
			names = append(names, []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}[d])
		}
		dow = strings.Join(names, ",") + " "
	}
	return fmt.Sprintf("%s*-%s-%s %s:%s:00", dow, list(f[3], true), list(f[2], true), list(f[1], true), list(f[0], true)), nil
}

// Spec adalah jadwal baru yang dibuat lewat wizard.
type Spec struct {
	Name        string // sudah di-slug, tanpa prefix ubt-
	Description string
	Command     string
	User        string
	Freq        Frequency
}

// CronFile menghasilkan isi /etc/cron.d/ubt-NAME.
func CronFile(s Spec) string {
	return fmt.Sprintf(`# Dibuat oleh ubt: %s
# Output dikirim ke journal: journalctl -t %s%s
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
%s %s ( %s ) 2>&1 | logger -t %s%s
`, s.Description, ManagedPrefix, s.Name, s.Freq.CronExpr(), s.User, s.Command, ManagedPrefix, s.Name)
}

// ServiceFile menghasilkan isi unit service untuk timer.
func ServiceFile(s Spec) string {
	return fmt.Sprintf(`# Dibuat oleh ubt
[Unit]
Description=%s

[Service]
Type=oneshot
User=%s
ExecStart=/bin/bash -c %s
`, s.Description, s.User, systemdQuote(s.Command))
}

// TimerFile menghasilkan isi unit timer.
func TimerFile(s Spec, calendar string) string {
	return fmt.Sprintf(`# Dibuat oleh ubt
[Unit]
Description=Jadwal: %s

[Timer]
OnCalendar=%s
Persistent=true
RandomizedDelaySec=30

[Install]
WantedBy=timers.target
`, s.Description, calendar)
}

// systemdQuote mengutip argumen untuk ExecStart (tanda kutip ganda, escape \ " dan %).
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

func writeFile(title, path, content, label string) run.Command {
	return run.Command{
		Title: title, Argv: []string{"install", "-m", "0644", "/dev/stdin", path}, NeedsRoot: true,
		Stdin: content, StdinLabel: label,
		Explain: []run.Line{
			{Token: "install -m 0644", Meaning: "tulis file dengan izin baca untuk semua, tulis hanya root"},
			{Token: "/dev/stdin", Meaning: "isi file diambil dari stdin (ditampilkan di bawah)"},
			{Token: path, Meaning: "lokasi file tujuan"},
		},
		Risk: risk.Caution,
	}
}

// CreateCronPlan membuat jadwal cron di /etc/cron.d.
func CreateCronPlan(s Spec) run.Plan {
	path := "/etc/cron.d/" + ManagedPrefix + s.Name
	return run.Plan{
		Title: "Buat jadwal cron " + ManagedPrefix + s.Name,
		Steps: []run.Command{writeFile("Tulis "+path, path, CronFile(s), "(file cron.d)")},
		Check: nil,
	}
}

// CreateTimerPlan membuat systemd service + timer.
func CreateTimerPlan(s Spec) (run.Plan, error) {
	cal, err := s.Freq.OnCalendar()
	if err != nil {
		return run.Plan{}, err
	}
	name := ManagedPrefix + s.Name
	svc, tmr := "/etc/systemd/system/"+name+".service", "/etc/systemd/system/"+name+".timer"
	return run.Plan{
		Title: "Buat systemd timer " + name,
		Steps: []run.Command{
			{Title: "Periksa ekspresi jadwal", Argv: []string{"systemd-analyze", "calendar", cal},
				Explain: []run.Line{{Token: "systemd-analyze calendar", Meaning: "validasi ekspresi & tampilkan kapan jadwal berikutnya"}}},
			writeFile("Tulis "+svc, svc, ServiceFile(s), "(unit service)"),
			writeFile("Tulis "+tmr, tmr, TimerFile(s, cal), "(unit timer)"),
			{Title: "Muat ulang systemd", Argv: []string{"systemctl", "daemon-reload"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "daemon-reload", Meaning: "baca ulang file unit yang baru ditulis"}}},
			{Title: "Aktifkan timer", Argv: []string{"systemctl", "enable", "--now", name + ".timer"}, NeedsRoot: true,
				Explain: []run.Line{{Token: "enable --now", Meaning: "aktifkan sekarang dan setiap boot"}},
				Effect:  "Jadwal mulai berjalan. Persistent=true: bila server mati saat jadwal lewat, dijalankan segera setelah menyala.",
				Risk:    risk.Caution},
		},
		Check: &run.Command{Title: "Validasi unit", Argv: []string{"systemd-analyze", "verify", svc, tmr},
			Explain: []run.Line{{Token: "systemd-analyze verify", Meaning: "periksa kesalahan penulisan unit sebelum diaktifkan"}}},
	}, nil
}

// RunNowPlan menjalankan jadwal sekarang untuk tes.
func RunNowPlan(j Job) run.Plan {
	switch j.Kind {
	case KindTimer:
		return run.Single(run.Command{Title: "Jalankan " + j.Unit + " sekarang", Argv: []string{"systemctl", "start", j.Unit}, NeedsRoot: true,
			Explain: []run.Line{{Token: "systemctl start " + j.Unit, Meaning: "jalankan service yang biasanya dipicu timer, tanpa menunggu jadwal"}},
			Effect:  "Hasilnya terlihat di log unit ini.", Risk: risk.Caution})
	case KindCronDir:
		return run.Single(run.Command{Title: "Jalankan " + j.Name + " sekarang", Argv: []string{j.Command}, NeedsRoot: true,
			Explain: []run.Line{{Token: j.Command, Meaning: "script yang biasanya dijalankan run-parts"}}, Risk: risk.Caution})
	}
	root := j.User != "" && j.User != "root"
	argv := []string{"bash", "-c", j.Command}
	if root {
		argv = []string{"sudo", "-u", j.User, "bash", "-c", j.Command}
	}
	return run.Single(run.Command{Title: "Jalankan command jadwal sekarang", Argv: argv, NeedsRoot: true,
		Explain: []run.Line{{Token: "bash -c", Meaning: "jalankan command persis seperti di cron"}},
		Effect:  "Catatan: cron memakai environment & PATH yang lebih minim daripada terminal, jadi hasil di sini bisa berbeda.",
		Risk:    risk.Caution})
}

// RemovePlan menghapus jadwal yang dibuat ubt.
func RemovePlan(j Job) run.Plan {
	if j.Kind == KindTimer {
		svc := strings.TrimSuffix(j.Name, ".timer")
		return run.Plan{
			Title: "Hapus timer " + j.Name,
			Steps: []run.Command{
				{Title: "Matikan timer", Argv: []string{"systemctl", "disable", "--now", j.Name}, NeedsRoot: true,
					Explain: []run.Line{{Token: "disable --now", Meaning: "hentikan dan jangan aktifkan saat boot"}}},
				{Title: "Hapus file unit", Argv: []string{"rm", "-f", "/etc/systemd/system/" + svc + ".timer", "/etc/systemd/system/" + svc + ".service"}, NeedsRoot: true,
					Explain: []run.Line{{Token: "rm -f", Meaning: "hapus file unit timer & service buatan ubt"}}, Risk: risk.Caution},
				{Title: "Muat ulang systemd", Argv: []string{"systemctl", "daemon-reload"}, NeedsRoot: true},
			},
		}
	}
	return run.Single(run.Command{Title: "Hapus " + j.Source, Argv: []string{"rm", "-f", j.Source}, NeedsRoot: true,
		Explain: []run.Line{{Token: "rm -f " + j.Source, Meaning: "cron otomatis berhenti menjalankan jadwal di file ini"}}, Risk: risk.Caution})
}
