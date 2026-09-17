package run

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Event adalah satu kejadian dari Stream: satu baris output, atau selesai.
type Event struct {
	Line     string
	Done     bool
	ExitCode int
	Err      error
}

// Stream adalah command yang berjalan dengan output dibaca baris demi baris.
type Stream interface {
	// Next menunggu kejadian berikutnya. Setelah Done, terus mengembalikan Done yang sama.
	Next() Event
	// Cancel menghentikan command (SIGTERM ke grup proses, lalu SIGKILL bila tidak berhenti).
	Cancel()
}

// Starter memulai Stream; diganti saat test.
type Starter func(ctx context.Context, argv []string, stdin string) (Stream, error)

type realStream struct {
	events chan Event
	cancel context.CancelFunc
	last   Event
}

// StartStream menjalankan argv dengan stdout dan stderr digabung sesuai urutan kemunculan.
// Stdin kosong berarti /dev/null, supaya command tidak menggantung menunggu input.
func StartStream(ctx context.Context, argv []string, stdin string) (Stream, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Grup proses sendiri, supaya pembatalan ikut menghentikan anak prosesnya.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		cancel()
		pr.Close()
		pw.Close()
		return nil, describeErr(argv, err)
	}
	pw.Close() // salinan milik anak proses tetap terbuka sampai ia selesai

	s := &realStream{events: make(chan Event, 256), cancel: cancel}
	go func() {
		readLines(pr, func(line string) { s.events <- Event{Line: line} })
		pr.Close()
		err := cmd.Wait()
		if ctx.Err() != nil && err != nil {
			err = context.Canceled
		}
		s.events <- Event{Done: true, ExitCode: ExitCode(err), Err: describeErr(argv, err)}
		close(s.events)
	}()
	return s, nil
}

func (s *realStream) Next() Event {
	if s.last.Done {
		return s.last
	}
	ev, ok := <-s.events
	if !ok {
		return s.last
	}
	if ev.Done {
		s.last = ev
	}
	return ev
}

func (s *realStream) Cancel() { s.cancel() }

// readLines memanggil fn untuk setiap baris. Carriage return (bar progres apt, curl) diperlakukan
// sebagai "timpa baris": hanya bagian setelah \r terakhir yang dipakai.
func readLines(r io.Reader, fn func(string)) {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if i := strings.LastIndexByte(line, '\r'); i >= 0 {
				line = line[i+1:]
			}
			fn(line)
		}
		if err != nil {
			return
		}
	}
}

// FakeStream adalah Stream untuk test yang mengeluarkan baris lalu selesai dengan exit code tertentu.
type FakeStream struct {
	Lines    []string
	ExitCode int
	Err      error
	i        int
	Canceled bool
}

func (f *FakeStream) Next() Event {
	if f.Canceled {
		return Event{Done: true, ExitCode: -1, Err: context.Canceled}
	}
	if f.i < len(f.Lines) {
		f.i++
		return Event{Line: f.Lines[f.i-1]}
	}
	return Event{Done: true, ExitCode: f.ExitCode, Err: f.Err}
}

func (f *FakeStream) Cancel() { f.Canceled = true }
