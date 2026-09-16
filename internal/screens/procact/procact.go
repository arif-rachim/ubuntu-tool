// Package procact membangun pilihan aksi dan command untuk menghentikan atau mengatur sebuah
// proses: stop unit systemd, docker stop, SIGTERM/SIGKILL, dan renice. Dipakai modul Ports dan Resource.
package procact

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// Nilai aksi.
const (
	StopUnit   = "stop-unit"
	Term       = "term"
	Kill       = "kill"
	DockerStop = "docker-stop"
	Renice     = "renice"
)

// Context adalah informasi user saat ini yang memengaruhi aksi.
type Context struct {
	UID     int
	IsRoot  bool
	SelfPID int
	// SSHPort adalah port server sesi SSH saat ini ($SSH_CONNECTION), 0 bila bukan sesi SSH.
	SSHPort int
}

// CurrentContext membaca konteks dari proses & environment saat ini.
func CurrentContext(uid int, isRoot bool) Context {
	return Context{UID: uid, IsRoot: isRoot, SelfPID: os.Getpid(), SSHPort: SSHPortFromEnv()}
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

// Subject menjelaskan konteks tambahan target, mis. port yang dipegang proses.
type Subject struct {
	Port    int    // 0 bila tidak terkait port
	Proto   string // "tcp"/"udp"
	Shared  int    // jumlah proses yang berbagi port
	Renice  bool   // tawarkan renice (modul Resource)
	TopHint string // penjelasan tambahan untuk SIGTERM, mis. "memakai CPU 95%"
}

func (s Subject) portText() string {
	if s.Port == 0 {
		return ""
	}
	return fmt.Sprintf("port %d/%s", s.Port, s.Proto)
}

// DropsSSH melaporkan apakah menghentikan proses bisa memutus sesi SSH.
func DropsSSH(p procs.Process, s Subject, ctx Context) bool {
	if p.Name == "sshd" || p.Cgroup.Unit == "ssh.service" || p.Cgroup.Unit == "sshd.service" {
		return true
	}
	return ctx.SSHPort != 0 && s.Port == ctx.SSHPort
}

// Blocked mengembalikan alasan proses tidak boleh dihentikan, atau string kosong.
func Blocked(p procs.Process, ctx Context) string {
	switch {
	case p.PID == 1:
		return "PID 1 adalah init sistem; menghentikannya mematikan seluruh server"
	case p.PID == ctx.SelfPID:
		return "ini adalah ubt sendiri"
	case len(p.Cmdline) == 0 && p.PPID == 2:
		return "kernel thread tidak bisa dihentikan dari user space"
	}
	return ""
}

func riskFor(p procs.Process, s Subject, ctx Context, base risk.Level) risk.Level {
	if DropsSSH(p, s, ctx) {
		return risk.Dangerous
	}
	return base
}

// ShortID memendekkan ID container menjadi 12 karakter seperti docker ps.
func ShortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// Options mengembalikan pilihan aksi untuk proses, dengan rekomendasi sesuai pengelolanya.
func Options(p procs.Process, s Subject, ctx Context) []ask.Option {
	blocked := Blocked(p, ctx)
	var opts []ask.Option

	if c := p.Cgroup.Container; c != "" {
		opts = append(opts, ask.Option{
			Value:       DockerStop,
			Label:       "Hentikan container Docker-nya",
			Recommended: blocked == "",
			Description: "Proses ini berjalan di dalam container " + ShortID(c) + ". Menghentikan prosesnya langsung akan membuat Docker menganggap container crash.",
			Disabled:    blocked,
		})
	}
	if p.Cgroup.Service() && p.Cgroup.Container == "" {
		opts = append(opts, ask.Option{
			Value:       StopUnit,
			Label:       "Hentikan unit " + p.Cgroup.Unit,
			Recommended: blocked == "",
			Description: "Cara paling rapi: systemd menghentikan semua proses unit ini dan tidak menyalakannya lagi sampai di-start ulang.",
			Risk:        riskFor(p, s, ctx, risk.Caution),
			Disabled:    blocked,
		})
	}
	if s.Renice {
		opts = append(opts, ask.Option{
			Value:       Renice,
			Label:       "Turunkan prioritas CPU (renice +10)",
			Recommended: blocked == "" && len(opts) == 0,
			Description: "Proses tetap berjalan, tetapi mengalah pada proses lain saat CPU sibuk. Aman dan bisa dikembalikan.",
			Disabled:    blocked,
		})
	}
	termDesc := "Minta proses berhenti baik-baik (SIGTERM): proses sempat menutup koneksi dan menyimpan data."
	if s.TopHint != "" {
		termDesc = s.TopHint + " " + termDesc
	}
	if p.Cgroup.Service() {
		termDesc += " Karena dikelola systemd, proses bisa dinyalakan lagi otomatis bila unit memakai Restart=."
	}
	if s.Shared > 1 {
		termDesc += fmt.Sprintf(" %d proses berbagi port ini; yang dihentikan adalah proses induknya (PID %d).", s.Shared, p.PID)
	}
	opts = append(opts,
		ask.Option{
			Value:       Term,
			Label:       fmt.Sprintf("Hentikan proses %s (PID %d)", p.Name, p.PID),
			Recommended: blocked == "" && len(opts) == 0,
			Description: termDesc,
			Risk:        riskFor(p, s, ctx, risk.Caution),
			Disabled:    blocked,
		},
		ask.Option{
			Value:       Kill,
			Label:       "Paksa berhenti (SIGKILL)",
			Description: "Proses langsung dimatikan kernel tanpa sempat membereskan apa pun. Pakai hanya bila SIGTERM tidak berhasil.",
			Risk:        risk.Dangerous,
			Disabled:    blocked,
		},
	)
	return opts
}

// Plan membangun command untuk aksi pada proses. Plan kosong bila aksi tidak dikenal.
func Plan(action string, p procs.Process, s Subject, ctx Context) run.Plan {
	needsRoot := p.UID != ctx.UID && !ctx.IsRoot
	pid := strconv.Itoa(p.PID)
	sshWarn := ""
	if DropsSSH(p, s, ctx) {
		sshWarn = " PERINGATAN: ini adalah layanan SSH. Sesi SSH kamu (dan orang lain) bisa terputus dan kamu tidak bisa login lagi sampai layanan dinyalakan dari konsol."
	}
	freed := ""
	if pt := s.portText(); pt != "" {
		freed = " dan " + pt + " menjadi kosong"
	}

	switch action {
	case StopUnit:
		argv := []string{"systemctl", "stop", p.Cgroup.Unit}
		root := true
		explain := []run.Line{
			{Token: "systemctl", Meaning: "alat pengelola service systemd"},
			{Token: "stop", Meaning: "hentikan unit sekarang (tidak mengubah apakah ia menyala saat boot)"},
			{Token: p.Cgroup.Unit, Meaning: "unit yang menjalankan " + p.Name},
		}
		if p.Cgroup.UserUnit {
			argv = []string{"systemctl", "--user", "stop", p.Cgroup.Unit}
			root = false
			explain = append(explain[:1], append([]run.Line{{Token: "--user", Meaning: "unit milik user kamu, bukan unit sistem"}}, explain[1:]...)...)
		}
		return run.Single(run.Command{
			Title:     "Hentikan unit " + p.Cgroup.Unit,
			Argv:      argv,
			NeedsRoot: root,
			Explain:   explain,
			Effect:    "Semua proses unit berhenti" + freed + ". Unit tetap menyala lagi saat boot bila di-enable." + sshWarn,
			Safer:     "sudo systemctl restart " + p.Cgroup.Unit + " bila tujuanmu hanya memuat ulang layanan",
			Risk:      riskFor(p, s, ctx, risk.Caution),
		})

	case DockerStop:
		id := ShortID(p.Cgroup.Container)
		return run.Single(run.Command{
			Title: "Hentikan container " + id,
			Argv:  []string{"docker", "stop", id},
			Explain: []run.Line{
				{Token: "docker stop", Meaning: "kirim SIGTERM ke container, tunggu 10 detik, lalu SIGKILL bila belum berhenti"},
				{Token: id, Meaning: "ID container pemilik proses"},
			},
			Effect: "Container berhenti (tidak dihapus)" + freed + ". Bila container memakai restart policy \"always\", Docker bisa menyalakannya lagi.",
			Risk:   risk.Caution,
		})

	case Renice:
		return run.Single(run.Command{
			Title:     fmt.Sprintf("Turunkan prioritas %s (PID %d)", p.Name, p.PID),
			Argv:      []string{"renice", "-n", "10", "-p", pid},
			NeedsRoot: needsRoot,
			Explain: []run.Line{
				{Token: "renice", Meaning: "ubah prioritas (nice) proses yang sedang berjalan"},
				{Token: "-n 10", Meaning: "tambah nilai nice 10: makin tinggi makin \"mengalah\" (rentang -20 sampai 19)"},
				{Token: "-p " + pid, Meaning: "PID dari " + p.Name},
			},
			Effect: "Proses tetap berjalan, tetapi mendapat jatah CPU lebih kecil saat CPU sibuk. Menaikkan prioritas lagi butuh root.",
			Risk:   risk.Safe,
		})

	case Term, Kill:
		sig, sigDesc, lvl := "-TERM", "sinyal \"tolong berhenti baik-baik\"", riskFor(p, s, ctx, risk.Caution)
		title := fmt.Sprintf("Hentikan proses %s (PID %d)", p.Name, p.PID)
		effect := p.Name + " berhenti" + freed + "."
		safer := ""
		if action == Kill {
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
