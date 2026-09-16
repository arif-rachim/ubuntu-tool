package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "ubt "},
		{name: "help", args: []string{"help"}, wantCode: 0, wantStdout: "Pemakaian:"},
		{name: "perintah tak dikenal", args: []string{"hapus-semua"}, wantCode: 2, wantStderr: `perintah tidak dikenal "hapus-semua"`},
		{name: "argumen version tak dikenal", args: []string{"version", "--xml"}, wantCode: 2, wantStderr: "argumen tidak dikenal"},
		{name: "tanpa argumen", args: nil, wantCode: 1, wantStderr: "belum tersedia"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut)
			if code != tt.wantCode {
				t.Errorf("exit code %d, ingin %d (stderr: %s)", code, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantStdout) {
				t.Errorf("stdout %q tidak mengandung %q", out.String(), tt.wantStdout)
			}
			if !strings.Contains(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr %q tidak mengandung %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d: %s", code, errOut.String())
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output bukan JSON valid: %v\n%s", err, out.String())
	}
	for _, key := range []string{"version", "commit", "date", "go", "platform"} {
		if _, ok := got[key]; !ok {
			t.Errorf("field %q tidak ada di JSON", key)
		}
	}
}
