package safety

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// eachCommand memanggil fn untuk setiap langkah (termasuk langkah validasi) di seluruh katalog.
func eachCommand(t *testing.T, fn func(t *testing.T, name string, c run.Command, isCheck bool)) {
	t.Helper()
	for _, e := range catalog() {
		if e.plan.Title == "" {
			t.Errorf("%s: Plan tanpa judul", e.name)
		}
		if len(e.plan.Steps) == 0 {
			t.Errorf("%s: Plan tanpa langkah", e.name)
		}
		for i, s := range e.plan.Sequence() {
			fn(t, e.name+"#"+string(rune('1'+i)), s.Command, s.IsCheck)
		}
	}
}

// Setiap command harus bisa dipahami user sebelum disetujui: ada judul dan penjelasan.
func TestSetiapCommandDijelaskan(t *testing.T) {
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		if len(c.Argv) == 0 {
			t.Errorf("%s: argv kosong", name)
			return
		}
		if strings.TrimSpace(c.Title) == "" {
			t.Errorf("%s: %s tanpa judul", name, c.Preview(false))
		}
		if len(c.Explain) == 0 {
			t.Errorf("%s: %s tanpa penjelasan", name, c.Preview(false))
		}
		for _, l := range c.Explain {
			if strings.TrimSpace(l.Token) == "" || strings.TrimSpace(l.Meaning) == "" {
				t.Errorf("%s: baris penjelasan kosong %+v", name, l)
			}
		}
	})
}

// sudo hanya boleh ditambahkan lapisan eksekusi lewat NeedsRoot, supaya tampilan, riwayat, dan
// deteksi "sudo minta password" konsisten, dan user yang sudah root tidak menjalankan sudo.
func TestHakRootHanyaLewatNeedsRoot(t *testing.T) {
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		switch filepath.Base(c.Argv[0]) {
		case "sudo", "su", "pkexec", "doas":
			t.Errorf("%s: argv diawali %s; pakai NeedsRoot", name, c.Argv[0])
		case "runuser":
			if !c.NeedsRoot {
				t.Errorf("%s: runuser hanya bisa dipakai root", name)
			}
		}
		if got := c.ExecArgv(true, true); got[0] == "sudo" {
			t.Errorf("%s: sebagai root tidak boleh memakai sudo: %v", name, got)
		}
		if c.NeedsRoot && c.Preview(false) != "sudo "+c.Preview(true) {
			t.Errorf("%s: preview non-root harus tepat diawali sudo: %q", name, c.Preview(false))
		}
	})
}

// Menulis atau menghapus di lokasi sistem wajib lewat root. Tanpa ini command gagal di tengah Plan,
// atau lebih buruk: berhasil sebagian.
func TestLokasiSistemButuhRoot(t *testing.T) {
	writers := map[string]bool{"install": true, "tee": true, "ln": true, "rm": true, "cp": true, "mv": true, "mkdir": true,
		"chmod": true, "chown": true, "fallocate": true, "mkswap": true, "swapon": true, "truncate": true, "sed": true}
	system := []string{"/etc/", "/usr/", "/var/", "/boot/", "/root/", "/swapfile", "/opt/"}
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		if !writers[filepath.Base(c.Argv[0])] {
			return
		}
		for _, a := range c.Argv[1:] {
			for _, prefix := range system {
				if strings.HasPrefix(a, prefix) && !c.NeedsRoot {
					t.Errorf("%s: %s menyentuh %s tanpa NeedsRoot", name, c.Preview(false), a)
				}
			}
		}
	})
}

// rule menetapkan tingkat risiko minimum untuk pola command yang merusak atau bisa memutus akses.
type rule struct {
	why   string
	match func(argv []string) bool
	min   risk.Level
}

func has(argv []string, words ...string) bool {
	joined := " " + strings.Join(argv, " ") + " "
	for _, w := range words {
		if !strings.Contains(joined, " "+w+" ") {
			return false
		}
	}
	return true
}

// effective mengembalikan argv program yang sebenarnya dijalankan: untuk runuser (dipakai modul
// PostgreSQL agar perintah berjalan sebagai user postgres), yang dinilai adalah bagian setelah "--".
func effective(argv []string) []string {
	if filepath.Base(argv[0]) != "runuser" {
		return argv
	}
	for i, a := range argv {
		if a == "--" && i+1 < len(argv) {
			return argv[i+1:]
		}
	}
	return argv
}

