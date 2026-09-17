// Package resource membaca pemakaian CPU, RAM, swap, load, tekanan (PSI), dan proses terberat
// dari /proc, lalu menerjemahkannya menjadi temuan yang bisa dipahami pemula.
package resource

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
)

// Memory dalam kB, dari /proc/meminfo.
type Memory struct {
	TotalKB, FreeKB, AvailableKB, BuffersKB, CachedKB, SReclaimableKB, ShmemKB int64
	SwapTotalKB, SwapFreeKB                                                    int64
}

// UsedKB adalah memori yang benar-benar dipakai aplikasi (Total - Available).
func (m Memory) UsedKB() int64 { return m.TotalKB - m.AvailableKB }

// CacheKB adalah memori yang dipakai cache & buffer (bisa diambil kembali saat dibutuhkan).
func (m Memory) CacheKB() int64 { return m.BuffersKB + m.CachedKB + m.SReclaimableKB - m.ShmemKB }

// SwapUsedKB adalah swap yang terpakai.
func (m Memory) SwapUsedKB() int64 { return m.SwapTotalKB - m.SwapFreeKB }

// ParseMeminfo mem-parse /proc/meminfo.
func ParseMeminfo(s string) Memory {
	var m Memory
	fields := map[string]*int64{
		"MemTotal": &m.TotalKB, "MemFree": &m.FreeKB, "MemAvailable": &m.AvailableKB,
		"Buffers": &m.BuffersKB, "Cached": &m.CachedKB, "SReclaimable": &m.SReclaimableKB,
		"Shmem": &m.ShmemKB, "SwapTotal": &m.SwapTotalKB, "SwapFree": &m.SwapFreeKB,
	}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if dst, ok := fields[k]; ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				*dst, _ = strconv.ParseInt(f[0], 10, 64)
			}
		}
	}
	return m
}

// Load adalah isi /proc/loadavg.
type Load struct {
	One, Five, Fifteen float64
	Running, Total     int
}

// ParseLoadavg mem-parse "0.45 0.54 0.57 2/1176 40204".
func ParseLoadavg(s string) (Load, error) {
	f := strings.Fields(s)
	if len(f) < 4 {
		return Load{}, fmt.Errorf("format loadavg tidak dikenal: %q", s)
	}
	var l Load
	var err error
	if l.One, err = strconv.ParseFloat(f[0], 64); err != nil {
		return l, err
	}
	l.Five, _ = strconv.ParseFloat(f[1], 64)
	l.Fifteen, _ = strconv.ParseFloat(f[2], 64)
	if r, t, ok := strings.Cut(f[3], "/"); ok {
		l.Running, _ = strconv.Atoi(r)
		l.Total, _ = strconv.Atoi(t)
	}
	return l, nil
}

// CPUTimes adalah total jiffies dari baris "cpu" /proc/stat.
type CPUTimes struct {
	Idle, Total int64
}

// ParseStat mengambil total CPU dan jumlah core dari /proc/stat.
func ParseStat(s string) (CPUTimes, int) {
	var t CPUTimes
	cores := 0
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		switch {
		case f[0] == "cpu":
			for i, v := range f[1:] {
				n, _ := strconv.ParseInt(v, 10, 64)
				// guest & guest_nice (kolom 9 & 10) sudah termasuk di user & nice.
				if i >= 8 {
					break
				}
				t.Total += n
				if i == 3 || i == 4 { // idle, iowait
					t.Idle += n
				}
			}
		case strings.HasPrefix(f[0], "cpu"):
			cores++
		}
	}
	return t, cores
}

// Busy menghitung persen CPU terpakai di antara dua sampel.
func Busy(prev, cur CPUTimes) float64 {
	dt := cur.Total - prev.Total
	if dt <= 0 {
		return 0
	}
	return 100 * float64(dt-(cur.Idle-prev.Idle)) / float64(dt)
}

// Pressure adalah nilai PSI "some avg10/avg60/avg300" (persen waktu ada tugas yang tertahan).
type Pressure struct {
	Available            bool
	Avg10, Avg60, Avg300 float64
}

// ParsePressure mengambil baris "some" dari /proc/pressure/*.
func ParsePressure(s string) Pressure {
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(line, "some ") {
			continue
		}
		p := Pressure{Available: true}
		for _, kv := range strings.Fields(line)[1:] {
			k, v, _ := strings.Cut(kv, "=")
			n, _ := strconv.ParseFloat(v, 64)
			switch k {
			case "avg10":
				p.Avg10 = n
			case "avg60":
				p.Avg60 = n
			case "avg300":
				p.Avg300 = n
			}
		}
		return p
	}
	return Pressure{}
}

