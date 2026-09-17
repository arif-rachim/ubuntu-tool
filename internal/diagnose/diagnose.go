// Package diagnose merangkai pemeriksaan read-only lintas modul berdasarkan gejala yang dirasakan user
// ("disk penuh", "server lambat", ...). Setiap langkah menjelaskan temuan dan menyarankan modul
// yang bisa memperbaikinya; tidak ada yang mengubah sistem.
package diagnose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/check"
	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/systemd"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/tlscheck"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/web"
)

// Env adalah sumber data semua pemeriksaan (diganti saat test).
type Env struct {
	Runner     run.Runner
	ProcRoot   string
	Now        func() time.Time
	UID        int
	Username   string
	Home       string
	Packages   packages.Paths
	Cleanup    disk.CleanupSources
	Schedule   schedule.Sources
	Web        web.Paths
	SSHDConfig string
	SSHDBinary string
	CertLive   string // /etc/letsencrypt/live
	// Sample mengambil dua sampel resource berjarak singkat (supaya CPU% terhitung).
	Sample func(ctx context.Context) (resource.Snapshot, error)
}

// DefaultEnv memakai sistem sungguhan.
func DefaultEnv(r run.Runner, procRoot string, uid int, now func() time.Time) Env {
	home, _ := os.UserHomeDir()
	name := os.Getenv("USER")
	return Env{
		Runner: r, ProcRoot: procRoot, Now: now, UID: uid, Username: name, Home: home,
		Packages: packages.DefaultPaths, Cleanup: disk.DefaultCleanupSources, Schedule: schedule.DefaultSources,
		Web: web.DefaultPaths, SSHDConfig: "/etc/ssh/sshd_config", SSHDBinary: "/usr/sbin/sshd", CertLive: "/etc/letsencrypt/live",
		Sample: func(ctx context.Context) (resource.Snapshot, error) {
			s := &resource.Sampler{ProcRoot: procRoot}
			if _, err := s.Sample(time.Now()); err != nil {
				return resource.Snapshot{}, err
			}
			select {
			case <-ctx.Done():
				return resource.Snapshot{}, ctx.Err()
			case <-time.After(700 * time.Millisecond):
			}
			return s.Sample(time.Now())
		},
	}
}

// Symptom adalah satu gejala di menu Diagnosa.
type Symptom struct {
	ID          string
	Label       string
	Description string
	Intro       string
	// Open: gejala ini ditangani wizard modul lain (mis. "network:inbound"); Steps kosong.
	Open string
	// Steps membuat urutan pemeriksaan. Semua langkah dijalankan walau ada yang gagal.
	Steps func(Env) []check.Step
}

// Symptoms adalah daftar gejala, urut dari yang paling sering dialami.
func Symptoms() []Symptom {
	return []Symptom{
		{ID: "health", Label: "Cek kesehatan umum", Description: "Ringkasan cepat semua hal penting: disk, RAM, update keamanan, reboot, service gagal, sertifikat, SSH.",
			Intro: "Pemeriksaan menyeluruh yang hanya membaca. Tanda ⚠ perlu diperhatikan, ✗ sebaiknya segera ditangani.", Steps: HealthSteps},
		{ID: "disk-full", Label: "Disk penuh", Description: "\"No space left on device\", aplikasi gagal menulis file, database berhenti.",
			Intro: "Mencari partisi yang penuh, inode yang habis, file terhapus yang masih memakan tempat, dan apa yang aman dibersihkan.", Steps: DiskSteps},
		{ID: "slow", Label: "Server lambat", Description: "Respons lambat, SSH tersendat, load tinggi.",
			Intro: "Membandingkan beban dengan kapasitas: CPU, RAM & swap, antrean disk (IO), proses terberat, dan proses yang dimatikan karena RAM habis.", Steps: SlowSteps},
		{ID: "service", Label: "Service mati terus / crash loop", Description: "Aplikasi berhenti sendiri, restart berulang, atau gagal start setelah reboot.",
			Intro: "Mencari unit systemd yang gagal atau restart berulang, error terbaru di log, dan proses yang dimatikan kernel karena RAM habis.", Steps: ServiceSteps},
		{ID: "web", Label: "Aplikasi/web saya tidak bisa diakses", Description: "Browser timeout, connection refused, 502 Bad Gateway.",
			Open: "network:inbound"},
		{ID: "internet", Label: "Tidak bisa konek ke internet/host lain", Description: "apt update gagal, curl timeout, DNS tidak ketemu.",
			Open: "network:outbound"},
		{ID: "ssh", Label: "SSH: tidak bisa login / mau diamankan", Description: "Permission denied (publickey), password ditolak, atau ingin menutup login password.",
			Intro: "Memeriksa server SSH, izin file key milik kamu, dan setelan keamanan sshd.", Steps: SSHSteps},
		{ID: "update", Label: "Habis update, ada yang rusak", Description: "Setelah apt upgrade ada paket error, service gagal, atau perlu reboot.",
			Intro: "Melihat apa yang terakhir diubah apt, paket yang setengah terpasang, kebutuhan reboot, dan service yang gagal sejak boot.", Steps: UpdateSteps},
	}
}