func prog(argv []string, name string) bool { return filepath.Base(effective(argv)[0]) == name }

var riskRules = []rule{
	{"kill -KILL tidak memberi kesempatan menyimpan data", func(a []string) bool { return prog(a, "kill") && has(a, "-KILL") }, risk.Dangerous},
	{"kill mengakhiri proses", func(a []string) bool { return prog(a, "kill") }, risk.Caution},
	{"menghapus user", func(a []string) bool { return prog(a, "deluser") || prog(a, "userdel") }, risk.Dangerous},
	{"memberi/mencabut hak sudo", func(a []string) bool {
		return (prog(a, "usermod") && has(a, "sudo")) || (prog(a, "gpasswd") && has(a, "sudo"))
	}, risk.Dangerous},
	{"mengunci akun lewat tanggal kedaluwarsa", func(a []string) bool { return prog(a, "usermod") && has(a, "--expiredate") }, risk.Dangerous},
	{"passwd -l mengunci login", func(a []string) bool { return prog(a, "passwd") }, risk.Caution},
	{"menyalakan/mematikan/mereset firewall bisa memutus SSH", func(a []string) bool {
		return prog(a, "ufw") && (has(a, "enable") || has(a, "disable") || has(a, "reset"))
	}, risk.Dangerous},
	{"menghapus aturan firewall", func(a []string) bool { return prog(a, "ufw") && has(a, "delete") }, risk.Caution},
	{"mengubah aturan firewall", func(a []string) bool { return prog(a, "ufw") && (has(a, "allow") || has(a, "deny") || has(a, "limit")) }, risk.Caution},
	{"reboot/poweroff memutus semua layanan", func(a []string) bool {
		return prog(a, "reboot") || prog(a, "poweroff") || prog(a, "shutdown") || (prog(a, "systemctl") && (has(a, "reboot") || has(a, "poweroff")))
	}, risk.Dangerous},
	{"menghentikan/menonaktifkan SSH bisa mengunci user di luar", func(a []string) bool {
		return prog(a, "systemctl") && (has(a, "stop") || has(a, "disable")) && (has(a, "ssh.service") || has(a, "sshd.service") || has(a, "ssh.socket"))
	}, risk.Dangerous},
	{"menghentikan service", func(a []string) bool { return prog(a, "systemctl") && (has(a, "stop") || has(a, "disable")) }, risk.Caution},
	{"restart ssh.socket saat port/akses berubah", func(a []string) bool { return prog(a, "systemctl") && has(a, "restart", "ssh.socket") }, risk.Dangerous},
	{"mengubah konfigurasi sshd", func(a []string) bool {
		for _, x := range a {
			if strings.HasPrefix(x, "/etc/ssh/") && (prog(a, "install") || prog(a, "tee") || prog(a, "rm")) {
				return true
			}
		}
		return false
	}, risk.Dangerous},
	{"menimpa authorized_keys bisa menghapus akses", func(a []string) bool {
		return prog(a, "install") && strings.HasSuffix(a[len(a)-1], "authorized_keys")
	}, risk.Dangerous},
	{"apt purge menghapus konfigurasi", func(a []string) bool { return prog(a, "apt-get") && has(a, "purge") }, risk.Dangerous},
	{"apt remove", func(a []string) bool { return prog(a, "apt-get") && has(a, "remove") }, risk.Caution},
	{"PPA bisa memasang apa pun lewat update", func(a []string) bool { return prog(a, "add-apt-repository") }, risk.Dangerous},
	{"rm menghapus file", func(a []string) bool { return prog(a, "rm") }, risk.Caution},
	{"docker rm -f menghentikan paksa lalu menghapus", func(a []string) bool { return prog(a, "docker") && has(a, "rm", "-f") }, risk.Dangerous},
	{"prune --volumes menghapus data", func(a []string) bool { return prog(a, "docker") && has(a, "--volumes") }, risk.Dangerous},
	{"docker rm/rmi/prune/stop", func(a []string) bool {
		return prog(a, "docker") && (has(a, "rm") || has(a, "rmi") || has(a, "prune") || has(a, "stop"))
	}, risk.Caution},
	{"menjalankan script bebas lewat shell", func(a []string) bool {
		return has(a, "bash", "-c") || has(a, "sh", "-c")
	}, risk.Caution},
	{"mengubah fstab bisa membuat gagal boot", func(a []string) bool { return prog(a, "tee") && has(a, "/etc/fstab") }, risk.Caution},
	{"dropdb/dropuser menghapus database atau role beserta isinya", func(a []string) bool {
		return prog(a, "dropdb") || prog(a, "dropuser")
	}, risk.Dangerous},
	{"pg_restore --clean menimpa tabel yang ada", func(a []string) bool { return prog(a, "pg_restore") && has(a, "--clean") }, risk.Dangerous},
	{"mengubah pg_hba.conf menentukan siapa boleh masuk ke database", func(a []string) bool {
		for _, x := range a {
			if strings.HasSuffix(x, "pg_hba.conf") && (prog(a, "install") || prog(a, "tee")) {
				return true
			}
		}
		return false
	}, risk.Dangerous},
	{"restart cluster database memutus semua koneksi aplikasi", func(a []string) bool {
		if !prog(a, "systemctl") || !has(a, "restart") {
			return false
		}
		for _, x := range a {
			if strings.HasPrefix(x, "postgresql@") {
				return true
			}
		}
		return false
	}, risk.Dangerous},
	{"restart daemon docker menghentikan semua container", func(a []string) bool {
		return prog(a, "systemctl") && has(a, "restart", "docker")
	}, risk.Dangerous},
	{"docker volume rm/prune menghapus data yang tidak bisa dikembalikan", func(a []string) bool {
		return prog(a, "docker") && has(a, "volume") && (has(a, "rm") || has(a, "prune"))
	}, risk.Dangerous},
	{"docker load menjalankan image dari sumber luar", func(a []string) bool { return prog(a, "docker") && has(a, "load") }, risk.Caution},
	{"docker push mengunggah image ke registry", func(a []string) bool { return prog(a, "docker") && has(a, "push") }, risk.Caution},
	{"docker login menyimpan kredensial di disk", func(a []string) bool { return prog(a, "docker") && has(a, "login") }, risk.Caution},
	{"menghapus log journal", func(a []string) bool {
		return prog(a, "journalctl") && strings.Contains(strings.Join(a, " "), "--vacuum")
	}, risk.Caution},
}

