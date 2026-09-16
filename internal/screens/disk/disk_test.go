package disk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	sysdisk "github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/testutil"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

func env() shared.Env { return shared.Env{Now: time.Now, UID: 1000, ProcRoot: "/proc"} }

func TestDashboardPeringatan(t *testing.T) {
	data := Data{
		Mounts: []sysdisk.Mount{
			{Target: "/", Source: "/dev/sda2", FSType: "ext4", Size: 100 << 30, Used: 93 << 30, Avail: 7 << 30, Inodes: 1000, IFree: 900},
			{Target: "/var/mail", Source: "/dev/sdb1", FSType: "xfs", Size: 10 << 30, Used: 1 << 30, Avail: 9 << 30, Inodes: 1000, IFree: 50},
		},
		Deleted: sysdisk.DeletedOpen{Files: []sysdisk.DeletedFile{{PID: 1, Path: "/var/log/app.log", Size: 3 << 30}}},
		Devices: []sysdisk.BlockDevice{{Name: "sdc", Type: "disk", Size: 500 << 30, Rotational: true}},
	}
	m := newModel(env(), func(shared.Env) (Data, error) { return data, nil }, sysdisk.CleanupSources{})
	m.Update(testutil.Run(m.Init())[0])
	view := ansi.Strip(m.View(120, 50))
	for _, s := range []string{"/ terpakai 93%", "Inode /var/mail terpakai 95%", "tidak ada swap aktif", "sdc  500,0 GiB  HDD", "belum dipartisi", "1 file (3,0 GiB) sudah dihapus"} {
		if !strings.Contains(view, s) {
			t.Errorf("tidak memuat %q:\n%s", s, view)
		}
	}
	_, cmd := m.Update(testutil.Key("a"))
	if msgs := testutil.Run(cmd); len(msgs) != 1 {
		t.Fatal("a harus membuka penjelajah folder")
	} else if ex := msgs[0].(nav.PushMsg).Screen.(*ExplorerModel); ex.root != "/" {
		t.Errorf("penjelajah harus mulai dari mount terpilih: %s", ex.root)
	}
}

func TestExplorerScanDanTelusuri(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "var/log"), 0o755)
	os.WriteFile(filepath.Join(root, "var/log/besar.log"), make([]byte, 60<<20), 0o644)
	os.MkdirAll(filepath.Join(root, "home"), 0o755)
	os.WriteFile(filepath.Join(root, "home/kecil.txt"), []byte("x"), 0o644)

	m := newExplorer(env(), root)
	var msgs []any
	for _, msg := range testutil.Run(m.Init()) {
		msgs = append(msgs, msg)
		m.Update(msg)
	}
	if m.result == nil {
		t.Fatalf("scan tidak selesai: %v", msgs)
	}
	view := ansi.Strip(m.View(120, 30))
	if !strings.Contains(view, "var/") || !strings.Contains(view, "sudo du -xh -d1") {
		t.Errorf("tampilan:\n%s", view)
	}
	m.Update(testutil.Key("enter")) // masuk var/
	if m.cur.Name != "var" {
		t.Fatalf("enter harus masuk folder terbesar, sekarang %s", m.cur.Name)
	}
	m.Update(testutil.Key("f"))
	if !strings.Contains(ansi.Strip(m.View(120, 30)), "besar.log") {
		t.Error("f harus menampilkan file besar")
	}
	m.Update(testutil.Key("esc")) // tutup daftar file
	m.Update(testutil.Key("esc")) // naik ke root
	if m.cur.Path != root {
		t.Errorf("esc harus naik ke induk, sekarang %s", m.cur.Path)
	}
	_, cmd := m.Update(testutil.Key("esc"))
	if msgs := testutil.Run(cmd); len(msgs) != 1 {
		t.Error("esc di folder awal harus keluar")
	}
}

func TestCleanupFormDanPlan(t *testing.T) {
	cands := []sysdisk.Candidate{
		{ID: "apt-clean", Label: "Cache apt", Size: 2 << 30, Available: true, Steps: sysdisk.SwapfilePlan("/x", 1, time.Now()).Steps[:1]},
		{ID: "snap", Label: "Snap", Why: "snap tidak terinstall"},
		{ID: "journal", Label: "Journal", Size: 1 << 30, Available: true, Steps: sysdisk.SwapfilePlan("/y", 1, time.Now()).Steps[1:2]},
	}
	f := cleanupForm(cands)
	q := f.Questions[0]
	if q.Options[1].Disabled != "snap tidak terinstall" || q.Options[0].Meta != "~2,0 GiB" {
		t.Errorf("opsi: %+v", q.Options)
	}
	if got := q.Summary([]ask.Option{q.Options[0], q.Options[2]}); got != "Perkiraan ruang yang kembali: 3,0 GiB" {
		t.Errorf("summary: %s", got)
	}
	p := cleanupPlan(cands, []string{"journal", "snap", "apt-clean"})
	if len(p.Steps) != 2 {
		t.Errorf("hanya kandidat tersedia yang masuk plan: %d langkah", len(p.Steps))
	}
}

func TestSwapForm(t *testing.T) {
	d := Data{
		RAMBytes: 4 << 30,
		Mounts:   []sysdisk.Mount{{Target: "/", Avail: 3 << 30}},
		Swaps:    []sysdisk.Swap{{Name: "/swap.img", Size: 4 << 30}},
	}
	f := swapForm(d)
	opts := f.Questions[0].Options
	for _, o := range opts {
		switch o.Value {
		case "4":
			if !o.Recommended || o.Disabled == "" {
				t.Errorf("4 GiB disarankan untuk RAM 4 GiB tetapi disabled karena disk sisa 3 GiB: %+v", o)
			}
		case "1":
			if o.Disabled != "" {
				t.Errorf("1 GiB muat: %+v", o)
			}
		}
	}
	if !strings.Contains(f.Questions[0].Help, "/swap.img") {
		t.Error("swap yang sudah ada harus disebut")
	}
	if validateSwapPath("relatif") == nil || validateSwapPath("/tmp") == nil || validateSwapPath("/tidak-ada/swapfile") == nil {
		t.Error("validasi path harus menolak path relatif, file yang ada, folder yang tidak ada")
	}
	if err := validateSwapPath(filepath.Join(t.TempDir(), "swapfile")); err != nil {
		t.Errorf("path valid ditolak: %v", err)
	}
}
