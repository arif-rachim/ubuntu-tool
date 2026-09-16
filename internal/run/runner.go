package run

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runner menjalankan command dan menangkap outputnya. Dipakai lapisan sys/* untuk membaca data,
// dan bisa diganti Fake saat test.
type Runner interface {
	Capture(ctx context.Context, c Command) (stdout, stderr string, err error)
}

// Real menjalankan command sungguhan.
type Real struct {
	IsRoot bool
}

// NewReal membuat Runner sungguhan untuk user saat ini.
func NewReal() Real { return Real{IsRoot: IsRoot()} }

// Capture menjalankan command dengan locale C (output stabil untuk di-parse).
// Command yang butuh root dijalankan dengan `sudo -n`: gagal bila kredensial sudo belum tersimpan.
func (r Real) Capture(ctx context.Context, c Command) (string, string, error) {
	argv := c.ExecArgv(r.IsRoot, true)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), describeErr(argv, err)
}

// ExitCode mengembalikan kode keluar dari error hasil menjalankan command:
// 0 bila err nil, -1 bila command tidak sempat berjalan.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// NotFoundError menandakan program tidak ditemukan di PATH.
type NotFoundError struct{ Program string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("program %q tidak ditemukan (belum terinstall atau tidak ada di PATH)", e.Program)
}

// describeErr mengganti error teknis yang umum dengan pesan yang bisa dipahami.
func describeErr(argv []string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return &NotFoundError{Program: argv[0]}
	}
	return err
}

// Fake adalah Runner untuk test: mengembalikan respons berdasarkan Preview command (tanpa sudo).
type Fake struct {
	Responses map[string]FakeResponse
	Calls     []Command
}

// FakeResponse adalah respons Fake untuk satu command.
type FakeResponse struct {
	Stdout, Stderr string
	Err            error
}

// Capture mengembalikan respons yang terdaftar, atau error bila command tidak dikenal.
func (f *Fake) Capture(_ context.Context, c Command) (string, string, error) {
	f.Calls = append(f.Calls, c)
	key := JoinShell(c.Argv)
	resp, ok := f.Responses[key]
	if !ok {
		return "", "", fmt.Errorf("fake: tidak ada respons untuk %q", key)
	}
	return resp.Stdout, resp.Stderr, resp.Err
}