// Command yang merusak atau bisa memutus akses harus membawa label risiko yang tepat, karena label itu
// yang memicu peringatan dan konfirmasi dua kali di layar.
func TestRisikoCommandBerbahaya(t *testing.T) {
	matched := map[string]bool{}
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		for _, r := range riskRules {
			if r.match(c.Argv) {
				matched[r.why] = true
				if c.Risk < r.min {
					t.Errorf("%s: %q risiko %d, minimal %d (%s)", name, c.Preview(false), c.Risk, r.min, r.why)
				}
			}
		}
	})
	// Aturan yang tidak pernah cocok berarti katalog tidak lagi mencakup kasusnya (atau aturannya salah).
	for _, r := range riskRules {
		if !matched[r.why] {
			t.Errorf("aturan risiko tidak pernah diuji oleh katalog: %s", r.why)
		}
	}
}

// Plan yang berisi langkah berbahaya harus terlihat berbahaya di layar konfirmasi.
func TestRisikoPlanMengikutiLangkahTerberat(t *testing.T) {
	for _, e := range catalog() {
		max := risk.Safe
		for _, s := range e.plan.Steps {
			if s.Risk > max {
				max = s.Risk
			}
		}
		if got := e.plan.Risk(); got != max {
			t.Errorf("%s: risiko plan %d, langkah terberat %d", e.name, got, max)
		}
	}
}

// Input user (nama paket, user, domain, image, ...) tidak boleh sampai ke interpreter shell. Satu-satunya
// pengecualian adalah "jalankan cron sekarang", yang memang menjalankan baris command dari file cron.
func TestInputTidakMasukShell(t *testing.T) {
	shells := map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true}
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		for i, a := range c.Argv {
			if !shells[filepath.Base(a)] || i+2 >= len(c.Argv)+1 {
				continue
			}
			for j := i + 1; j < len(c.Argv)-1; j++ {
				if c.Argv[j] == "-c" && strings.Contains(c.Argv[j+1], "ubt-pwned") && !strings.HasPrefix(name, "schedule.RunNowCron") {
					t.Errorf("%s: input user masuk ke skrip shell: %q", name, c.Argv[j+1])
				}
			}
		}
		for _, a := range c.Argv {
			// Pemecahan ala shell (per spasi) akan menghasilkan argumen yang persis sama dengan potongannya.
			for _, piece := range strings.Fields(evil) {
				if a == piece {
					t.Errorf("%s: input jahat terpecah menjadi beberapa argumen: %q", name, c.Argv)
				}
			}
		}
	})
}

