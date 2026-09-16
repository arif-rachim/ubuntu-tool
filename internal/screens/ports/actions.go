package ports

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// Nilai aksi yang bisa dipilih di layar detail.
const (
	ActStopUnit   = "stop-unit"
	ActTerm       = "term"
	ActKill       = "kill"
	ActDockerStop = "docker-stop"
	ActFindOwner  = "find-owner"
)

// Target adalah port yang akan ditindak beserta proses pemiliknya.
type Target struct {
	Listener sysports.Listener
	Proc     *procs.Process // proses utama (induk bila beberapa proses berbagi socket); nil bila tidak terlihat
	Shared   int            // jumlah proses yang berbagi socket ini
}

// Context adalah informasi user saat ini yang memengaruhi aksi.
type Context struct {
	UID     int
	IsRoot  bool
	SelfPID int
	// SSHPort adalah port server dari sesi SSH saat ini ($SSH_CONNECTION), 0 bila bukan sesi SSH.
	SSHPort int
}

// SSHPortFromEnv membaca port server dari $SSH_CONNECTION ("ip_client port_client ip_server port_server").
func SSHPortFromEnv() int {
	f := strings.Fields(os.Getenv("SSH_CONNECTION"))
	if len(f) != 4 {
		return 0
	}
	p, _ := strconv.Atoi(f[3])
	return p
}

// MainPID memilih proses utama dari beberapa proses yang berbagi socket: yang induknya
// bukan salah satu dari mereka (mis. master nginx, bukan worker-nya).
func MainPID(pids []int, parent func(pid int) int) int {
	if len(pids) == 0 {
		return 0
	}
	set := map[int]bool{}
	for _, p := range pids {
		set[p] = true
	}
	for _, p := range pids {
		if !set[parent(p)] {
			return p
		}
	}
	return pids[0]
}

// dropsSSH melaporkan apakah menghentikan target bisa memutus sesi SSH user.
func (t Target) dropsSSH(ctx Context) bool {
	if t.Proc != nil && t.Proc.Name == "sshd" {
		return true
	}
	return ctx.SSHPort != 0 && int(t.Listener.Local.Port()) == ctx.SSHPort
}

// Options mengembalikan pilihan aksi untuk target, dengan rekomendasi yang sesuai kondisinya.
func Options(t Target, ctx Context) []ask.Option {
	port := t.Listener.Local.Port()
	if t.Proc == nil {
		return []ask.Option{{
			Value:       ActFindOwner,
			Label:       "Cari tahu pemiliknya dengan sudo",
			Recommended: true,
			Description: fmt.Sprintf("Pemilik port %d tidak terlihat oleh user ini. Jalankan ss sebagai root untuk melihat proses pemiliknya.", port),
		}}
	}
	p := t.Proc
	var opts []ask.Option
	blocked := ""
	switch {
	case p.PID == 1:
		blocked = "PID 1 adalah init sistem; menghentikannya mematikan seluruh server"
	case p.PID == ctx.SelfPID:
		blocked = "ini adalah ubt sendiri"
	}

	if c := p.Cgroup.Container; c != "" {
		opts = append(opts, ask.Option{
			Value:       ActDockerStop,
			Label:       "Hentikan container Docker-nya",
			Recommended: blocked == "",
			Description: "Proses ini berjalan di dalam container " + shortID(c) + ". Menghentikan prosesnya langsung akan membuat Docker menganggap container crash.",
			Disabled:    blocked,
		})
	}
	if p.Cgroup.Service() && p.Cgroup.Container == "" {
		desc := "Cara paling rapi: systemd menghentikan semua proses unit ini dan tidak menyalakannya lagi sampai di-start ulang."
		opts = append(opts, ask.Option{
			Value:       ActStopUnit,
			Label:       "Hentikan unit " + p.Cgroup.Unit,
			Recommended: blocked == "",
			Description: desc,
			Risk:        riskFor(t, ctx, risk.Caution),
			Disabled:    blocked,
		})
	}
	termDesc := "Minta proses berhenti baik-baik (SIGTERM): proses sempat menutup koneksi dan menyimpan data."
	if p.Cgroup.Service() {
		termDesc += " Karena dikelola systemd, proses bisa dinyalakan lagi otomatis bila unit memakai Restart=."
	}
	if t.Shared > 1 {
		termDesc += fmt.Sprintf(" %d proses berbagi port ini; yang dihentikan adalah proses induknya (PID %d).", t.Shared, p.PID)
	}
	opts = append(opts,
		ask.Option{
			Value:       ActTerm,
			Label:       fmt.Sprintf("Hentikan proses %s (PID %d)", p.Name, p.PID),
			Recommended: blocked == "" && len(opts) == 0,
			Description: termDesc,
			Risk:        riskFor(t, ctx, risk.Caution),
			Disabled:    blocked,
		},
		ask.Option{
			Value:       ActKill,
			Label:       "Paksa berhenti (SIGKILL)",
			Description: "Proses langsung dimatikan kernel tanpa sempat membereskan apa pun. Pakai hanya bila SIGTERM tidak berhasil.",
			Risk:        risk.Dangerous,
			Disabled:    blocked,
		},
	)
	return opts
}

