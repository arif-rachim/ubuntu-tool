package schedule

import (
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func answers(kv map[string]string) ask.Answers {
	a := ask.Answers{}
	for k, v := range kv {
		switch k {
		case "desc", "command", "time", "cron":
			a[k] = ask.Answer{Text: v}
		default:
			a[k] = ask.Answer{Values: []string{v}}
		}
	}
	return a
}

func TestPlanFromAnswers(t *testing.T) {
	p, err := PlanFromAnswers(answers(map[string]string{
		"desc": "Backup DB", "command": "/usr/local/bin/backup.sh", "freq": "weekly", "weekday": "0", "time": "03:15", "user": "root", "kind": "timer",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var timer string
	for _, s := range p.Steps {
		if strings.HasSuffix(s.Argv[len(s.Argv)-1], ".timer") && s.Stdin != "" {
			timer = s.Stdin
		}
	}
	if !strings.Contains(timer, "OnCalendar=Sun *-*-* 03:15:00") || !strings.Contains(p.Title, "ubt-backup-db") {
		t.Errorf("timer mingguan:\n%s", timer)
	}

	p, err = PlanFromAnswers(answers(map[string]string{
		"desc": "Bersihkan tmp", "command": "find /tmp -mtime +7 -delete", "freq": "15", "user": "developer", "kind": "cron",
	}))
	if err != nil || len(p.Steps) != 1 || !strings.Contains(p.Steps[0].Stdin, "*/15 * * * * developer ( find /tmp -mtime +7 -delete )") {
		t.Errorf("cron 15 menit: %+v %v", p, err)
	}
}

func TestValidasiWizard(t *testing.T) {
	if validateTime("24:00") == nil || validateTime("2:5") != nil || validateTime("jam 2") == nil {
		t.Error("validateTime salah")
	}
	if validateCommand("") == nil || validateCommand("a\nb") == nil || validateCommand("/bin/true") != nil {
		t.Error("validateCommand salah")
	}
	f := NewForm("developer")
	var cronQ ask.Question
	for _, q := range f.Questions {
		if q.ID == "cron" {
			cronQ = q
		}
	}
	err := cronQ.Validate("0 9 * * 1-5")
	var w *ask.Warning
	if err == nil || !strings.Contains(err.Error(), "hari kerja") {
		t.Errorf("ekspresi valid harus menampilkan artinya sebagai peringatan: %v %T", err, w)
	}
	if cronQ.Validate("0 9 * *") == nil {
		t.Error("ekspresi tidak lengkap harus ditolak")
	}
	if !cronQ.When(ask.Answers{"freq": {Values: []string{"custom"}}}) || cronQ.When(ask.Answers{"freq": {Values: []string{"daily"}}}) {
		t.Error("pertanyaan ekspresi hanya untuk custom")
	}
}
