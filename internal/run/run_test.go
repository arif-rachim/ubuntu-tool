package run

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

func TestQuoteShell(t *testing.T) {
	tests := map[string]string{
		"kill":              "kill",
		"-TERM":             "-TERM",
		"/etc/nginx/a.conf": "/etc/nginx/a.conf",
		"user@host:22":      "user@host:22",
		"":                  "''",
		"dua kata":          "'dua kata'",
		"it's":              `'it'\''s'`,
		"$HOME":             "'$HOME'",
		"a;rm -rf /":        "'a;rm -rf /'",
		"*":                 "'*'",
		"baris\nbaru":       "'baris\nbaru'",
	}
	for in, want := range tests {
		if got := QuoteShell(in); got != want {
			t.Errorf("QuoteShell(%q) = %s, ingin %s", in, got, want)
		}
	}
}

func TestPreviewSudoHanyaBilaPerlu(t *testing.T) {
	c := Command{Argv: []string{"ufw", "allow", "8080/tcp"}, NeedsRoot: true}
	if got := c.Preview(false); got != "sudo ufw allow 8080/tcp" {
		t.Errorf("non-root: %s", got)
	}
	if got := c.Preview(true); got != "ufw allow 8080/tcp" {
		t.Errorf("root: %s", got)
	}
	if got := c.ExecArgv(false, true); !reflect.DeepEqual(got, []string{"sudo", "-n", "ufw", "allow", "8080/tcp"}) {
		t.Errorf("argv capture non-root: %v", got)
	}
	plain := Command{Argv: []string{"echo", "halo dunia"}}
	if got := plain.Preview(false); got != "echo 'halo dunia'" {
		t.Errorf("tanpa root: %s", got)
	}
	if c.Argv[0] != "ufw" {
		t.Error("Preview tidak boleh mengubah Argv asli")
	}
}

func TestPlanSequenceDanRisk(t *testing.T) {
	a := Command{Title: "a", Argv: []string{"a"}}
	b := Command{Title: "b", Argv: []string{"b"}, Risk: risk.Caution, NeedsRoot: true}
	chk := Command{Title: "cek", Argv: []string{"sshd", "-t"}}

	p := Plan{Steps: []Command{a, b}, Check: &chk}
	seq := p.Sequence()
	titles := []string{}
	for _, s := range seq {
		titles = append(titles, s.Command.Title)
	}
	if !reflect.DeepEqual(titles, []string{"a", "cek", "b"}) || !seq[1].IsCheck {
		t.Fatalf("Check harus tepat sebelum langkah terakhir: %v", titles)
	}
	if p.Risk() != risk.Caution || !p.NeedsRoot() {
		t.Fatal("Risk/NeedsRoot harus mengambil nilai tertinggi dari semua langkah")
	}
	if got := len(Single(a).Sequence()); got != 1 {
		t.Fatalf("Single: %d langkah", got)
	}
}

func TestOutcome(t *testing.T) {
	ok := StepResult{Status: StatusOK}
	fail := StepResult{Status: StatusFailed}
	skip := StepResult{Status: StatusSkipped}
	if !(Outcome{Approved: true, Results: []StepResult{ok, ok}}).OK() {
		t.Error("semua OK harus OK")
	}
	o := Outcome{Approved: true, Results: []StepResult{ok, fail, skip}}
	if o.OK() || len(o.Executed()) != 2 {
		t.Errorf("gagal di tengah: OK=%v executed=%d", o.OK(), len(o.Executed()))
	}
	if (Outcome{Approved: false}).OK() {
		t.Error("tidak disetujui tidak boleh OK")
	}
}

func TestHistory(t *testing.T) {
	dir := t.TempDir()
	h := &History{Path: filepath.Join(dir, "ubt", "history.log")}

	if got, err := h.Read(0); err != nil || got != nil {
		t.Fatalf("riwayat belum ada harus kosong tanpa error: %v %v", got, err)
	}

	secret := Step{Command: Command{Title: "key", Argv: []string{"tee", "x"}, Stdin: "RAHASIA", Sensitive: true}}
	public := Step{Command: Command{Title: "conf", Argv: []string{"tee", "y"}, Stdin: "isi"}}
	start := time.Now()
	if err := h.Append(NewEntry(secret, true, start, 0, nil)); err != nil {
		t.Fatal(err)
	}
	// Baris rusak di tengah tidak boleh menghilangkan entri lain.
	f, _ := os.OpenFile(h.Path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("{bukan json\n")
	f.Close()
	if err := h.Append(NewEntry(public, false, start, 1, errors.New("gagal"))); err != nil {
		t.Fatal(err)
	}

	got, err := h.Read(0)
	if err != nil || len(got) != 2 {
		t.Fatalf("Read: %d entri, err %v", len(got), err)
	}
	if got[0].Stdin != "" || !got[0].Hidden || !got[0].Sudo {
		t.Errorf("stdin sensitif tidak boleh tersimpan: %+v", got[0])
	}
	if got[1].Stdin != "isi" || got[1].ExitCode != 1 || got[1].Error != "gagal" {
		t.Errorf("entri kedua: %+v", got[1])
	}
	if last, _ := h.Read(1); len(last) != 1 || last[0].Title != "conf" {
		t.Errorf("limit harus mengambil entri terbaru: %+v", last)
	}

	for path, want := range map[string]os.FileMode{filepath.Dir(h.Path): 0o700, h.Path: 0o600} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != want {
			t.Errorf("%s: mode %v, ingin %v", path, st.Mode().Perm(), want)
		}
	}
	data, _ := os.ReadFile(h.Path)
	if strings.Contains(string(data), "RAHASIA") {
		t.Error("isi rahasia bocor ke file riwayat")
	}
}