func ok(summary string) check.Result { return check.Result{Status: check.OK, Summary: summary} }

func warn(summary, explain, next string) check.Result {
	return check.Result{Status: check.Warn, Summary: summary, Explain: explain, Next: next}
}

func fail(summary, explain, next string) check.Result {
	return check.Result{Status: check.Fail, Summary: summary, Explain: explain, Next: next}
}

func skip(summary string) check.Result { return check.Result{Status: check.Skip, Summary: summary} }

func listMax(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" (+%d lagi)", len(items)-n)
}

// --- Disk --------------------------------------------------------------------------------------------

func realMounts(env Env) ([]disk.Mount, error) {
	ms, err := disk.ReadMounts(env.ProcRoot, false)
	if err != nil {
		return nil, err
	}
	var out []disk.Mount
	seen := map[string]bool{}
	for _, m := range ms {
		// Snap memasang squashfs yang selalu 100% penuh; itu normal.
		if m.FSType == "squashfs" || m.Size == 0 || m.Err != nil || seen[m.Source] {
			continue
		}
		seen[m.Source] = true
		out = append(out, m)
	}
	return out, nil
}

func stepDiskUsage(env Env) check.Step {
	return check.Step{Title: "Ruang partisi", Equivalent: "df -h -x tmpfs -x squashfs", Run: func(context.Context) check.Result {
		ms, err := realMounts(env)
		if err != nil {
			return skip("Tidak bisa membaca daftar partisi: " + err.Error())
		}
		var full, tight []string
		maxPct, maxName := 0, ""
		for _, m := range ms {
			p := m.UsePercent()
			if p > maxPct {
				maxPct, maxName = p, m.Target
			}
			label := fmt.Sprintf("%s %d%% (sisa %s)", m.Target, p, shared.Bytes(m.Avail))
			switch {
			case p >= 95:
				full = append(full, label)
			case p >= 85:
				tight = append(tight, label)
			}
		}
		switch {
		case len(full) > 0:
			return fail("Hampir penuh: "+strings.Join(full, ", "),
				"Di atas 95% program mulai gagal menulis file (log, database, upload). Cari folder terbesar dan bersihkan dengan panduan di modul Disk.", "disk")
		case len(tight) > 0:
			return warn("Mulai sempit: "+strings.Join(tight, ", "), "Masih aman, tetapi rencanakan pembersihan sebelum menyentuh 95%.", "disk")
		}
		return ok(fmt.Sprintf("Semua partisi lega (tertinggi %s %d%%)", maxName, maxPct))
	}}
}

