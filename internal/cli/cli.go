// Package cli berisi subcommand non-interaktif ubt: doctor, ports --json, dan history.
// Output ditujukan untuk dibaca manusia di terminal atau diproses script (JSON).
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
)

// Tool adalah satu program luar yang dipakai modul ubt.
type Tool struct {
	Binary   string   `json:"binary"`
	Package  string   `json:"package"`
	Modules  []string `json:"modules"`
	Purpose  string   `json:"purpose"`
	Paths    []string `json:"-"` // lokasi tambahan di luar PATH (mis. /usr/sbin untuk user biasa, /snap/bin)
	Found    string   `json:"found,omitempty"`
	Optional bool     `json:"optional"`
}

// Tools adalah daftar program yang dipakai ubt beserta paket apt-nya.
func Tools() []Tool {
	return []Tool{
		{Binary: "systemctl", Package: "systemd", Modules: []string{"Service", "Penjadwalan", "Diagnosa"}, Purpose: "mengelola service & timer"},
		{Binary: "journalctl", Package: "systemd", Modules: []string{"Log", "Diagnosa"}, Purpose: "membaca log"},
		{Binary: "sudo", Package: "sudo", Modules: []string{"semua aksi yang butuh root"}, Purpose: "menjalankan command sebagai root"},
		{Binary: "ip", Package: "iproute2", Modules: []string{"Network"}, Purpose: "alamat & route"},
		{Binary: "resolvectl", Package: "systemd-resolved", Modules: []string{"Network"}, Purpose: "server DNS", Optional: true},
		{Binary: "apt-get", Package: "apt", Modules: []string{"Paket"}, Purpose: "install & update paket"},
		{Binary: "dpkg", Package: "dpkg", Modules: []string{"Paket"}, Purpose: "status paket terpasang"},
		{Binary: "ufw", Package: "ufw", Modules: []string{"Firewall"}, Purpose: "firewall", Paths: []string{"/usr/sbin/ufw"}},
		{Binary: "sshd", Package: "openssh-server", Modules: []string{"User & SSH"}, Purpose: "server SSH", Paths: []string{"/usr/sbin/sshd"}, Optional: true},
		{Binary: "ssh-keygen", Package: "openssh-client", Modules: []string{"User & SSH"}, Purpose: "membuat & memeriksa SSH key"},
		{Binary: "useradd", Package: "passwd", Modules: []string{"User & SSH"}, Purpose: "menambah user", Paths: []string{"/usr/sbin/useradd"}},
		{Binary: "crontab", Package: "cron", Modules: []string{"Penjadwalan"}, Purpose: "jadwal cron per user", Optional: true},
		{Binary: "lsblk", Package: "util-linux", Modules: []string{"Disk"}, Purpose: "daftar perangkat disk"},
		{Binary: "swapon", Package: "util-linux", Modules: []string{"Disk"}, Purpose: "status swap", Paths: []string{"/usr/sbin/swapon"}},
		{Binary: "ss", Package: "iproute2", Modules: []string{"Ports"}, Purpose: "cadangan pembacaan port", Optional: true},
		{Binary: "dig", Package: "bind9-dnsutils", Modules: []string{"Network"}, Purpose: "uji DNS lanjutan", Optional: true},
		{Binary: "nc", Package: "netcat-openbsd", Modules: []string{"Network"}, Purpose: "uji port manual", Optional: true},
		{Binary: "tracepath", Package: "iputils-tracepath", Modules: []string{"Network"}, Purpose: "melacak rute", Optional: true},
		{Binary: "curl", Package: "curl", Modules: []string{"Network"}, Purpose: "IP publik & uji HTTP manual", Optional: true},
		{Binary: "ncdu", Package: "ncdu", Modules: []string{"Disk"}, Purpose: "penjelajah ukuran folder", Optional: true},
		{Binary: "needrestart", Package: "needrestart", Modules: []string{"Paket"}, Purpose: "service yang perlu restart setelah update", Optional: true, Paths: []string{"/usr/sbin/needrestart"}},
		{Binary: "nginx", Package: "nginx", Modules: []string{"Web & TLS"}, Purpose: "web server & reverse proxy", Optional: true, Paths: []string{"/usr/sbin/nginx"}},
		{Binary: "certbot", Package: "certbot (snap)", Modules: []string{"Web & TLS"}, Purpose: "sertifikat HTTPS Let's Encrypt", Optional: true, Paths: []string{"/snap/bin/certbot"}},
		{Binary: "docker", Package: "docker.io", Modules: []string{"Docker"}, Purpose: "container", Optional: true},
	}
}

