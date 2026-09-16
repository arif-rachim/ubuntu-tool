package disk

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

func TestParseMountinfo(t *testing.T) {
	in := `28 33 0:25 / /sys rw,nosuid shared:7 - sysfs sysfs rw
33 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw
40 33 8:1 / /boot/efi rw shared:30 - vfat /dev/sda1 rw
41 33 8:2 /home/data /mnt/My\040Data rw - ext4 /dev/sda2 rw
50 33 0:40 / /snap/core24/2124 ro - squashfs /dev/loop2 ro
`
	ms := ParseMountinfo(in)
	if len(ms) != 5 || ms[3].Target != "/mnt/My Data" || ms[1].Source != "/dev/sda2" || ms[1].FSType != "ext4" {
		t.Fatalf("%+v", ms)
	}
	if ms[0].Real() || !ms[1].Real() || ms[4].Real() {
		t.Error("Real() salah untuk sysfs/ext4/squashfs")
	}
	if m, ok := MountFor(ms, "/boot/efi/EFI/ubuntu"); !ok || m.Target != "/boot/efi" {
		t.Errorf("MountFor: %+v", m)
	}
	if m, _ := MountFor(ms, "/bootloader"); m.Target != "/" {
		t.Errorf("/bootloader bukan di /boot: %+v", m.Target)
	}
}

func TestUsePercentSepertiDf(t *testing.T) {
	m := Mount{Used: 26511896576, Avail: 210516918272}
	if got := m.UsePercent(); got != 12 {
		t.Errorf("UsePercent %d, ingin 12 (dibulatkan ke atas seperti df)", got)
	}
	if (Mount{Inodes: 100, IFree: 5}).InodePercent() != 95 {
		t.Error("InodePercent salah")
	}
}

func TestReadMountsNyata(t *testing.T) {
	if _, err := os.Stat("/proc/self/mountinfo"); err != nil {
		t.Skip("tidak ada /proc")
	}
	ms, err := ReadMounts("/proc", false)
	if err != nil || len(ms) == 0 {
		t.Fatalf("%v %d", err, len(ms))
	}
	root, ok := MountFor(ms, "/")
	if !ok || root.Size == 0 || root.Inodes == 0 {
		t.Errorf("mount / tidak terbaca: %+v", root)
	}
}