func stepInodes(env Env) check.Step {
	return check.Step{Title: "Inode (jumlah file)", Equivalent: "df -i", Run: func(context.Context) check.Result {
		ms, err := realMounts(env)
		if err != nil {
			return skip(err.Error())
		}
		var bad []string
		for _, m := range ms {
			if p := m.InodePercent(); p >= 90 {
				bad = append(bad, fmt.Sprintf("%s %d%%", m.Target, p))
			}
		}
		if len(bad) > 0 {
			return fail("Inode hampir habis: "+strings.Join(bad, ", "),
				"Disk bisa \"penuh\" walau masih ada ruang, karena jumlah file sudah maksimum. Biasanya ulah jutaan file kecil: cache, sesi PHP, atau antrean mail.", "disk")
		}
		return ok("Jumlah file masih jauh dari batas")
	}}
}

func stepDeletedOpen(env Env) check.Step {
	return check.Step{Title: "File terhapus yang masih dipakai proses", Equivalent: "sudo lsof +L1", Run: func(context.Context) check.Result {
		d, err := disk.FindDeletedOpen(env.ProcRoot)
		if err != nil {
			return skip(err.Error())
		}
		total := d.Total()
		note := ""
		if d.Denied > 0 {
			note = fmt.Sprintf(" (%d proses milik user lain tidak bisa diperiksa tanpa sudo)", d.Denied)
		}
		if total >= 500<<20 {
			var names []string
			for _, f := range d.Files {
				names = append(names, fmt.Sprintf("%s (%s, PID %d)", filepath.Base(f.Path), shared.Bytes(f.Size), f.PID))
			}
			return warn(fmt.Sprintf("%s masih terpakai oleh file yang sudah dihapus%s", shared.Bytes(total), note),
				"Ruang baru kembali setelah proses yang membukanya di-restart. Contoh: "+listMax(names, 3)+".", "disk")
		}
		return ok("Tidak ada yang signifikan" + note)
	}}
}

func stepCleanup(env Env) check.Step {
	return check.Step{Title: "Yang aman dibersihkan", Equivalent: "journalctl --disk-usage; du -sh /var/cache/apt/archives", Run: func(ctx context.Context) check.Result {
		cands := disk.FindCandidates(ctx, env.Runner, env.Cleanup)
		var total int64
		var parts []string
		sort.Slice(cands, func(a, b int) bool { return cands[a].Size > cands[b].Size })
		for _, c := range cands {
			if c.Available && c.Size > 0 {
				total += c.Size
				parts = append(parts, c.Label+" "+shared.Bytes(c.Size))
			}
		}
		if total >= 1<<30 {
			return warn("Sekitar "+shared.Bytes(total)+" bisa dibebaskan", listMax(parts, 4)+". Pilih dan jalankan dari menu bersih-bersih di modul Disk.", "disk")
		}
		if total > 0 {
			return ok("Sekitar " + shared.Bytes(total) + " bisa dibebaskan: " + listMax(parts, 3))
		}
		return ok("Tidak ada sisa besar yang bisa dibersihkan")
	}}
}

// DiskSteps untuk gejala "Disk penuh".
func DiskSteps(env Env) []check.Step {
	return []check.Step{stepDiskUsage(env), stepInodes(env), stepDeletedOpen(env), stepCleanup(env)}
}

// --- Resource ----------------------------------------------------------------------------------------

type sampleOnce struct {
	once sync.Once
	snap resource.Snapshot
	err  error
}

func (s *sampleOnce) get(ctx context.Context, env Env) (resource.Snapshot, error) {
	s.once.Do(func() { s.snap, s.err = env.Sample(ctx) })
	return s.snap, s.err
}

func insightResult(ins []resource.Insight, match func(resource.Insight) bool, okMsg string) check.Result {
	for _, in := range ins {
		// Insight tingkat Safe hanya edukasi (mis. "cache itu normal"), bukan temuan.
		if in.Level == risk.Safe || !match(in) {
			continue
		}
		explain := in.Detail
		if in.Next != "" {
			explain += " Saran: " + in.Next + "."
		}
		if in.Level == risk.Dangerous {
			return fail(in.Title, explain, "resource")
		}
		return warn(in.Title, explain, "resource")
	}
	return ok(okMsg)
}