// LookPath dapat diganti saat test.
var LookPath = exec.LookPath

func findTool(t Tool) string {
	if p, err := LookPath(t.Binary); err == nil {
		return p
	}
	for _, p := range t.Paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Doctor memeriksa ketersediaan semua program. Exit 1 bila ada program wajib yang tidak ditemukan.
func Doctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "keluaran JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	tools := Tools()
	missingRequired := 0
	var missingPkgs []string
	for i := range tools {
		tools[i].Found = findTool(tools[i])
		if tools[i].Found == "" {
			if !tools[i].Optional {
				missingRequired++
			}
			if !strings.Contains(tools[i].Package, " ") {
				missingPkgs = append(missingPkgs, tools[i].Package)
			}
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(tools)
	} else {
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\tPROGRAM\tPAKET APT\tDIPAKAI UNTUK")
		for _, t := range tools {
			mark := "✓"
			switch {
			case t.Found == "" && t.Optional:
				mark = "–"
			case t.Found == "":
				mark = "✗"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s (%s)\n", mark, t.Binary, t.Package, t.Purpose, strings.Join(t.Modules, ", "))
		}
		w.Flush()
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "✓ ada   – opsional, belum terpasang   ✗ wajib, belum terpasang")
		if len(missingPkgs) > 0 {
			fmt.Fprintf(stdout, "\nUntuk memasang yang belum ada (termasuk yang opsional):\n  sudo apt install %s\n", strings.Join(dedupe(missingPkgs), " "))
		}
	}
	if missingRequired > 0 {
		return 1
	}
	return 0
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// PortJSON adalah satu port listening untuk `ubt ports --json`.
type PortJSON struct {
	Proto     string        `json:"proto"`
	Address   string        `json:"address"`
	Port      uint16        `json:"port"`
	Scope     string        `json:"scope"` // all, localhost, specific
	Processes []ProcessJSON `json:"processes"`
}

// ProcessJSON adalah pemilik port.
type ProcessJSON struct {
	PID       int    `json:"pid"`
	Name      string `json:"name"`
	User      string `json:"user"`
	Command   string `json:"command,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Container string `json:"container,omitempty"`
}

// ProcRoot dapat diganti saat test.
var ProcRoot = "/proc"

// Ports menampilkan port listening.
func Ports(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ports", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "keluaran JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	snap, err := ports.ReadListeners(ProcRoot)
	if err != nil {
		fmt.Fprintf(stderr, "ubt ports: %v\n", err)
		return 1
	}
	var out []PortJSON
	for _, l := range snap.Listeners {
		p := PortJSON{Proto: string(l.Proto), Address: l.Local.Addr().String(), Port: l.Local.Port(), Processes: []ProcessJSON{}}
		switch l.Scope() {
		case ports.ScopeAll:
			p.Scope = "all"
		case ports.ScopeLoopback:
			p.Scope = "localhost"
		default:
			p.Scope = "specific"
		}
		for _, pid := range l.PIDs {
			pr, err := procs.Read(ProcRoot, pid)
			if err != nil {
				continue
			}
			p.Processes = append(p.Processes, ProcessJSON{PID: pid, Name: pr.Name, User: pr.User, Command: strings.Join(pr.Cmdline, " "), Unit: pr.Cgroup.Unit, Container: pr.Cgroup.Container})
		}
		out = append(out, p)
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if out == nil {
			out = []PortJSON{}
		}
		_ = enc.Encode(out)
		return 0
	}
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROTO\tALAMAT\tPORT\tAKSES\tPROSES")
	for _, p := range out {
		var names []string
		for _, pr := range p.Processes {
			names = append(names, fmt.Sprintf("%s (PID %d)", pr.Name, pr.PID))
		}
		owner := strings.Join(names, ", ")
		if owner == "" {
			owner = "? (milik user lain; jalankan dengan sudo)"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", p.Proto, p.Address, p.Port, map[string]string{"all": "dari mana saja", "localhost": "hanya server ini", "specific": "IP tertentu"}[p.Scope], owner)
	}
	w.Flush()
	return 0
}

// HistoryFile dapat diganti saat test.
var HistoryFile = func() (*run.History, error) { return run.DefaultHistory() }

// History menampilkan atau mengekspor riwayat command.
func History(args []string, stdout, stderr io.Writer) int {
	export := len(args) > 0 && args[0] == "export"
	if export {
		args = args[1:]
	}
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	last := fs.Int("last", 0, "hanya N command terakhir (0 = semua)")
	asJSON := fs.Bool("json", false, "keluaran JSON (hanya untuk daftar)")
	withFailed := fs.Bool("include-failed", false, "ekspor: ikutkan command yang gagal (tetap dikomentari)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	h, err := HistoryFile()
	if err != nil {
		fmt.Fprintf(stderr, "ubt history: %v\n", err)
		return 1
	}
	entries, err := h.Read(*last)
	if err != nil {
		fmt.Fprintf(stderr, "ubt history: %v\n", err)
		return 1
	}
	switch {
	case export:
		fmt.Fprint(stdout, ExportScript(entries, *withFailed, time.Now()))
	case *asJSON:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if entries == nil {
			entries = []run.Entry{}
		}
		_ = enc.Encode(entries)
	default:
		if len(entries) == 0 {
			fmt.Fprintln(stdout, "Riwayat masih kosong. Command yang dijalankan lewat ubt akan tercatat di "+h.Path)
			return 0
		}
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "WAKTU\tSTATUS\tCOMMAND")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.Time.Local().Format("2006-01-02 15:04"), EntryStatus(e), EntryCommand(e))
		}
		w.Flush()
	}
	return 0
}

// EntryCommand adalah command satu entri seperti diketik di shell.
func EntryCommand(e run.Entry) string {
	return run.Command{Argv: e.Argv, NeedsRoot: e.Sudo}.Preview(false)
}

// EntryStatus meringkas hasil satu entri.
func EntryStatus(e run.Entry) string {
	switch {
	case e.ExitCode == 0 && e.Error == "":
		return "ok"
	case e.ExitCode > 0:
		return fmt.Sprintf("gagal (exit %d)", e.ExitCode)
	}
	return "gagal"
}

// ExportScript mengubah riwayat menjadi script bash yang bisa dijalankan ulang di server lain.
// Command yang gagal dilewati (atau dikomentari bila includeFailed). Isi stdin sensitif tidak diikutkan.
func ExportScript(entries []run.Entry, includeFailed bool, now time.Time) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	fmt.Fprintf(&b, "# Diekspor oleh ubt pada %s dari %d command.\n", now.Format("2006-01-02 15:04"), len(entries))
	b.WriteString("# Baca dulu sebelum menjalankan: urutan dan konteksnya mungkin berbeda di server lain.\n")
	b.WriteString("set -euo pipefail\n")
	skipped := 0
	for _, e := range entries {
		ok := e.ExitCode == 0 && e.Error == ""
		if !ok && !includeFailed {
			skipped++
			continue
		}
		b.WriteString("\n")
		label := e.Title
		if e.Check {
			label += " (validasi)"
		}
		fmt.Fprintf(&b, "# %s — %s\n", label, e.Time.Local().Format("2006-01-02 15:04"))
		cmd := run.Command{Argv: e.Argv, NeedsRoot: e.Sudo, Stdin: e.Stdin, Sensitive: e.Hidden}
		if e.Hidden {
			cmd.Stdin, cmd.StdinLabel = "x", "isi rahasia tidak disimpan di riwayat"
		}
		script := cmd.Script(false)
		if !ok {
			fmt.Fprintf(&b, "# GAGAL saat dijalankan (%s); dikomentari:\n", EntryStatus(e))
			script = "# " + strings.ReplaceAll(script, "\n", "\n# ")
		}
		b.WriteString(script + "\n")
	}
	if skipped > 0 {
		fmt.Fprintf(&b, "\n# %d command yang gagal tidak diikutkan (pakai --include-failed untuk melihatnya).\n", skipped)
	}
	return b.String()
}

// SortNewestFirst mengurutkan salinan entri dari yang terbaru.
func SortNewestFirst(entries []run.Entry) []run.Entry {
	out := append([]run.Entry(nil), entries...)
	sort.SliceStable(out, func(a, b int) bool { return out[a].Time.After(out[b].Time) })
	return out
}
