package resource

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
)

const meminfo = `MemTotal:       16249368 kB
MemFree:          816360 kB
MemAvailable:   11693644 kB
Buffers:          310012 kB
Cached:         11335556 kB
SwapCached:            0 kB
SwapTotal:       4194300 kB
SwapFree:        4194300 kB
Shmem:           1170128 kB
SReclaimable:     742472 kB
`

func TestParseMeminfo(t *testing.T) {
	m := ParseMeminfo(meminfo)
	if m.TotalKB != 16249368 || m.AvailableKB != 11693644 || m.SwapUsedKB() != 0 {
		t.Fatalf("%+v", m)
	}
	if m.UsedKB() != 16249368-11693644 {
		t.Errorf("UsedKB harus Total-Available")
	}
	if m.CacheKB() != 310012+11335556+742472-1170128 {
		t.Errorf("CacheKB %d", m.CacheKB())
	}
}

func TestParseLoadavgDanStat(t *testing.T) {
	l, err := ParseLoadavg("0.45 0.54 0.57 2/1176 40204\n")
	if err != nil || l.One != 0.45 || l.Fifteen != 0.57 || l.Running != 2 || l.Total != 1176 {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := ParseLoadavg("rusak"); err == nil {
		t.Error("format rusak harus error")
	}

	stat1 := "cpu  100 0 50 800 50 0 0 0 10 0\ncpu0 50 0 25 400 25 0 0 0 5 0\ncpu1 50 0 25 400 25 0 0 0 5 0\nintr 1\n"
	stat2 := "cpu  250 0 100 850 50 0 0 0 20 0\ncpu0 1 0 0 0 0 0 0 0 0 0\ncpu1 1 0 0 0 0 0 0 0 0 0\n"
	a, cores := ParseStat(stat1)
	b, _ := ParseStat(stat2)
	if cores != 2 || a.Total != 1000 || a.Idle != 850 {
		t.Fatalf("stat: %+v cores %d", a, cores)
	}
	// Selisih total 250, selisih idle 50 → sibuk 80%.
	if got := Busy(a, b); got < 79.9 || got > 80.1 {
		t.Errorf("Busy %.1f, ingin 80", got)
	}
	if Busy(b, b) != 0 {
		t.Error("tanpa selisih harus 0")
	}
}

func TestParsePressure(t *testing.T) {
	p := ParsePressure("some avg10=1.50 avg60=12.25 avg300=3.00 total=123\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n")
	if !p.Available || p.Avg60 != 12.25 {
		t.Fatalf("%+v", p)
	}
	if ParsePressure("").Available {
		t.Error("kosong tidak boleh Available")
	}
}

func TestAnalyze(t *testing.T) {
	has := func(ins []Insight, lvl risk.Level, sub string) bool {
		for _, i := range ins {
			if i.Level == lvl && strings.Contains(i.Title, sub) {
				return true
			}
		}
		return false
	}
	healthy := Snapshot{Cores: 4, Memory: ParseMeminfo(meminfo), Load: Load{One: 0.5}}
	ins := Analyze(healthy)
	if !has(ins, risk.Safe, "kondisi baik") || !has(ins, risk.Safe, "cache itu normal") {
		t.Errorf("sehat: %+v", ins)
	}

	busy := healthy
	busy.Load.One = 9
	if !has(Analyze(busy), risk.Dangerous, "load 9.00 di 4 core") {
		t.Error("load 9 di 4 core harus berbahaya")
	}

	low := healthy
	low.Memory.AvailableKB = low.Memory.TotalKB / 20
	low.PSI.Memory = Pressure{Available: true, Avg60: 25}
	got := Analyze(low)
	if !has(got, risk.Dangerous, "RAM hampir habis") || !has(got, risk.Dangerous, "Kekurangan RAM nyata") {
		t.Errorf("RAM rendah: %+v", got)
	}

	small := Snapshot{Cores: 1, Memory: Memory{TotalKB: 1024 * 1024, AvailableKB: 800 * 1024}}
	if !has(Analyze(small), risk.Caution, "Tidak ada swap") {
		t.Error("VPS kecil tanpa swap harus diingatkan")
	}

	io := healthy
	io.PSI.IO = Pressure{Available: true, Avg60: 40}
	if !has(Analyze(io), risk.Caution, "Disk jadi hambatan") {
		t.Error("PSI IO tinggi harus terdeteksi")
	}
}

func TestSamplerProcCPU(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("meminfo", meminfo)
	write("loadavg", "1.00 0.50 0.25 1/100 999\n")
	write("uptime", "3600.50 100.00\n")
	write("pressure/io", "some avg10=0 avg60=5.5 avg300=0 total=1\n")
	write("stat", "cpu  100 0 0 900 0 0 0 0 0 0\ncpu0 100 0 0 900 0 0 0 0 0 0\n")
	write("10/stat", "10 (busy app) R 1 10 10 0 -1 0 0 0 0 0 100 0 0 0 20 0 1 0 5 0 0")
	write("10/status", "Name:\tbusy app\nUid:\t1000\t1000\t1000\t1000\nVmRSS:\t 2048 kB\n")
	write("10/cmdline", "busy\x00--loop\x00")
	write("11/stat", "11 (kworker/0:1) I 2 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 5 0 0")
	write("11/status", "Name:\tkworker/0:1\nUid:\t0\t0\t0\t0\n")

	s := &Sampler{ProcRoot: root}
	t0 := time.Unix(1000, 0)
	first, err := s.Sample(t0)
	if err != nil {
		t.Fatal(err)
	}
	if first.HasDelta || len(first.Procs) != 1 || first.Uptime != time.Duration(3600.5*float64(time.Second)) || first.PSI.IO.Avg60 != 5.5 {
		t.Fatalf("sampel pertama: delta=%v procs=%d uptime=%v psi=%+v", first.HasDelta, len(first.Procs), first.Uptime, first.PSI.IO)
	}

	// 2 detik kemudian proses memakai 100 tick lagi (1 detik CPU) → 50%.
	write("10/stat", "10 (busy app) R 1 10 10 0 -1 0 0 0 0 0 150 50 0 0 20 0 1 0 5 0 0")
	write("stat", "cpu  200 0 0 1000 0 0 0 0 0 0\ncpu0 200 0 0 1000 0 0 0 0 0 0\n")
	second, _ := s.Sample(t0.Add(2 * time.Second))
	if !second.HasDelta || second.Procs[0].CPU != 50 || second.CPUBusy != 50 {
		t.Fatalf("sampel kedua: %+v busy %.1f", second.Procs, second.CPUBusy)
	}
	if second.Procs[0].Command != "busy --loop" || second.Procs[0].Name != "busy app" {
		t.Errorf("proses: %+v", second.Procs[0])
	}
}

func TestSortByDanBar(t *testing.T) {
	ps := []Proc{{PID: 1, CPU: 5, RSSKB: 900}, {PID: 2, CPU: 50, RSSKB: 10}, {PID: 3, CPU: 5, RSSKB: 1000}}
	if got := SortBy(ps, false, 2); got[0].PID != 2 || got[1].PID != 3 || len(got) != 2 {
		t.Errorf("urut CPU: %+v", got)
	}
	if got := SortBy(ps, true, 0); got[0].PID != 3 {
		t.Errorf("urut RAM: %+v", got)
	}
	if Bar(50, 10) != "█████░░░░░" || Bar(150, 4) != "████" || Bar(-1, 4) != "░░░░" {
		t.Error("Bar salah")
	}
}

func TestSamplerSistemNyata(t *testing.T) {
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("tidak ada /proc")
	}
	s := &Sampler{ProcRoot: "/proc"}
	snap, err := s.Sample(time.Now())
	if err != nil || snap.Memory.TotalKB == 0 || snap.Cores == 0 || len(snap.Procs) == 0 {
		t.Fatalf("snapshot nyata: %v cores=%d procs=%d", err, snap.Cores, len(snap.Procs))
	}
}

func TestParseOOM(t *testing.T) {
	out := `2026-09-16T03:12:44+04:00 web01 kernel: node invoked oom-killer: gfp_mask=0x140cca
2026-09-16T03:12:44+04:00 web01 kernel: Out of memory: Killed process 2211 (node) total-vm:4123456kB, anon-rss:3012345kB
2026-09-16T04:00:01+0400 web01 kernel: Memory cgroup out of memory: Killed process 3100 (java) total-vm:1kB
`
	ev := ParseOOM(out)
	if len(ev) != 2 || ev[0].PID != 2211 || ev[0].Process != "node" || ev[1].Process != "java" {
		t.Fatalf("%+v", ev)
	}
	if ev[0].Time.IsZero() || ev[1].Time.IsZero() {
		t.Errorf("waktu tidak terbaca: %+v", ev)
	}
}