// SlowSteps untuk gejala "Server lambat".
func SlowSteps(env Env) []check.Step {
	s := &sampleOnce{}
	has := func(words ...string) func(resource.Insight) bool {
		return func(in resource.Insight) bool {
			t := strings.ToLower(in.Title)
			for _, w := range words {
				if strings.Contains(t, w) {
					return true
				}
			}
			return false
		}
	}
	return []check.Step{
		{Title: "Beban CPU", Equivalent: "uptime; nproc", Run: func(ctx context.Context) check.Result {
			snap, err := s.get(ctx, env)
			if err != nil {
				return skip(err.Error())
			}
			return insightResult(resource.Analyze(snap), has("load", "cpu"),
				fmt.Sprintf("Load %.2f untuk %d core, CPU terpakai %.0f%%", snap.Load.One, snap.Cores, snap.CPUBusy))
		}},
		{Title: "RAM dan swap", Equivalent: "free -h", Run: func(ctx context.Context) check.Result {
			snap, err := s.get(ctx, env)
			if err != nil {
				return skip(err.Error())
			}
			m := snap.Memory
			return insightResult(resource.Analyze(snap), has("ram", "swap", "memori"),
				fmt.Sprintf("RAM tersedia %s dari %s", shared.Bytes(m.AvailableKB<<10), shared.Bytes(m.TotalKB<<10)))
		}},
		{Title: "Antrean disk (IO)", Equivalent: "cat /proc/pressure/io", Run: func(ctx context.Context) check.Result {
			snap, err := s.get(ctx, env)
			if err != nil {
				return skip(err.Error())
			}
			if !snap.PSI.IO.Available {
				return skip("Kernel tidak menyediakan data tekanan IO")
			}
			return insightResult(resource.Analyze(snap), has("io", "disk"), fmt.Sprintf("Tekanan IO rendah (%.1f%% waktu menunggu disk)", snap.PSI.IO.Avg60))
		}},
		{Title: "Proses terberat", Equivalent: "top -o %CPU", Run: func(ctx context.Context) check.Result {
			snap, err := s.get(ctx, env)
			if err != nil {
				return skip(err.Error())
			}
			top := resource.SortBy(snap.Procs, false, 3)
			var names []string
			for _, p := range top {
				names = append(names, fmt.Sprintf("%s %.0f%%", p.Name, p.CPU))
			}
			mem := resource.SortBy(snap.Procs, true, 1)
			summary := "CPU: " + strings.Join(names, ", ")
			if len(mem) > 0 {
				summary += fmt.Sprintf(" · RAM terbesar: %s %s", mem[0].Name, shared.Bytes(mem[0].RSSKB<<10))
			}
			if len(top) > 0 && top[0].CPU >= 90 {
				return warn(summary, "Satu proses memakai hampir satu core penuh. Bila tidak wajar (mis. loop tak berujung), restart service-nya.", "resource")
			}
			return ok(summary)
		}},
		{Title: "Proses dimatikan karena RAM habis", Equivalent: "journalctl -k -b --grep 'Out of memory'", Run: func(ctx context.Context) check.Result {
			return oomResult(ctx, env)
		}},
	}
}

func oomResult(ctx context.Context, env Env) check.Result {
	events, err := resource.ReadOOM(ctx, env.Runner)
	if err != nil {
		return skip("Log kernel tidak terbaca (butuh grup adm atau sudo)")
	}
	if len(events) == 0 {
		return ok("Tidak ada sejak boot")
	}
	var names []string
	for _, e := range events {
		names = append(names, e.Process)
	}
	return fail(fmt.Sprintf("%d kali sejak boot: %s", len(events), listMax(names, 4)),
		"Kernel mematikan proses karena RAM habis. Tambah swap (modul Disk), batasi memori aplikasi, atau tambah RAM.", "resource")
}

// --- Service -----------------------------------------------------------------------------------------