func drain(t *testing.T, s Stream) ([]string, Event) {
	t.Helper()
	var lines []string
	timeout := time.After(10 * time.Second)
	for {
		ch := make(chan Event, 1)
		go func() { ch <- s.Next() }()
		select {
		case ev := <-ch:
			if ev.Done {
				return lines, ev
			}
			lines = append(lines, ev.Line)
		case <-timeout:
			t.Fatal("stream tidak selesai")
		}
	}
}

func TestStreamOutputUrutanDanExitCode(t *testing.T) {
	s, err := StartStream(context.Background(), []string{"sh", "-c", `echo satu; echo dua >&2; printf '10%%\r50%%\r100%%\n'; printf 'tanpa-newline'; exit 3`}, "")
	if err != nil {
		t.Fatal(err)
	}
	lines, done := drain(t, s)
	want := []string{"satu", "dua", "100%", "tanpa-newline"}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("baris %q, ingin %q", lines, want)
	}
	if done.ExitCode != 3 || done.Err == nil {
		t.Errorf("exit %d err %v, ingin 3 dan error", done.ExitCode, done.Err)
	}
	if again := s.Next(); !again.Done {
		t.Error("Next setelah selesai harus tetap Done")
	}
}

func TestStreamStdinDanTanpaStdin(t *testing.T) {
	s, err := StartStream(context.Background(), []string{"cat"}, "halo\ndunia\n")
	if err != nil {
		t.Fatal(err)
	}
	lines, done := drain(t, s)
	if !reflect.DeepEqual(lines, []string{"halo", "dunia"}) || done.ExitCode != 0 {
		t.Errorf("cat dengan stdin: %q exit %d", lines, done.ExitCode)
	}

	// Tanpa stdin, cat tidak boleh menggantung menunggu input.
	s, err = StartStream(context.Background(), []string{"cat"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, done := drain(t, s); done.ExitCode != 0 {
		t.Errorf("cat tanpa stdin exit %d", done.ExitCode)
	}
}

func TestStreamProgramTidakAda(t *testing.T) {
	_, err := StartStream(context.Background(), []string{"program-yang-tidak-ada-ubt"}, "")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error harus NotFoundError, dapat %v", err)
	}
}

func TestStreamCancelMenghentikanAnakProses(t *testing.T) {
	s, err := StartStream(context.Background(), []string{"sh", "-c", `echo mulai; sleep 30 & wait`}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ev := s.Next(); ev.Line != "mulai" {
		t.Fatalf("baris pertama %+v", ev)
	}
	start := time.Now()
	s.Cancel()
	_, done := drain(t, s)
	if !errors.Is(done.Err, context.Canceled) {
		t.Errorf("error harus context.Canceled, dapat %v", done.Err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("pembatalan terlalu lama; anak proses tidak ikut dihentikan")
	}
}

func TestFakeRunner(t *testing.T) {
	f := &Fake{Responses: map[string]FakeResponse{"ufw status numbered": {Stdout: "Status: active"}}}
	out, _, err := f.Capture(context.Background(), Command{Argv: []string{"ufw", "status", "numbered"}, NeedsRoot: true})
	if err != nil || out != "Status: active" || len(f.Calls) != 1 {
		t.Fatalf("out %q err %v calls %d", out, err, len(f.Calls))
	}
	if _, _, err := f.Capture(context.Background(), Command{Argv: []string{"ls"}}); err == nil {
		t.Fatal("command tidak terdaftar harus error")
	}
}

func TestRealCaptureLocaleC(t *testing.T) {
	out, _, err := Real{}.Capture(context.Background(), Command{Argv: []string{"sh", "-c", "echo $LC_ALL"}})
	if err != nil || strings.TrimSpace(out) != "C" {
		t.Fatalf("Capture harus memakai LC_ALL=C: %q %v", out, err)
	}
	_, _, err = Real{}.Capture(context.Background(), Command{Argv: []string{"sh", "-c", "exit 7"}})
	if ExitCode(err) != 7 {
		t.Fatalf("ExitCode %d", ExitCode(err))
	}
}

func TestScript(t *testing.T) {
	c := Command{Argv: []string{"tee", "/etc/ubt demo.conf"}, NeedsRoot: true, Stdin: "a=1\nUBT_EOF\n"}
	want := "sudo tee '/etc/ubt demo.conf' <<'UBT_EOF_2'\na=1\nUBT_EOF\nUBT_EOF_2"
	if got := c.Script(false); got != want {
		t.Errorf("Script:\n%s\ningin:\n%s", got, want)
	}
	secret := Command{Argv: []string{"tee", "k"}, Stdin: "RAHASIA", Sensitive: true, StdinLabel: "private key"}
	if got := secret.Script(true); strings.Contains(got, "RAHASIA") {
		t.Errorf("rahasia bocor: %s", got)
	}
	if got := (Command{Argv: []string{"ls"}}).Script(false); got != "ls" {
		t.Errorf("tanpa stdin: %s", got)
	}
}

func TestScriptBisaDijalankanShell(t *testing.T) {
	dir := t.TempDir()
	c := Command{Argv: []string{"tee", filepath.Join(dir, "out file")}, Stdin: "baris '1'\n$HOME tetap literal"}
	if _, _, err := (Real{}).Capture(context.Background(), Command{Argv: []string{"sh", "-c", "exec >/dev/null\n" + c.Script(false)}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "out file"))
	if string(got) != "baris '1'\n$HOME tetap literal\n" {
		t.Errorf("hasil script berbeda: %q", got)
	}
}
