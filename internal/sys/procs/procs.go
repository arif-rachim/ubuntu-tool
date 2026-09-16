// Package procs membaca informasi proses dari /proc/PID. Murni pengumpul data.
package procs

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClockTicks adalah USER_HZ; di Linux amd64/arm64 selalu 100.
const ClockTicks = 100

// Process adalah ringkasan satu proses.
type Process struct {
	PID     int
	PPID    int
	Name    string   // dari /proc/PID/status (comm, maks 15 karakter)
	State   string   // R, S, D, Z, ...
	UID     int      // UID efektif
	User    string   // nama user, atau UID bila tidak ditemukan
	Cmdline []string // kosong untuk kernel thread
	Exe     string   // kosong bila tidak terbaca (butuh root untuk proses user lain)
	Cwd     string
	Started time.Time
	Threads int
	RSSKB   int64
	Cgroup  Cgroup

	// Denied bila sebagian info (exe, cwd) tidak bisa dibaca karena izin.
	Denied bool
}

// CommandLine mengembalikan cmdline sebagai satu baris, atau [nama] untuk kernel thread.
func (p Process) CommandLine() string {
	if len(p.Cmdline) == 0 {
		return "[" + p.Name + "]"
	}
	return strings.Join(p.Cmdline, " ")
}

// Cgroup adalah informasi pengelola proses dari /proc/PID/cgroup.
type Cgroup struct {
	Path      string // path cgroup lengkap
	Unit      string // unit systemd terdekat: nginx.service, session-2.scope, docker-<id>.scope
	UserUnit  bool   // unit milik systemd --user (di bawah user@UID.service)
	Container string // ID container Docker/containerd bila terdeteksi
}

// Service melaporkan apakah proses dikelola unit .service (bisa di-stop lewat systemctl).
func (c Cgroup) Service() bool { return strings.HasSuffix(c.Unit, ".service") }

var (
	userCache   = map[int]string{}
	userCacheMu sync.Mutex
)

// Username mencari nama user untuk UID dengan cache.
func Username(uid int) string {
	userCacheMu.Lock()
	defer userCacheMu.Unlock()
	if name, ok := userCache[uid]; ok {
		return name
	}
	name := strconv.Itoa(uid)
	if u, err := user.LookupId(name); err == nil {
		name = u.Username
	}
	userCache[uid] = name
	return name
}

// Exists melaporkan apakah PID masih ada.
func Exists(procRoot string, pid int) bool {
	_, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(pid)))
	return err == nil
}

// Read membaca satu proses. Error hanya bila proses tidak ada; info yang tidak terbaca
// karena izin ditandai Denied.
func Read(procRoot string, pid int) (Process, error) {
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	p := Process{PID: pid}

	status, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		return p, fmt.Errorf("proses %d: %w", pid, err)
	}
	parseStatus(&p, string(status))
	p.User = Username(p.UID)

	if data, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		p.Cmdline = SplitCmdline(data)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		if ticks, ok := parseStartTicks(string(data)); ok {
			if boot, err := BootTime(procRoot); err == nil {
				p.Started = boot.Add(time.Duration(ticks) * time.Second / ClockTicks)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		p.Cgroup = ParseCgroup(string(data))
	}

	p.Exe, err = os.Readlink(filepath.Join(dir, "exe"))
	if errors.Is(err, fs.ErrPermission) {
		p.Denied = true
	}
	p.Exe = strings.TrimSuffix(p.Exe, " (deleted)")
	p.Cwd, err = os.Readlink(filepath.Join(dir, "cwd"))
	if errors.Is(err, fs.ErrPermission) {
		p.Denied = true
	}
	return p, nil
}

func parseStatus(p *Process, s string) {
	for _, line := range strings.Split(s, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Name":
			p.Name = val
		case "State":
			p.State, _, _ = strings.Cut(val, " ")
		case "PPid":
			p.PPID, _ = strconv.Atoi(val)
		case "Uid":
			// real, effective, saved, fs
			if f := strings.Fields(val); len(f) > 1 {
				p.UID, _ = strconv.Atoi(f[1])
			}
		case "Threads":
			p.Threads, _ = strconv.Atoi(val)
		case "VmRSS":
			f := strings.Fields(val)
			if len(f) > 0 {
				p.RSSKB, _ = strconv.ParseInt(f[0], 10, 64)
			}
		}
	}
}

// SplitCmdline memecah /proc/PID/cmdline (dipisah NUL, diakhiri NUL).
func SplitCmdline(data []byte) []string {
	data = bytes.TrimRight(data, "\x00")
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(p)
	}
	return out
}

// parseStartTicks mengambil field ke-22 (starttime) dari /proc/PID/stat. Nama proses (field 2)
// bisa mengandung spasi dan tanda kurung, jadi parsing dimulai setelah ')' terakhir.
func parseStartTicks(stat string) (int64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	// f[0] adalah field 3 (state); starttime adalah field 22 → f[19].
	if len(f) < 20 {
		return 0, false
	}
	v, err := strconv.ParseInt(f[19], 10, 64)
	return v, err == nil
}

// ParseStatTimes mengambil utime+stime (field 14 dan 15, dalam clock ticks) dari /proc/PID/stat.
func ParseStatTimes(stat string) (int64, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 13 {
		return 0, false
	}
	u, err1 := strconv.ParseInt(f[11], 10, 64)
	s, err2 := strconv.ParseInt(f[12], 10, 64)
	return u + s, err1 == nil && err2 == nil
}

// BootTime membaca waktu boot dari baris btime di /proc/stat.
func BootTime(procRoot string) (time.Time, error) {
	f, err := os.Open(filepath.Join(procRoot, "stat"))
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return time.Unix(sec, 0), nil
		}
	}
	return time.Time{}, errors.New("btime tidak ditemukan di /proc/stat")
}

// ParseCgroup mengenali unit systemd dan container dari isi /proc/PID/cgroup (v2 maupun v1).
func ParseCgroup(s string) Cgroup {
	var path, v1 string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		// cgroup v2: "0::/path". cgroup v1/hybrid: "1:name=systemd:/path" lebih informatif,
		// karena pada mode hybrid baris v2 sering hanya "0::/".
		switch {
		case parts[1] == "name=systemd":
			v1 = parts[2]
		case parts[0] == "0" && parts[1] == "":
			path = parts[2]
		}
	}
	if v1 != "" && (path == "" || path == "/") {
		path = v1
	}
	c := Cgroup{Path: path}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, "user@") && strings.HasSuffix(seg, ".service") && i < len(segs)-1 {
			c.UserUnit = true
			continue
		}
		if strings.HasSuffix(seg, ".service") || strings.HasSuffix(seg, ".scope") {
			c.Unit = seg
		}
	}
	if c.UserUnit && c.Unit == "" {
		c.UserUnit = false
	}
	for _, prefix := range []string{"docker-", "cri-containerd-", "libpod-"} {
		if strings.HasPrefix(c.Unit, prefix) && strings.HasSuffix(c.Unit, ".scope") {
			c.Container = strings.TrimSuffix(strings.TrimPrefix(c.Unit, prefix), ".scope")
		}
	}
	return c
}