func stepFailedUnits(env Env) check.Step {
	return check.Step{Title: "Service yang gagal", Equivalent: "systemctl --failed", Run: func(ctx context.Context) check.Result {
		units, err := systemd.List(ctx, env.Runner, false)
		if err != nil {
			return skip("systemctl tidak bisa dibaca: " + err.Error())
		}
		var failed []string
		for _, u := range units {
			if u.Failed() {
				failed = append(failed, strings.TrimSuffix(u.Name, ".service"))
			}
		}
		if len(failed) > 0 {
			return fail(fmt.Sprintf("%d unit gagal: %s", len(failed), listMax(failed, 5)),
				"Buka modul Service, pilih unit, lalu lihat lognya untuk alasan gagal.", "services")
		}
		return ok("Tidak ada unit yang gagal")
	}}
}

// ServiceSteps untuk gejala "Service mati terus".
func ServiceSteps(env Env) []check.Step {
	return []check.Step{
		stepFailedUnits(env),
		{Title: "Service yang restart berulang", Equivalent: "systemctl show -p NRestarts UNIT", Run: func(ctx context.Context) check.Result {
			units, err := systemd.List(ctx, env.Runner, false)
			if err != nil {
				return skip(err.Error())
			}
			var looping []string
			for _, u := range units {
				if !strings.HasSuffix(u.Name, ".service") || u.Active == "inactive" {
					continue
				}
				d, err := systemd.Show(ctx, env.Runner, u.Name, false)
				if err == nil && (d.CrashLooping() || d.Int("NRestarts") >= 3) {
					looping = append(looping, fmt.Sprintf("%s (%d×)", strings.TrimSuffix(u.Name, ".service"), d.Int("NRestarts")))
				}
			}
			if len(looping) > 0 {
				return fail("Restart berulang: "+listMax(looping, 5),
					"systemd terus menyalakan ulang karena programnya keluar dengan error. Penyebab ada di log unit tersebut (modul Log → per service).", "logs")
			}
			return ok("Tidak ada service yang restart berulang")
		}},
		{Title: "Error terbaru di log (boot ini)", Equivalent: "journalctl -b -p err -n 50", Run: func(ctx context.Context) check.Result {
			res, err := logs.Read(ctx, env.Runner, logs.Query{Boot: logs.BootPtr(0), Priority: 3, Lines: 200})
			if err != nil {
				return skip(err.Error())
			}
			count := map[string]int{}
			for _, e := range res.Entries {
				count[e.Source()]++
			}
			if len(count) == 0 {
				return ok("Tidak ada error sejak boot")
			}
			var srcs []string
			for s := range count {
				srcs = append(srcs, s)
			}
			sort.Slice(srcs, func(a, b int) bool { return count[srcs[a]] > count[srcs[b]] })
			var parts []string
			for _, s := range srcs {
				parts = append(parts, fmt.Sprintf("%s %d", s, count[s]))
			}
			note := ""
			if res.Limited {
				note = " Sebagian log sistem tidak terlihat oleh user ini."
			}
			return warn(fmt.Sprintf("%d error dari: %s", len(res.Entries), listMax(parts, 4)),
				"Tidak semua error berarti masalah, tetapi yang berulang dari service yang kamu pakai patut dibaca."+note, "logs")
		}},
		{Title: "Proses dimatikan karena RAM habis", Equivalent: "journalctl -k -b --grep 'Out of memory'", Run: func(ctx context.Context) check.Result {
			return oomResult(ctx, env)
		}},
	}
}

// --- SSH ---------------------------------------------------------------------------------------------

