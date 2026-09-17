package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestDoctor(t *testing.T) {
	defer func(f func(string) (string, error)) { LookPath = f }(LookPath)
	LookPath = func(name string) (string, error) {
		if name == "systemctl" || name == "nginx" {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/" + name, nil
	}
	var out, errb bytes.Buffer
	code := Doctor(nil, &out, &errb)
	if code != 1 {
		t.Errorf("systemctl wajib hilang → exit 1, dapat %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "✗  systemctl") || !strings.Contains(s, "sudo apt install systemd") {
		t.Errorf("%s", s)
	}
	out.Reset()
	Doctor([]string{"--json"}, &out, &errb)
	var tools []Tool
	if err := json.Unmarshal(out.Bytes(), &tools); err != nil || len(tools) != len(Tools()) {
		t.Errorf("json: %v", err)
	}
}

func TestExportScript(t *testing.T) {
	at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.Local)
	entries := []run.Entry{
		{Time: at, Title: "Install nginx", Argv: []string{"apt-get", "install", "-y", "nginx"}, Sudo: true},
		{Time: at, Title: "Tulis site", Argv: []string{"install", "-m", "0644", "/dev/stdin", "/etc/nginx/sites-available/a"}, Sudo: true, Stdin: "server {}\n"},
		{Time: at, Title: "Validasi", Argv: []string{"nginx", "-t"}, Sudo: true, Check: true, ExitCode: 1},
		{Time: at, Title: "Set password", Argv: []string{"chpasswd"}, Sudo: true, Hidden: true},
	}
	s := ExportScript(entries, false, at)
	for _, want := range []string{"#!/usr/bin/env bash", "set -euo pipefail", "# Install nginx — 2026-09-16 10:00\nsudo apt-get install -y nginx",
		"sudo install -m 0644 /dev/stdin /etc/nginx/sites-available/a <<'UBT_EOF'\nserver {}\nUBT_EOF", "# isi stdin disembunyikan", "1 command yang gagal tidak diikutkan"} {
		if !strings.Contains(s, want) {
			t.Errorf("tidak memuat %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "nginx -t") {
		t.Error("command gagal tidak boleh ikut tanpa --include-failed")
	}
	if s := ExportScript(entries, true, at); !strings.Contains(s, "# GAGAL saat dijalankan (gagal (exit 1)); dikomentari:\n# sudo nginx -t") {
		t.Errorf("include-failed:\n%s", s)
	}
}

func TestHistoryCLI(t *testing.T) {
	h := &run.History{Path: filepath.Join(t.TempDir(), "history.log")}
	defer func(f func() (*run.History, error)) { HistoryFile = f }(HistoryFile)
	HistoryFile = func() (*run.History, error) { return h, nil }
	var out, errb bytes.Buffer
	History(nil, &out, &errb)
	if !strings.Contains(out.String(), "masih kosong") {
		t.Error(out.String())
	}
	for i := 0; i < 3; i++ {
		h.Append(run.Entry{Time: time.Now(), Title: "t", Argv: []string{"echo", strings.Repeat("x", i+1)}})
	}
	out.Reset()
	History([]string{"export", "--last", "2"}, &out, &errb)
	if strings.Contains(out.String(), "echo x\n") || !strings.Contains(out.String(), "echo xxx") {
		t.Errorf("%s", out.String())
	}
	out.Reset()
	if code := History([]string{"--json"}, &out, &errb); code != 0 || !strings.HasPrefix(out.String(), "[") {
		t.Errorf("%d %s", code, out.String())
	}
	HistoryFile = func() (*run.History, error) { return nil, errors.New("x") }
	if History(nil, &out, &errb) != 1 {
		t.Error("error harus exit 1")
	}
}

func TestPortsJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Ports([]string{"--json"}, &out, &errb); code != 0 {
		t.Skipf("proc tidak tersedia: %s", errb.String())
	}
	var ports []PortJSON
	if err := json.Unmarshal(out.Bytes(), &ports); err != nil {
		t.Errorf("%v: %s", err, out.String())
	}
}