// Proc adalah satu proses dengan pemakaian sumber daya.
type Proc struct {
	PID     int
	Name    string
	User    string
	UID     int
	CPU     float64 // persen satu core (seperti top); bisa >100 untuk proses multi-thread
	RSSKB   int64
	Command string
	ticks   int64
}

// Snapshot adalah pembacaan lengkap satu waktu.
type Snapshot struct {
	Time     time.Time
	Cores    int
	CPU      CPUTimes
	CPUBusy  float64 // persen, 0 pada sampel pertama
	Memory   Memory
	Load     Load
	Uptime   time.Duration
	PSI      struct{ CPU, IO, Memory Pressure }
	Procs    []Proc
	HasDelta bool // CPU per proses dihitung dari sampel sebelumnya
}

// Sampler membaca snapshot berulang dan menghitung CPU dari selisih antar sampel.
type Sampler struct {
	ProcRoot string
	prev     *Snapshot
}

// Sample membaca snapshot baru.
func (s *Sampler) Sample(now time.Time) (Snapshot, error) {
	root := s.ProcRoot
	var snap Snapshot
	snap.Time = now

	stat, err := os.ReadFile(filepath.Join(root, "stat"))
	if err != nil {
		return snap, err
	}
	snap.CPU, snap.Cores = ParseStat(string(stat))
	snap.Cores = max(snap.Cores, 1)

	mem, err := os.ReadFile(filepath.Join(root, "meminfo"))
	if err != nil {
		return snap, err
	}
	snap.Memory = ParseMeminfo(string(mem))

	if la, err := os.ReadFile(filepath.Join(root, "loadavg")); err == nil {
		snap.Load, _ = ParseLoadavg(string(la))
	}
	if up, err := os.ReadFile(filepath.Join(root, "uptime")); err == nil {
		if f := strings.Fields(string(up)); len(f) > 0 {
			sec, _ := strconv.ParseFloat(f[0], 64)
			snap.Uptime = time.Duration(sec * float64(time.Second))
		}
	}
	for name, dst := range map[string]*Pressure{"cpu": &snap.PSI.CPU, "io": &snap.PSI.IO, "memory": &snap.PSI.Memory} {
		if data, err := os.ReadFile(filepath.Join(root, "pressure", name)); err == nil {
			*dst = ParsePressure(string(data))
		}
	}

	snap.Procs = readProcs(root)
	if s.prev != nil {
		snap.CPUBusy = Busy(s.prev.CPU, snap.CPU)
		elapsed := now.Sub(s.prev.Time).Seconds()
		prevTicks := map[int]int64{}
		for _, p := range s.prev.Procs {
			prevTicks[p.PID] = p.ticks
		}
		if elapsed > 0 {
			for i := range snap.Procs {
				if before, ok := prevTicks[snap.Procs[i].PID]; ok {
					d := snap.Procs[i].ticks - before
					snap.Procs[i].CPU = 100 * float64(d) / procs.ClockTicks / elapsed
				}
			}
			snap.HasDelta = true
		}
	}
	s.prev = &snap
	return snap, nil
}

func readProcs(root string) []Proc {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []Proc
	seen := map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		// Membaca /proc saat proses dibuat/selesai bisa mengembalikan entri ganda.
		if err != nil || seen[pid] {
			continue
		}
		seen[pid] = true
		dir := filepath.Join(root, e.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		ticks, ok := procs.ParseStatTimes(string(stat))
		if !ok {
			continue
		}
		p := Proc{PID: pid, ticks: ticks}
		if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
			parseStatusInto(&p, string(status))
		}
		if p.RSSKB == 0 && isKernelThread(stat) {
			continue // kernel thread tidak relevan untuk pemula
		}
		if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
			p.Command = strings.Join(procs.SplitCmdline(cmd), " ")
		}
		p.User = procs.Username(p.UID)
		out = append(out, p)
	}
	return out
}

func isKernelThread(stat []byte) bool {
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return false
	}
	f := strings.Fields(s[i+1:])
	// field 4 = ppid; kernel thread berinduk kthreadd (PID 2) atau PID 0.
	return len(f) > 1 && (f[1] == "2" || f[1] == "0")
}

func parseStatusInto(p *Proc, s string) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Name":
			p.Name = v
		case "Uid":
			if f := strings.Fields(v); len(f) > 1 {
				p.UID, _ = strconv.Atoi(f[1])
			}
		case "VmRSS":
			if f := strings.Fields(v); len(f) > 0 {
				p.RSSKB, _ = strconv.ParseInt(f[0], 10, 64)
			}
		}
	}
}

// SortBy mengurutkan proses dan mengembalikan n teratas.
func SortBy(ps []Proc, byMemory bool, n int) []Proc {
	out := append([]Proc(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool {
		if byMemory {
			return out[i].RSSKB > out[j].RSSKB
		}
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].RSSKB > out[j].RSSKB
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}