// SSHSteps untuk gejala SSH.
func SSHSteps(env Env) []check.Step {
	return []check.Step{
		{Title: "Server SSH terpasang dan berjalan", Equivalent: "systemctl status ssh", Run: func(ctx context.Context) check.Result {
			if _, err := os.Stat(env.SSHDBinary); err != nil {
				return fail("openssh-server belum terpasang", "Server ini tidak bisa dimasuki lewat SSH. Pasang dari modul Paket: openssh-server.", "packages")
			}
			for _, unit := range []string{"ssh.socket", "ssh.service"} {
				out, _, _ := env.Runner.Capture(ctx, run.Command{Argv: []string{"systemctl", "is-active", unit}})
				if strings.TrimSpace(out) == "active" {
					return ok(unit + " aktif")
				}
			}
			return fail("SSH terpasang tetapi tidak aktif", "Nyalakan ssh.socket atau ssh.service dari modul Service.", "services")
		}},
		{Title: "Port SSH mendengarkan", Equivalent: "ss -tlnp | grep ssh", Run: func(context.Context) check.Result {
			if _, err := os.Stat(env.SSHDBinary); err != nil {
				return skip("openssh-server tidak terpasang")
			}
			cfg := users.ReadSSHD(env.SSHDConfig, env.SSHDBinary)
			port := cfg.Values["port"]
			snap, err := ports.ReadListeners(env.ProcRoot)
			if err != nil {
				return skip(err.Error())
			}
			for _, l := range snap.Listeners {
				if fmt.Sprint(l.Local.Port()) == port && l.Proto == ports.TCP {
					if l.Scope() == ports.ScopeLoopback {
						return fail("Port "+port+" hanya mendengarkan di localhost", "Koneksi dari luar ditolak. Periksa ListenAddress di sshd_config.", "users")
					}
					return ok("Port " + port + " terbuka untuk koneksi masuk")
				}
			}
			return warn("Tidak ada yang mendengarkan di port "+port, "Bila memakai ssh.socket, port baru dibuka setelah socket aktif. Bila port di konfigurasi baru diganti, restart ssh.socket.", "network")
		}},
		{Title: "Izin file SSH milik " + env.Username, Equivalent: "ls -ld ~ ~/.ssh ~/.ssh/authorized_keys", Run: func(context.Context) check.Result {
			probs := users.CheckPermissions(env.Home, env.UID)
			if len(probs) > 0 {
				var d []string
				for _, p := range probs {
					d = append(d, p.Path+": "+p.Detail)
				}
				return fail("Izin terlalu longgar: "+listMax(d, 3),
					"sshd menolak key (\"Permission denied (publickey)\") bila home, ~/.ssh, atau authorized_keys bisa ditulis orang lain. Perbaiki dari modul User & SSH.", "users")
			}
			keys, err := users.ReadAuthorizedKeys(env.Home)
			if err != nil || len(keys) == 0 {
				return warn("Belum ada SSH key di ~/.ssh/authorized_keys", "Tanpa key, login hanya bisa dengan password. Tambahkan public key dari modul User & SSH.", "users")
			}
			return ok(fmt.Sprintf("Izin benar, %d key terdaftar", len(keys)))
		}},
		{Title: "Setelan keamanan sshd", Equivalent: "sudo sshd -T | grep -Ei 'permitroot|passwordauth'", Run: func(context.Context) check.Result {
			return sshAudit(env)
		}},
	}
}

func sshAudit(env Env) check.Result {
	if _, err := os.Stat(env.SSHDBinary); err != nil {
		return skip("openssh-server tidak terpasang")
	}
	cfg := users.ReadSSHD(env.SSHDConfig, env.SSHDBinary)
	var bad, cautions []string
	for _, f := range cfg.Audit() {
		if f.OK {
			continue
		}
		label := fmt.Sprintf("%s = %s", f.Key, f.Value)
		if f.Level == risk.Dangerous {
			bad = append(bad, label)
		} else if f.Level == risk.Caution {
			cautions = append(cautions, label)
		}
	}
	switch {
	case len(bad) > 0:
		return fail("Berbahaya: "+strings.Join(bad, ", "), "Perbaiki dari modul User & SSH → amankan SSH (dengan pengaman anti-terkunci).", "users")
	case len(cautions) > 0:
		return warn("Bisa diperketat: "+strings.Join(cautions, ", "), "Login password di server yang terbuka ke internet rawan ditebak. Pasang key dulu, lalu matikan password dari modul User & SSH.", "users")
	}
	return ok("Setelan utama sudah aman")
}

// --- Paket -------------------------------------------------------------------------------------------