func riskFor(t Target, ctx Context, base risk.Level) risk.Level {
	if t.dropsSSH(ctx) {
		return risk.Dangerous
	}
	return base
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Plan membangun command untuk aksi yang dipilih.
func Plan(action string, t Target, ctx Context) run.Plan {
	l := t.Listener
	port := l.Local.Port()
	proto := string(l.Proto)
	sshWarn := ""
	if t.dropsSSH(ctx) {
		sshWarn = " PERINGATAN: ini adalah layanan SSH. Sesi SSH kamu (dan orang lain) bisa terputus dan kamu tidak bisa login lagi sampai layanan dinyalakan dari konsol."
	}

	switch action {
	case ActFindOwner:
		filter := fmt.Sprintf("sport = :%d", port)
		flag := "-tlpn"
		if l.Proto == sysports.UDP {
			flag = "-ulpn"
		}
		return run.Single(run.Command{
			Title:     fmt.Sprintf("Cari pemilik port %d/%s", port, proto),
			Argv:      []string{"ss", flag, filter},
			NeedsRoot: true,
			Explain: []run.Line{
				{Token: "ss", Meaning: "tampilkan socket (pengganti netstat)"},
				{Token: flag, Meaning: "t/u = TCP/UDP, l = yang listening, p = tampilkan proses, n = angka port apa adanya"},
				{Token: filter, Meaning: "hanya socket dengan port lokal " + strconv.Itoa(int(port))},
			},
			Effect: "Hanya membaca. Nama proses & PID pemilik terlihat di kolom users:((...)).",
		})
	}

	p := t.Proc
	if p == nil {
		return run.Plan{}
	}
	needsRoot := p.UID != ctx.UID
	pid := strconv.Itoa(p.PID)

	switch action {
	case ActStopUnit:
		argv := []string{"systemctl", "stop", p.Cgroup.Unit}
		root := true
		if p.Cgroup.UserUnit {
			argv = []string{"systemctl", "--user", "stop", p.Cgroup.Unit}
			root = false
		}
		explain := []run.Line{
			{Token: "systemctl", Meaning: "alat pengelola service systemd"},
			{Token: "stop", Meaning: "hentikan unit sekarang (tidak mengubah apakah ia menyala saat boot)"},
			{Token: p.Cgroup.Unit, Meaning: "unit yang menjalankan " + p.Name},
		}
		if p.Cgroup.UserUnit {
			explain = append(explain[:1], append([]run.Line{{Token: "--user", Meaning: "unit milik user kamu, bukan unit sistem"}}, explain[1:]...)...)
		}
		return run.Single(run.Command{
			Title:     "Hentikan unit " + p.Cgroup.Unit,
			Argv:      argv,
			NeedsRoot: root,
			Explain:   explain,
			Effect:    fmt.Sprintf("Semua proses unit berhenti dan port %d/%s menjadi kosong. Unit tetap menyala lagi saat boot bila di-enable.%s", port, proto, sshWarn),
			Safer:     "sudo systemctl restart " + p.Cgroup.Unit + " bila tujuanmu hanya memuat ulang layanan",
			Risk:      riskFor(t, ctx, risk.Caution),
		})
	case ActDockerStop:
		id := shortID(p.Cgroup.Container)
		return run.Single(run.Command{
			Title: "Hentikan container " + id,
			Argv:  []string{"docker", "stop", id},
			Explain: []run.Line{
				{Token: "docker stop", Meaning: "kirim SIGTERM ke container, tunggu 10 detik, lalu SIGKILL bila belum berhenti"},
				{Token: id, Meaning: "ID container pemilik port"},
			},
			Effect: fmt.Sprintf("Container berhenti (tidak dihapus) dan port %d/%s menjadi kosong. Bila container memakai restart policy \"always\", Docker bisa menyalakannya lagi.", port, proto),
			Risk:   risk.Caution,
		})
	case ActTerm, ActKill:
		sig, sigDesc, lvl := "-TERM", "sinyal \"tolong berhenti baik-baik\"", riskFor(t, ctx, risk.Caution)
		title := fmt.Sprintf("Hentikan proses %s (PID %d)", p.Name, p.PID)
		effect := fmt.Sprintf("%s berhenti dan port %d/%s menjadi kosong.", p.Name, port, proto)
		safer := ""
		if action == ActKill {
			sig, sigDesc, lvl = "-KILL", "sinyal yang tidak bisa ditolak: kernel langsung mematikan proses", risk.Dangerous
			title = fmt.Sprintf("Paksa hentikan %s (PID %d)", p.Name, p.PID)
			effect += " Data yang belum tersimpan bisa hilang dan file sementara tertinggal."
			safer = "coba dulu kill -TERM " + pid
		}
		if p.Cgroup.Service() {
			effect += " Karena dikelola " + p.Cgroup.Unit + ", systemd bisa menyalakannya lagi otomatis."
			if safer == "" {
				safer = "hentikan unit-nya → sudo systemctl stop " + p.Cgroup.Unit
			}
		}
		return run.Single(run.Command{
			Title:     title,
			Argv:      []string{"kill", sig, pid},
			NeedsRoot: needsRoot,
			Explain: []run.Line{
				{Token: "kill", Meaning: "kirim sinyal ke sebuah proses"},
				{Token: sig, Meaning: sigDesc},
				{Token: pid, Meaning: "PID dari " + p.Name},
			},
			Effect: effect + sshWarn,
			Safer:  safer,
			Risk:   lvl,
		})
	}
	return run.Plan{}
}