func TestParseLsblk(t *testing.T) {
	data := []byte(`{"blockdevices":[
	 {"name":"sda","size":256060514304,"type":"disk","fstype":null,"mountpoints":[null],"model":"SSD 256GB","rota":false,
	  "children":[
	   {"name":"sda1","size":1127219200,"type":"part","fstype":"vfat","mountpoints":["/boot/efi"],"model":null,"rota":false},
	   {"name":"sda2","size":254931533824,"type":"part","fstype":"ext4","mountpoints":["/", "/mnt/My Data"],"model":null,"rota":false}
	  ]},
	 {"name":"sdb","size":1000204886016,"type":"disk","fstype":"ext4","mountpoints":[null],"model":"HDD","rota":"1"}
	]}`)
	devs, err := ParseLsblk(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 || len(devs[0].Children) != 2 || devs[0].Rotational || !devs[1].Rotational {
		t.Fatalf("%+v", devs)
	}
	if !reflect.DeepEqual(devs[0].Children[1].Mountpoints, []string{"/", "/mnt/My Data"}) {
		t.Errorf("mountpoints: %v", devs[0].Children[1].Mountpoints)
	}
	if !devs[1].Unmounted() || devs[0].Children[0].Unmounted() {
		t.Error("Unmounted salah")
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string, size int) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("logs/app.log", 3<<20)
	mk("logs/old/app.log.1", 1<<20)
	mk("cache/a.bin", 512<<10)
	mk("small.txt", 10)
	// Seperti du, hardlink dihitung di folder yang ditemukan pertama (urutan nama): "logs" sebelum "zlinks".
	os.MkdirAll(filepath.Join(root, "zlinks"), 0o755)
	os.Link(filepath.Join(root, "logs/app.log"), filepath.Join(root, "zlinks/hardlink.log"))
	locked := filepath.Join(root, "secret")
	os.MkdirAll(locked, 0o755)
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	var prog Progress
	res, err := Scan(context.Background(), root, ScanOptions{MaxBigFiles: 2, MinBigFile: 100 << 10}, &prog)
	if err != nil {
		t.Fatal(err)
	}
	r := res.Root
	if len(r.Children) != 4 || r.Children[0].Name != "logs" {
		t.Fatalf("anak: %+v", r.Children)
	}
	logs := r.Children[0]
	if logs.Size < 4<<20 || logs.Files != 2 || len(logs.Children) != 1 || logs.Children[0].Parent != logs {
		t.Errorf("logs: size %d files %d", logs.Size, logs.Files)
	}
	// Hardlink tidak dihitung dua kali: total kira-kira 4.5 MiB, bukan 7.5 MiB.
	if r.Size > 6<<20 {
		t.Errorf("hardlink terhitung dua kali: %d", r.Size)
	}
	if len(res.BigFiles) != 2 || !strings.HasSuffix(res.BigFiles[0].Path, "app.log") || res.BigFiles[0].Size < res.BigFiles[1].Size {
		t.Errorf("file besar: %+v", res.BigFiles)
	}
	if os.Geteuid() != 0 && (res.Denied != 1 || r.Denied != 1) {
		t.Errorf("folder terkunci harus tercatat: %d/%d", res.Denied, r.Denied)
	}
	if prog.Files.Load() < 4 {
		t.Errorf("progress: %d file", prog.Files.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Scan(ctx, root, ScanOptions{}, nil); err == nil {
		t.Error("scan yang dibatalkan harus error")
	}
}

func TestFindDeletedOpen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "besar.log")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.Write(make([]byte, 2<<20))
	os.Remove(p)

	res, err := FindDeletedOpen("/proc")
	if err != nil {
		t.Skipf("tidak ada /proc: %v", err)
	}
	for _, df := range res.Files {
		if df.Path == p && df.PID == os.Getpid() && df.Size == 2<<20 {
			if res.Total() < df.Size {
				t.Error("Total salah")
			}
			return
		}
	}
	t.Errorf("file terhapus yang masih dibuka tidak ditemukan: %+v", res.Files)
}

func TestParseCleanupOutputs(t *testing.T) {
	if got := ParseJournalUsage("Archived and active journals take up 1.5G in the file system."); got != 3<<29 {
		t.Errorf("journal %d", got)
	}
	for in, want := range map[string]int64{"30.5M": 31981568, "8.507GB": 8507000000, "850MB": 850000000, "0B": 0, "12kB": 12000, "2KiB": 2048} {
		if got := ParseHumanSize(in); got != want {
			t.Errorf("ParseHumanSize(%q) = %d, ingin %d", in, got, want)
		}
	}
	snaps := t.TempDir()
	os.WriteFile(filepath.Join(snaps, "core22_1564.snap"), make([]byte, 1000), 0o644)
	revs := ParseSnapDisabled(`Name     Version   Rev    Tracking       Publisher   Notes
core22   20240111  1564   latest/stable  canonical✓  base,disabled
core22   20240408  1380   latest/stable  canonical✓  base
lxd      5.21.1    28460  5.21/stable/…  canonical✓  disabled
`, snaps)
	if len(revs) != 2 || revs[0].Revision != "1564" || revs[0].Size != 1000 || revs[1].Name != "lxd" {
		t.Errorf("snap: %+v", revs)
	}
	pkgs := ParseAutoremove("Reading...\nRemv linux-headers-6.8.0-40 [6.8.0-40.40]\nRemv libfoo1 [1.0]\n")
	if !reflect.DeepEqual(pkgs, []string{"linux-headers-6.8.0-40", "libfoo1"}) {
		t.Errorf("autoremove: %v", pkgs)
	}
	docker := `TYPE            TOTAL     ACTIVE    SIZE      RECLAIMABLE
Images          12        3         8.507GB   6.1GB (71%)
Containers      12        12        28.96MB   0B (0%)
Local Volumes   3         2         84.77MB   10MB (11%)
Build Cache     4         0         1GB       1GB
`
	if got := ParseDockerReclaimable(docker); got != 6100000000+10000000+1000000000 {
		t.Errorf("docker reclaimable %d", got)
	}
	for name, want := range map[string]bool{"syslog.1": true, "auth.log.2.gz": true, "syslog": false, "kern.log": false, "dmesg.0": true} {
		if RotatedLog(name) != want {
			t.Errorf("RotatedLog(%q) != %v", name, want)
		}
	}
}

func TestFindCandidates(t *testing.T) {
	apt, logs := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(apt, "nginx_1.24.deb"), make([]byte, 5000), 0o644)
	os.WriteFile(filepath.Join(logs, "syslog.1"), make([]byte, 700), 0o644)
	os.WriteFile(filepath.Join(logs, "syslog"), make([]byte, 999), 0o644)
	fake := &run.Fake{Responses: map[string]run.FakeResponse{
		"apt-get --dry-run autoremove": {Stdout: "Remv libold1 [1]\n"},
		"journalctl --disk-usage":      {Stdout: "Archived and active journals take up 1.0G in the file system."},
		"snap list --all":              {Err: os.ErrNotExist},
		"docker system df":             {Err: os.ErrPermission},
	}}
	cs := FindCandidates(context.Background(), fake, CleanupSources{AptArchives: apt, SnapsDir: t.TempDir(), LogDir: logs})
	byID := map[string]Candidate{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	if c := byID["apt-clean"]; !c.Available || c.Size != 5000 || c.Steps[0].Preview(false) != "sudo apt-get clean" {
		t.Errorf("apt: %+v", c)
	}
	if c := byID["autoremove"]; !c.Available || c.SizeLabel != "1 paket" {
		t.Errorf("autoremove: %+v", c)
	}
	if c := byID["journal"]; !c.Available || c.Size != 1<<30-JournalKeep {
		t.Errorf("journal: %+v", c)
	}
	if c := byID["snap"]; c.Available || c.Why != "snap tidak terinstall" {
		t.Errorf("snap: %+v", c)
	}
	if c := byID["docker"]; c.Available {
		t.Errorf("docker tanpa akses tidak boleh tersedia: %+v", c)
	}
	if c := byID["rotated-logs"]; !c.Available || c.Size != 700 {
		t.Errorf("log rotasi hanya syslog.1: %+v", c)
	}
}

func TestSwap(t *testing.T) {
	swaps := ParseSwapon("/swap.img file 4294963200 1048576\n/dev/sda3 partition 2147479552 0\n")
	if len(swaps) != 2 || swaps[0].Used != 1048576 || swaps[1].Type != "partition" {
		t.Fatalf("%+v", swaps)
	}
	for ram, want := range map[int64]int{1 << 30: 2, 4 << 30: 4, 16 << 30: 4} {
		if got := RecommendedSwap(ram); got != want {
			t.Errorf("RecommendedSwap(%d GiB) = %d, ingin %d", ram>>30, got, want)
		}
	}
	p := SwapfilePlan("/swapfile", 2, time.Date(2026, 9, 16, 21, 0, 0, 0, time.UTC))
	seq := p.Sequence()
	var cmds []string
	for _, s := range seq {
		cmds = append(cmds, s.Command.Preview(false))
	}
	want := []string{
		"sudo fallocate -l 2G /swapfile", "sudo chmod 600 /swapfile", "sudo mkswap /swapfile", "sudo swapon /swapfile",
		"sudo cp -a /etc/fstab /etc/fstab.ubt-bak-20260916-210000", "sudo tee -a /etc/fstab", "findmnt --verify", "swapon --show",
	}
	if !reflect.DeepEqual(cmds, want) {
		t.Errorf("plan swapfile:\n%s", strings.Join(cmds, "\n"))
	}
	if seq[5].Command.Stdin != "/swapfile none swap sw 0 0\n" {
		t.Errorf("baris fstab: %q", seq[5].Command.Stdin)
	}
}