// UpdateSteps untuk gejala "Habis update, ada yang rusak".
func UpdateSteps(env Env) []check.Step {
	return []check.Step{
		{Title: "Perubahan apt terakhir", Equivalent: "tail -n 40 /var/log/apt/history.log", Run: func(context.Context) check.Result {
			hist, err := packages.ReadHistory(env.Packages.History, 3)
			if err != nil || len(hist) == 0 {
				return skip("Riwayat apt tidak ditemukan")
			}
			last := hist[0]
			return ok(fmt.Sprintf("%s (%s)", last.Summary(), shared.Ago(last.Start, env.Now())))
		}},
		{Title: "Paket setengah terpasang", Equivalent: "sudo dpkg --audit", Run: func(ctx context.Context) check.Result {
			st := packages.ReadStatus(ctx, env.Runner, env.Packages, env.Now())
			if st.LockHolder != nil {
				return warn("apt sedang dipakai "+st.LockHolder.Name, "Tunggu sampai selesai sebelum menilai ada yang rusak.", "packages")
			}
			if strings.TrimSpace(st.Broken) != "" {
				return fail("Ada paket yang belum selesai dikonfigurasi", firstLines(st.Broken, 2)+" Jalankan perbaikan paket rusak dari modul Paket.", "packages")
			}
			return ok("Database paket sehat")
		}},
		stepReboot(env),
		stepFailedUnits(env),
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}

func stepReboot(env Env) check.Step {
	return check.Step{Title: "Perlu reboot", Equivalent: "cat /var/run/reboot-required.pkgs", Run: func(context.Context) check.Result {
		if _, err := os.Stat(env.Packages.RebootRequired); err != nil {
			return ok("Tidak perlu reboot")
		}
		pkgs, _ := os.ReadFile(env.Packages.RebootPkgs)
		names := strings.Fields(string(pkgs))
		return warn("Reboot diperlukan", "Update ("+listMax(names, 4)+") baru berlaku penuh setelah reboot, terutama kernel dan library inti. Jadwalkan di jam sepi.", "packages")
	}}
}

// --- Kesehatan umum ----------------------------------------------------------------------------------

// HealthSteps untuk "Cek kesehatan umum".
func HealthSteps(env Env) []check.Step {
	s := &sampleOnce{}
	return []check.Step{
		stepDiskUsage(env),
		{Title: "RAM", Equivalent: "free -h", Run: func(ctx context.Context) check.Result {
			snap, err := s.get(ctx, env)
			if err != nil {
				return skip(err.Error())
			}
			m := snap.Memory
			avail := 100 * float64(m.AvailableKB) / float64(max(m.TotalKB, 1))
			summary := fmt.Sprintf("Tersedia %s dari %s (%.0f%%)", shared.Bytes(m.AvailableKB<<10), shared.Bytes(m.TotalKB<<10), avail)
			switch {
			case avail < 10:
				return fail(summary, "RAM hampir habis; kernel akan mematikan proses bila terus begini.", "resource")
			case avail < 20:
				return warn(summary, "RAM menipis. Lihat proses terbesar di modul Resource.", "resource")
			}
			return ok(summary)
		}},
		{Title: "Update keamanan", Equivalent: "apt list --upgradable", Run: func(ctx context.Context) check.Result {
			st := packages.ReadStatus(ctx, env.Runner, env.Packages, env.Now())
			sec := 0
			for _, u := range st.Upgradable {
				if u.Security {
					sec++
				}
			}
			age := ""
			if st.ListAge > 7*24*time.Hour {
				age = fmt.Sprintf(" Daftar paket terakhir diperbarui %s lalu; jalankan apt update dulu.", shared.Duration(st.ListAge))
			}
			switch {
			case sec > 0:
				return warn(fmt.Sprintf("%d update keamanan tertunda (dari %d update)", sec, len(st.Upgradable)), "Pasang dari modul Paket."+age, "packages")
			case age != "":
				return warn("Daftar paket sudah lama", strings.TrimSpace(age), "packages")
			}
			return ok(fmt.Sprintf("Tidak ada update keamanan tertunda (%d update biasa)", len(st.Upgradable)))
		}},
		stepReboot(env),
		stepFailedUnits(env),
		{Title: "Jadwal bermasalah", Equivalent: "systemctl list-timers --all", Run: func(ctx context.Context) check.Result {
			jobs, err := schedule.ReadAll(ctx, env.Runner, env.Schedule, env.Username, env.Now())
			if err != nil && len(jobs) == 0 {
				return skip(err.Error())
			}
			var bad []string
			for _, j := range jobs {
				if j.Problem != "" {
					bad = append(bad, j.Name+": "+j.Problem)
				}
			}
			if len(bad) > 0 {
				return warn(fmt.Sprintf("%d jadwal bermasalah", len(bad)), listMax(bad, 3), "schedule")
			}
			return ok(fmt.Sprintf("%d jadwal, tidak ada masalah terdeteksi", len(jobs)))
		}},
		{Title: "Sertifikat HTTPS", Equivalent: "sudo certbot certificates", Run: func(context.Context) check.Result {
			return certResult(env)
		}},
		{Title: "Keamanan SSH", Equivalent: "sudo sshd -T", Run: func(context.Context) check.Result {
			return sshAudit(env)
		}},
	}
}

func certResult(env Env) check.Result {
	entries, err := os.ReadDir(env.CertLive)
	if err != nil {
		if os.IsPermission(err) {
			return skip("Folder sertifikat hanya bisa dibaca root")
		}
		return skip("Tidak ada sertifikat Let's Encrypt di server ini")
	}
	var expiring, expired []string
	checked := 0
	now := env.Now()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		res := tlscheck.CheckFile(filepath.Join(env.CertLive, e.Name(), "cert.pem"), now)
		if res.Err != nil {
			continue
		}
		checked++
		days := res.Cert.DaysLeft(now)
		switch {
		case days < 0:
			expired = append(expired, e.Name())
		case days < 14:
			expiring = append(expiring, fmt.Sprintf("%s (%d hari)", e.Name(), days))
		}
	}
	switch {
	case len(expired) > 0:
		return fail("Kedaluwarsa: "+strings.Join(expired, ", "), "Pengunjung melihat peringatan keamanan. Periksa renew otomatis: sudo certbot renew --dry-run (modul Web & TLS).", "web")
	case len(expiring) > 0:
		return warn("Segera kedaluwarsa: "+strings.Join(expiring, ", "), "certbot biasanya memperbarui 30 hari sebelum habis; bila tinggal <14 hari, renew otomatis kemungkinan gagal.", "web")
	case checked == 0:
		return skip("Sertifikat tidak bisa dibaca (butuh root)")
	}
	return ok(fmt.Sprintf("%d sertifikat masih berlaku lebih dari 14 hari", checked))
}

// --- Ringkasan home ----------------------------------------------------------------------------------

// Quick adalah pemeriksaan kilat (tanpa jaringan dan tanpa apt) untuk baris ringkas di menu utama.
// Mengembalikan temuan yang perlu perhatian; kosong berarti semua baik.
func Quick(ctx context.Context, env Env) []string {
	var out []string
	if ms, err := realMounts(env); err == nil {
		for _, m := range ms {
			if p := m.UsePercent(); p >= 90 {
				out = append(out, fmt.Sprintf("disk %s %d%%", m.Target, p))
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(env.ProcRoot, "meminfo")); err == nil {
		m := resource.ParseMeminfo(string(data))
		if m.TotalKB > 0 && m.AvailableKB*10 < m.TotalKB {
			out = append(out, fmt.Sprintf("RAM tersisa %d%%", 100*m.AvailableKB/m.TotalKB))
		}
	}
	if units, err := systemd.List(ctx, env.Runner, false); err == nil {
		n := 0
		for _, u := range units {
			if u.Failed() {
				n++
			}
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%d service gagal", n))
		}
	}
	if _, err := os.Stat(env.Packages.RebootRequired); err == nil {
		out = append(out, "perlu reboot")
	}
	return out
}
