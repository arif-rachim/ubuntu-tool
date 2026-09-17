package logs

import (
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestParseJSONLine(t *testing.T) {
	e, err := ParseJSONLine([]byte(`{"__REALTIME_TIMESTAMP":"1789579501116890","PRIORITY":"3","_SYSTEMD_UNIT":"nginx.service","SYSLOG_IDENTIFIER":"nginx","_PID":"2001","MESSAGE":"bind() to 0.0.0.0:80 failed"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Priority != PrioErr || e.Unit != "nginx.service" || e.PID != 2001 || e.Source() != "nginx" || !e.Time.Equal(time.UnixMicro(1789579501116890)) {
		t.Errorf("%+v", e)
	}

	// Pesan non-UTF-8 dikirim journalctl sebagai array byte.
	e, _ = ParseJSONLine([]byte(`{"MESSAGE":[104,97,108,111,255],"_SYSTEMD_UNIT":"app.service"}`))
	if e.Message != "halo�" || e.Priority != PrioInfo || e.Source() != "app" {
		t.Errorf("array byte: %+v", e)
	}
	// Field berulang menjadi array string; MESSAGE null.
	e, _ = ParseJSONLine([]byte(`{"MESSAGE":null,"SYSLOG_IDENTIFIER":["a","b"]}`))
	if e.Message != "" || e.Identifier != "a" {
		t.Errorf("null/array: %+v", e)
	}
	if _, err := ParseJSONLine([]byte(`{rusak`)); err == nil {
		t.Error("JSON rusak harus error")
	}
}

func TestQueryArgs(t *testing.T) {
	q := Query{Boot: BootPtr(-1), Priority: PrioErr, Lines: 100}
	if got := strings.Join(q.Args(), " "); got != "journalctl -b -1 -p err -n 100 --no-pager" {
		t.Errorf("%s", got)
	}
	q = Query{Boot: BootPtr(0), Kernel: true, Priority: -1, Unit: "ssh.service", Since: "1 hour ago", Grep: "Failed password"}
	if got := run.JoinShell(q.Args()); got != "journalctl -b -k -u ssh.service --since '1 hour ago' --grep 'Failed password' -n 300 --no-pager" {
		t.Errorf("%s", got)
	}
	f := strings.Join(q.FollowArgs(""), " ")
	if !strings.HasSuffix(f, "-n 0 -f") || strings.Count(f, "-n ") != 1 || !strings.Contains(f, "-o json") {
		t.Errorf("follow tanpa cursor: %s", f)
	}
	f = strings.Join(q.FollowArgs("s=abc;i=1"), " ")
	if !strings.HasSuffix(f, "--after-cursor s=abc;i=1 -f") || strings.Contains(f, "-n ") {
		t.Errorf("follow dengan cursor: %s", f)
	}
}

func TestRead(t *testing.T) {
	q := Query{Boot: BootPtr(0), Priority: PrioErr}
	key := run.JoinShell(q.JSONArgs())
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		key: {
			Stdout: `{"MESSAGE":"satu","PRIORITY":"3"}` + "\nrusak\n" + `{"MESSAGE":"dua","PRIORITY":"2"}` + "\n",
			Stderr: "Hint: You are currently not seeing messages from other users and the system.\n",
		},
	}}
	res, err := Read(context.Background(), fake, q)
	if err != nil || len(res.Entries) != 2 || !res.Limited {
		t.Fatalf("%+v %v", res, err)
	}

	fake.Responses[key] = run.FakeResponse{Stdout: "-- No entries --\n", Err: errors.New("exit status 1")}
	if res, err := Read(context.Background(), fake, q); err != nil || len(res.Entries) != 0 {
		t.Errorf("No entries harus kosong tanpa error: %v", err)
	}
	fake.Responses[key] = run.FakeResponse{Stderr: "Failed to add match 'x': Invalid argument", Err: errors.New("exit status 1")}
	if _, err := Read(context.Background(), fake, q); err == nil || !strings.Contains(err.Error(), "Invalid argument") {
		t.Errorf("error journalctl harus diteruskan: %v", err)
	}
}

func TestParseBootsDanHealth(t *testing.T) {
	out := `[{"index":-1,"boot_id":"35ea","first_entry":1789573005589061,"last_entry":1789577451856357},{"index":0,"boot_id":"9879","first_entry":1789577468379002,"last_entry":1789579501117072}]`
	boots, err := ParseBoots(out)
	if err != nil || len(boots) != 2 || boots[0].Index != -1 || boots[1].First.IsZero() {
		t.Fatalf("%+v %v", boots, err)
	}
	dir := t.TempDir()
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"journalctl --disk-usage":                    {Stdout: "Archived and active journals take up 1.5G in the file system."},
		"journalctl --list-boots -o json --no-pager": {Stdout: out},
	}}
	h := ReadHealth(context.Background(), fake, dir)
	if !h.Persistent || h.Usage != 3<<29 || len(h.Boots) != 2 {
		t.Errorf("%+v", h)
	}
	if ReadHealth(context.Background(), fake, filepath.Join(dir, "tidak-ada")).Persistent {
		t.Error("tanpa folder journal tidak persisten")
	}
}

func TestListFilesDanTail(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string, age time.Duration) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		mt := time.Now().Add(-age)
		os.Chtimes(p, mt, mt)
	}
	write("syslog", "a\nb\nc\nd\n", time.Minute)
	write("auth.log", "x\n", time.Second)
	write("syslog.1", "lama\n", time.Hour)
	write("nginx/error.log", "e\n", 2*time.Second)
	write("journal/abc/system.journal", "biner", 0)
	gz := filepath.Join(dir, "syslog.2.gz")
	f, _ := os.Create(gz)
	w := gzip.NewWriter(f)
	w.Write([]byte("g1\ng2\n"))
	w.Close()
	f.Close()

	files, err := ListFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		rel, _ := filepath.Rel(dir, f.Path)
		names = append(names, rel)
	}
	if len(names) != 5 || names[0] != "auth.log" || names[1] != "nginx/error.log" || !files[3].Rotated || strings.Contains(strings.Join(names, " "), "journal") {
		t.Errorf("urutan/isi: %v", names)
	}

	lines, err := Tail(filepath.Join(dir, "syslog"), 2)
	if err != nil || !reflect.DeepEqual(lines, []string{"c", "d"}) {
		t.Errorf("tail: %v %v", lines, err)
	}
	if lines, err := Tail(gz, 10); err != nil || !reflect.DeepEqual(lines, []string{"g1", "g2"}) {
		t.Errorf("tail gz: %v %v", lines, err)
	}
}