// shellWords mem-parse satu baris shell sederhana (spasi, 'kutip tunggal', "kutip ganda", dan \escape)
// menjadi argumen, cukup untuk memeriksa hasil QuoteShell.
func shellWords(t *testing.T, s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == ' ':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		case ch == '\'':
			inWord = true
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				t.Fatalf("kutip tunggal tidak ditutup: %q", s)
			}
			cur.WriteString(s[i+1 : i+1+end])
			i += end + 1
		case ch == '\\' && i+1 < len(s):
			inWord = true
			cur.WriteByte(s[i+1])
			i++
		case strings.IndexByte("\"$`;&|<>()*?[]{}~#!\n\t", ch) >= 0:
			t.Fatalf("karakter shell %q tidak dikutip di %q", ch, s)
		default:
			inWord = true
			cur.WriteByte(ch)
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// Command yang ditampilkan dan bisa disalin user harus identik dengan yang dijalankan ubt.
func TestPreviewSamaDenganYangDijalankan(t *testing.T) {
	var previews []string
	var argvs [][]string
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		for _, root := range []bool{false, true} {
			got := shellWords(t, c.Preview(root))
			if want := c.DisplayArgv(root); !reflect.DeepEqual(got, want) {
				t.Errorf("%s: preview %q dibaca shell sebagai %q, bukan %q", name, c.Preview(root), got, want)
			}
		}
		previews = append(previews, c.Preview(false))
		argvs = append(argvs, c.DisplayArgv(false))
	})

	// Silang-periksa dengan bash sungguhan: printf hanya mencetak argumen, tidak menjalankan apa pun
	// kecuali quoting-nya rusak (dan justru itu yang dideteksi lewat file penanda).
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash tidak tersedia")
	}
	marker := "/tmp/ubt-pwned"
	os.Remove(marker)
	var script strings.Builder
	for _, p := range previews {
		script.WriteString("printf '%s\\037' " + p + "; printf '\\036'\n")
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-c", script.String())
	cmd.Env = []string{"PATH=/nonexistent"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash gagal: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		os.Remove(marker)
		t.Fatal("bash menjalankan input jahat: quoting bocor")
	}
	records := bytes.Split(bytes.TrimSuffix(out, []byte("\x1e")), []byte("\x1e"))
	if len(records) != len(argvs) {
		t.Fatalf("jumlah record bash %d, ingin %d", len(records), len(argvs))
	}
	for i, rec := range records {
		got := strings.Split(strings.TrimSuffix(string(rec), "\x1f"), "\x1f")
		if !reflect.DeepEqual(got, argvs[i]) {
			t.Errorf("bash membaca %q sebagai %q, ingin %q", previews[i], got, argvs[i])
		}
	}
}

// Script yang bisa disalin (termasuk isi file lewat heredoc) harus memberikan stdin yang sama persis.
func TestScriptHeredocUtuh(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash tidak tersedia")
	}
	checked := 0
	eachCommand(t, func(t *testing.T, name string, c run.Command, _ bool) {
		if c.Stdin == "" || c.Sensitive {
			return
		}
		// Ganti program dengan cat supaya script hanya mengembalikan stdin yang akan diterima.
		probe := run.Command{Argv: []string{"cat"}, Stdin: c.Stdin}
		out, err := exec.Command(bash, "--noprofile", "--norc", "-c", probe.Script(true)).Output()
		if err != nil {
			t.Errorf("%s: script gagal: %v", name, err)
			return
		}
		want := c.Stdin
		if !strings.HasSuffix(want, "\n") {
			want += "\n"
		}
		if string(out) != want {
			t.Errorf("%s: isi heredoc berubah:\n%q\n%q", name, out, want)
		}
		checked++
	})
	if checked == 0 {
		t.Error("katalog tidak punya command dengan stdin")
	}
}
