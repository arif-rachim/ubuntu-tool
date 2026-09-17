// Package users membaca akun, grup, login, SSH key, dan konfigurasi SSH server, serta membangun
// command untuk mengelolanya dengan pengaman anti-terkunci. Murni pengumpul data.
package users

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// User adalah satu akun login.
type User struct {
	Name      string
	UID, GID  int
	Home      string
	Shell     string
	Groups    []string
	LastLogin time.Time // nol bila belum pernah login atau tidak diketahui
	CanLogin  bool      // shell bukan nologin/false
}

// InGroup melaporkan keanggotaan grup.
func (u User) InGroup(g string) bool {
	for _, x := range u.Groups {
		if x == g {
			return true
		}
	}
	return false
}

// Group adalah satu grup.
type Group struct {
	Name    string
	GID     int
	Members []string
}

// ParsePasswd mem-parse format /etc/passwd (atau getent passwd).
func ParsePasswd(s string) []User {
	var out []User
	for _, line := range strings.Split(s, "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		shell := f[6]
		out = append(out, User{Name: f[0], UID: uid, GID: gid, Home: f[5], Shell: shell,
			CanLogin: !strings.HasSuffix(shell, "nologin") && !strings.HasSuffix(shell, "/false") && shell != ""})
	}
	return out
}

// ParseGroup mem-parse format /etc/group.
func ParseGroup(s string) []Group {
	var out []Group
	for _, line := range strings.Split(s, "\n") {
		f := strings.Split(line, ":")
		if len(f) < 4 {
			continue
		}
		gid, _ := strconv.Atoi(f[2])
		g := Group{Name: f[0], GID: gid}
		if f[3] != "" {
			g.Members = strings.Split(f[3], ",")
		}
		out = append(out, g)
	}
	return out
}

// Human melaporkan akun manusia: root atau UID 1000–59999 yang bisa login.
func Human(u User) bool {
	return u.UID == 0 || (u.UID >= 1000 && u.UID < 60000 && u.CanLogin)
}

// ParseLastlog mem-parse output `lastlog`.
func ParseLastlog(s string) map[string]time.Time {
	out := map[string]time.Time{}
	for _, line := range strings.Split(s, "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 2 || strings.Contains(line, "**Never logged in**") {
			continue
		}
		if len(f) >= 7 {
			tail := strings.Join(f[len(f)-6:], " ")
			if t, err := time.Parse("Mon Jan 2 15:04:05 -0700 2006", tail); err == nil {
				out[f[0]] = t
			}
		}
	}
	return out
}

// Session adalah satu sesi login yang sedang aktif.
type Session struct {
	User string
	TTY  string
	From string
	Time string
}

// ParseWho mem-parse output `who`.
func ParseWho(s string) []Session {
	var out []Session
	for _, line := range strings.Split(s, "\n") {
		// Format tanggal tergantung locale ("2026-09-16 20:51" atau "Sep 16 20:51"), jadi asal login
		// diambil dari bagian dalam kurung di akhir baris, dan waktu adalah sisa kolom di tengah.
		from := ""
		if i := strings.LastIndex(line, "("); i >= 0 && strings.HasSuffix(strings.TrimSpace(line), ")") {
			from = strings.TrimSuffix(strings.TrimSpace(line[i+1:]), ")")
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		out = append(out, Session{User: f[0], TTY: f[1], Time: strings.Join(f[2:], " "), From: from})
	}
	return out
}

// Data adalah ringkasan akun di server.
type Data struct {
	Users    []User // hanya akun manusia
	Groups   map[string]Group
	Sessions []Session
}

// SudoMembers adalah anggota grup sudo (dan admin lama).
func (d Data) SudoMembers() []string {
	var out []string
	for _, g := range []string{"sudo", "admin", "wheel"} {
		out = append(out, d.Groups[g].Members...)
	}
	return out
}

// Read membaca akun, grup, login terakhir, dan sesi aktif.
func Read(ctx context.Context, r run.Runner) (Data, error) {
	d := Data{Groups: map[string]Group{}}
	passwd, _, err := r.Capture(ctx, run.Command{Argv: []string{"getent", "passwd"}})
	if err != nil {
		data, ferr := os.ReadFile("/etc/passwd")
		if ferr != nil {
			return d, err
		}
		passwd = string(data)
	}
	groupOut, _, err := r.Capture(ctx, run.Command{Argv: []string{"getent", "group"}})
	if err != nil {
		data, _ := os.ReadFile("/etc/group")
		groupOut = string(data)
	}
	groups := ParseGroup(groupOut)
	gidName := map[int]string{}
	for _, g := range groups {
		d.Groups[g.Name] = g
		gidName[g.GID] = g.Name
	}
	var last map[string]time.Time
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"lastlog"}}); err == nil {
		last = ParseLastlog(out)
	}
	for _, u := range ParsePasswd(passwd) {
		if !Human(u) {
			continue
		}
		if primary := gidName[u.GID]; primary != "" {
			u.Groups = append(u.Groups, primary)
		}
		for _, g := range groups {
			for _, m := range g.Members {
				if m == u.Name && g.GID != u.GID {
					u.Groups = append(u.Groups, g.Name)
				}
			}
		}
		sort.Strings(u.Groups[min(1, len(u.Groups)):])
		u.LastLogin = last[u.Name]
		d.Users = append(d.Users, u)
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"who"}}); err == nil {
		d.Sessions = ParseWho(out)
	}
	return d, nil
}

var nameRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ValidUsername memeriksa nama user sesuai aturan adduser Ubuntu.
func ValidUsername(s string) bool { return nameRe.MatchString(s) }

// FailedLogin adalah percobaan login SSH gagal yang dikelompokkan per IP.
type FailedLogin struct {
	IP       string
	Count    int
	Users    []string
	LastSeen time.Time
}

var (
	failedRe  = regexp.MustCompile(`Failed (?:password|publickey) for (?:invalid user )?(\S+) from (\S+)`)
	invalidRe = regexp.MustCompile(`Invalid user (\S*) from (\S+)`)
)

// ParseFailedLogins mengelompokkan baris log sshd berisi login gagal, terbanyak lebih dulu.
func ParseFailedLogins(lines []string, times []time.Time) []FailedLogin {
	byIP := map[string]*FailedLogin{}
	for i, l := range lines {
		m := failedRe.FindStringSubmatch(l)
		if m == nil {
			m = invalidRe.FindStringSubmatch(l)
		}
		if m == nil {
			continue
		}
		user, ip := m[1], m[2]
		f := byIP[ip]
		if f == nil {
			f = &FailedLogin{IP: ip}
			byIP[ip] = f
		}
		f.Count++
		if user != "" && !contains(f.Users, user) && len(f.Users) < 5 {
			f.Users = append(f.Users, user)
		}
		if i < len(times) && times[i].After(f.LastSeen) {
			f.LastSeen = times[i]
		}
	}
	out := make([]FailedLogin, 0, len(byIP))
	for _, f := range byIP {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].IP < out[j].IP
	})
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
