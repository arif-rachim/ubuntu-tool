package postgres

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Pilihan siapa yang boleh menyambung ke server database.
const (
	ListenLocal    = "local"    // hanya dari server ini (bawaan Ubuntu)
	ListenSpecific = "specific" // dari alamat tertentu
	ListenAll      = "all"      // dari mana saja yang bisa mencapai port 5432
)

// ValidCIDR memeriksa alamat atau rentang alamat untuk pg_hba: 10.0.0.5/32 atau 192.168.1.0/24.
func ValidCIDR(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("alamat wajib diisi")
	}
	if _, err := netip.ParsePrefix(s); err == nil {
		return nil
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		bits := 32
		if addr.Is6() {
			bits = 128
		}
		return fmt.Errorf("sertakan panjang prefix: %s/%d untuk satu alamat saja", s, bits)
	}
	return errors.New("format: ALAMAT/PREFIX, mis. 10.8.0.4/32 (satu server) atau 192.168.1.0/24 (satu jaringan)")
}

// ListenAddressValue mengembalikan nilai listen_addresses untuk pilihan akses.
func ListenAddressValue(mode string) string {
	if mode == ListenLocal {
		return "localhost"
	}
	return "*"
}

// ListenConf merender berkas conf.d milik ubt untuk listen_addresses.
func ListenConf(mode string) string {
	value := ListenAddressValue(mode)
	why := "hanya menerima koneksi dari server ini"
	if value == "*" {
		why = "menerima koneksi dari jaringan; siapa yang benar-benar boleh masuk ditentukan pg_hba.conf dan firewall"
	}
	return "# Dibuat oleh ubt — " + why + ".\nlisten_addresses = '" + value + "'\n"
}

// HBALine merender satu baris pg_hba.conf.
func HBALine(db, user, cidr, method string) string {
	kind := "host"
	if cidr == "" {
		kind = "local"
	}
	return fmt.Sprintf("%-7s %-16s %-16s %-20s %s", kind, db, user, cidr, method)
}

// RemoteAccessPlan membuka akses dari jaringan: listen_addresses + satu aturan pg_hba + restart.
// Urutannya sengaja: aturan siapa yang boleh masuk ditulis DULU, baru port dibuka.
func RemoteAccessPlan(cl Cluster, mode, db, user, cidr string) run.Plan {
	listenPath := cl.DropIn(DropInListen)
	line := HBALine(db, user, cidr, "scram-sha-256")

	steps := []run.Command{
		{
			Title: "Cadangkan " + cl.HBAFile(), Argv: []string{"cp", "-a", cl.HBAFile(), cl.HBAFile() + ".ubt.bak"}, NeedsRoot: true, Risk: risk.Safe,
			Explain: []run.Line{{Token: "cp -a", Meaning: "salin apa adanya sebagai cadangan sebelum aturan baru ditambahkan"}},
			Effect:  "Bila akses jadi kacau, kembalikan dengan: sudo mv " + cl.HBAFile() + ".ubt.bak " + cl.HBAFile(),
		},
		{
			Title: "Tambah aturan akses di pg_hba.conf", Argv: []string{"tee", "-a", cl.HBAFile()}, NeedsRoot: true, Risk: risk.Dangerous,
			Stdin: "# Ditambahkan ubt\n" + line + "\n", StdinLabel: "(1 aturan pg_hba)",
			Explain: []run.Line{
				{Token: "tee -a", Meaning: "tambahkan baris di akhir berkas tanpa mengubah baris yang sudah ada"},
				{Token: cl.HBAFile(), Meaning: "daftar siapa boleh konek ke database mana dari alamat mana"},
				{Token: line, Meaning: "user " + user + " boleh konek ke database " + db + " dari " + cidr + " dengan password (scram-sha-256)"},
			},
			Effect: "Urutan baris menentukan: baris pertama yang cocok yang dipakai. Aturan ini ditaruh paling bawah, jadi aturan penolakan di atasnya tetap menang.",
			Safer:  "Batasi alamat sesempit mungkin (mis. /32 untuk satu server aplikasi), bukan 0.0.0.0/0.",
		},
	}
	if mode != ListenLocal {
		steps = append(steps, run.Command{
			Title: "Buka listen_addresses", Argv: []string{"install", "-D", "-m", "0644", "-o", SuperUser, "-g", SuperUser, "/dev/stdin", listenPath}, NeedsRoot: true, Risk: risk.Dangerous,
			Stdin: ListenConf(mode), StdinLabel: "(konfigurasi listen_addresses)",
			Explain: []run.Line{
				{Token: "install -D -o postgres", Meaning: "tulis berkas tambahan di conf.d; postgresql.conf bawaan tidak diubah"},
				{Token: "listen_addresses = '*'", Meaning: "server mulai mendengarkan di semua alamat jaringan, bukan hanya localhost"},
			},
			Effect: "Port 5432 mulai terbuka di jaringan. Yang menentukan siapa boleh masuk tetap pg_hba.conf dan firewall.",
			Safer:  "Bila aplikasi berada di server lain di jaringan privat, pertimbangkan tunnel SSH atau VPN alih-alih membuka port.",
		}, run.Command{
			Title: "Restart cluster " + cl.ID(), Argv: []string{"systemctl", "restart", cl.Unit()}, NeedsRoot: true, Risk: risk.Dangerous,
			Explain: []run.Line{{Token: "systemctl restart", Meaning: "listen_addresses hanya dibaca saat server start, tidak cukup reload"}},
			Effect:  "Semua koneksi aplikasi terputus beberapa detik.",
		})
	} else {
		steps = append(steps, run.Command{
			Title: "Muat ulang aturan akses", Argv: []string{"systemctl", "reload", cl.Unit()}, NeedsRoot: true, Risk: risk.Caution,
			Explain: []run.Line{{Token: "systemctl reload", Meaning: "server membaca ulang pg_hba.conf tanpa memutus koneksi"}},
			Effect:  "Aturan baru langsung berlaku untuk koneksi berikutnya. Bila ada baris yang salah tulis, server MEMPERTAHANKAN aturan lama dan mencatat errornya di log.",
		})
	}
	return run.Plan{Title: "Atur akses PostgreSQL dari jaringan", Steps: steps}
}

