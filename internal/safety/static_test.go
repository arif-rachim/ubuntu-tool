package safety

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// executorDirs adalah satu-satunya tempat yang boleh menjalankan proses atau mengubah file: lapisan
// eksekusi yang selalu dipanggil setelah layar konfirmasi, dan penyimpan riwayat.
var executorDirs = []string{"internal/run", "internal/ui/runflow"}

// sideEffects adalah fungsi paket standar yang menjalankan program, mengirim sinyal, atau mengubah file.
var sideEffects = map[string][]string{
	"os/exec": {"Command", "CommandContext"},
	"os": {"Remove", "RemoveAll", "WriteFile", "Rename", "Chmod", "Chown", "Lchown", "Chtimes", "Create", "CreateTemp",
		"OpenFile", "Symlink", "Link", "Truncate", "Mkdir", "MkdirAll", "MkdirTemp", "StartProcess", "Setenv", "Unsetenv", "Clearenv", "Exit"},
	"syscall":                            {"Kill", "Exec", "ForkExec", "StartProcess", "Unlink", "Rmdir", "Mount", "Unmount", "Reboot", "Setuid", "Setgid", "Chmod", "Chown", "Rename", "Mkdir", "Symlink", "Link", "Truncate", "Open"},
	"github.com/charm.land/bubbletea/v2": {"ExecProcess"},
	"charm.land/bubbletea/v2":            {"ExecProcess"},
	"net/http":                           {"Post", "PostForm", "Put"},
	"io/ioutil":                          {"WriteFile", "TempFile", "TempDir"},
}

// allowedOutside adalah pengecualian yang sudah ditinjau, dengan alasannya.
var allowedOutside = map[string]string{
	"cmd/ubt/main.go:os.Exit": "keluar dari program dengan exit code",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod tidak ditemukan")
		}
		dir = parent
	}
}

// Seluruh perubahan sistem harus lewat lapisan eksekusi supaya selalu didahului layar konfirmasi dan
// tercatat di riwayat. Modul, layar, Diagnosa, dan CLI hanya boleh membaca.
func TestHanyaLapisanEksekusiYangMengubahSistem(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "dist" || d.Name() == "bin" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		for _, dir := range executorDirs {
			if strings.HasPrefix(rel, dir+"/") {
				return nil
			}
		}
		scanned++
		checkFile(t, root, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 50 {
		t.Fatalf("hanya %d file dipindai; pemindaian kemungkinan salah", scanned)
	}
}

type reporter interface {
	Errorf(format string, args ...any)
}

type msgRecorder struct{ msgs []string }

func (r *msgRecorder) Errorf(format string, args ...any) {
	r.msgs = append(r.msgs, fmt.Sprintf(format, args...))
}

func checkFile(t reporter, root, path string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Errorf("parse %s: %v", path, err)
		return
	}
	rel, _ := filepath.Rel(root, path)
	// nama lokal import → path import
	imports := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := filepath.Base(p)
		if strings.HasSuffix(p, "/v2") {
			name = filepath.Base(strings.TrimSuffix(p, "/v2"))
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath, ok := imports[pkg.Name]
		if !ok {
			return true
		}
		for _, fn := range sideEffects[importPath] {
			if sel.Sel.Name != fn {
				continue
			}
			key := rel + ":" + pkg.Name + "." + fn
			if _, ok := allowedOutside[key]; ok {
				continue
			}
			t.Errorf("%s: %s.%s dipakai di luar lapisan eksekusi (%v); perubahan sistem harus lewat run.Plan + layar konfirmasi",
				fset.Position(sel.Pos()), pkg.Name, fn, executorDirs)
		}
		return true
	})
}

// Pastikan pemindai benar-benar mendeteksi pelanggaran (bukan lulus karena salah baca import).
func TestPemindaiMendeteksiPelanggaran(t *testing.T) {
	dir := t.TempDir()
	src := `package x
import (
	"os"
	ex "os/exec"
	tea "charm.land/bubbletea/v2"
)
func f() { os.RemoveAll("/"); ex.Command("rm"); _ = tea.ExecProcess; _ = os.ReadFile }
`
	path := filepath.Join(dir, "x.go")
	os.WriteFile(path, []byte(src), 0o644)
	rec := &msgRecorder{}
	checkFile(rec, dir, path)
	found := rec.msgs
	want := []string{"os.RemoveAll", "ex.Command", "tea.ExecProcess"}
	if len(found) != len(want) {
		t.Fatalf("ditemukan %d pelanggaran, ingin %d: %v", len(found), len(want), found)
	}
	for i, w := range want {
		if !strings.Contains(found[i], w) {
			t.Errorf("pelanggaran %d = %q, ingin memuat %q", i, found[i], w)
		}
	}
}