// SettingsConf merender berkas conf.d dari daftar parameter.
func SettingsConf(title string, settings []Tuned) string {
	var b strings.Builder
	b.WriteString("# Dibuat oleh ubt — " + title + ".\n")
	b.WriteString("# Berkas ini dibaca lewat include_dir 'conf.d' di postgresql.conf bawaan Ubuntu.\n")
	b.WriteString("# Hapus berkas ini lalu restart untuk kembali ke nilai bawaan.\n\n")
	for _, s := range settings {
		b.WriteString(fmt.Sprintf("%-32s = %-12s # %s\n", s.Name, s.Value, s.Why))
	}
	return b.String()
}

// ApplySettingsPlan menulis parameter hasil tuning lalu me-restart cluster.
func ApplySettingsPlan(cl Cluster, settings []Tuned) run.Plan {
	path := cl.DropIn(DropInTuning)
	restart := false
	for _, s := range settings {
		if s.NeedsRestart {
			restart = true
		}
	}
	write := run.Command{
		Title: "Tulis " + path, Argv: []string{"install", "-D", "-m", "0644", "-o", SuperUser, "-g", SuperUser, "/dev/stdin", path}, NeedsRoot: true,
		Stdin: SettingsConf("hasil penyetelan otomatis", settings), StdinLabel: fmt.Sprintf("(%d parameter)", len(settings)),
		Explain: []run.Line{
			{Token: "install -D -o postgres", Meaning: "tulis berkas parameter tambahan milik postgres"},
			{Token: path, Meaning: "berkas terpisah di conf.d; postgresql.conf bawaan tidak pernah diubah ubt"},
		},
		Effect: "Nilai di berkas ini menang atas nilai bawaan karena dibaca paling akhir.",
		Risk:   risk.Caution,
		Safer:  "Catat nilai sebelumnya (terlihat di layar sebelum ini); untuk membatalkan, hapus berkas ini lalu restart.",
	}
	steps := []run.Command{write}
	if restart {
		steps = append(steps, run.Command{
			Title: "Restart cluster " + cl.ID(), Argv: []string{"systemctl", "restart", cl.Unit()}, NeedsRoot: true, Risk: risk.Dangerous,
			Explain: []run.Line{{Token: "systemctl restart", Meaning: "parameter seperti shared_buffers & max_connections hanya dibaca saat server start"}},
			Effect:  "Semua koneksi aplikasi terputus beberapa detik. Bila server menolak menyala karena nilai terlalu besar, alasannya ada di " + cl.LogFile + ".",
			Safer:   "Lakukan di jam sepi, dan siapkan cara mengembalikan: sudo rm " + path + " && sudo systemctl restart " + cl.Unit(),
		})
	} else {
		steps = append(steps, run.Command{
			Title: "Muat ulang konfigurasi", Argv: []string{"systemctl", "reload", cl.Unit()}, NeedsRoot: true, Risk: risk.Safe,
			Explain: []run.Line{{Token: "systemctl reload", Meaning: "parameter ini bisa berlaku tanpa memutus koneksi"}},
			Effect:  "Nilai baru berlaku untuk koneksi berikutnya.",
		})
	}
	return run.Plan{Title: "Terapkan penyetelan PostgreSQL", Steps: steps}
}
