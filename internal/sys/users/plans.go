package users

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Context adalah siapa yang sedang menjalankan ubt.
type Context struct {
	CurrentUser string
	IsRoot      bool
}

// GuardUserChange menolak perubahan yang mengunci admin keluar.
func GuardUserChange(action string, target User, d Data, ctx Context) error {
	sudo := d.SudoMembers()
	lastSudo := len(sudo) == 1 && sudo[0] == target.Name
	switch action {
	case "delete", "lock", "expire":
		if target.Name == ctx.CurrentUser {
			return errors.New("ditolak: ini user yang sedang kamu pakai")
		}
		if target.UID == 0 {
			return errors.New("ditolak: akun root tidak boleh dihapus atau dikunci dari sini")
		}
		if lastSudo {
			return errors.New("ditolak: ini satu-satunya anggota grup sudo; server tidak akan punya admin lagi")
		}
	case "remove-sudo":
		if lastSudo {
			return errors.New("ditolak: ini satu-satunya anggota grup sudo; tambahkan admin lain dulu")
		}
		if target.Name == ctx.CurrentUser {
			return errors.New("ditolak: kamu akan kehilangan akses sudo sendiri di tengah sesi")
		}
	}
	return nil
}

// AddUserPlan membuat user baru, opsional langsung jadi admin.
func AddUserPlan(name string, admin bool) run.Plan {
	p := run.Plan{
		Title: "Tambah user " + name,
		Steps: []run.Command{{
			Title: "Buat user " + name, Argv: []string{"adduser", name}, NeedsRoot: true, Interactive: true,
			Explain: []run.Line{{Token: "adduser", Meaning: "buat user, folder home, dan minta password serta nama lengkap (boleh dikosongkan)"}},
			Effect:  "Password diketik langsung di terminal dan tidak tersimpan di ubt.",
			Risk:    risk.Caution,
		}},
	}
	if admin {
		p.Steps = append(p.Steps, GroupPlan(name, "sudo", true).Steps...)
	}
	return p
}

var groupMeaning = map[string]string{
	"sudo":     "boleh menjalankan perintah sebagai root dengan sudo (admin)",
	"docker":   "boleh memakai docker tanpa sudo — setara root, berikan hanya ke yang dipercaya",
	"adm":      "boleh membaca log sistem di /var/log dan journal",
	"www-data": "boleh membaca/menulis file web yang dimiliki www-data",
}

// GroupPlan menambah (add=true) atau mengeluarkan user dari grup.
func GroupPlan(user, group string, add bool) run.Plan {
	lvl := risk.Caution
	if group == "sudo" || group == "docker" {
		lvl = risk.Dangerous
	}
	meaning := groupMeaning[group]
	if add {
		return run.Single(run.Command{
			Title: fmt.Sprintf("Tambahkan %s ke grup %s", user, group), Argv: []string{"usermod", "-aG", group, user}, NeedsRoot: true,
			Explain: []run.Line{{Token: "-aG " + group, Meaning: "tambahkan (append) ke grup tanpa mengeluarkan dari grup lain"}, {Token: group, Meaning: meaning}},
			Effect:  "Berlaku setelah " + user + " logout lalu login lagi.",
			Safer:   "jangan lupakan -a: usermod -G tanpa -a mengeluarkan user dari semua grup lain",
			Risk:    lvl,
		})
	}
	return run.Single(run.Command{
		Title: fmt.Sprintf("Keluarkan %s dari grup %s", user, group), Argv: []string{"gpasswd", "-d", user, group}, NeedsRoot: true,
		Explain: []run.Line{{Token: "gpasswd -d", Meaning: "hapus user dari satu grup saja"}},
		Effect:  "Berlaku untuk login berikutnya; sesi yang sedang berjalan masih punya hak lama.",
		Risk:    lvl,
	})
}

// LockPlan mengunci atau membuka password user.
func LockPlan(user string, lock bool) run.Plan {
	if lock {
		return run.Single(run.Command{
			Title: "Kunci password " + user, Argv: []string{"passwd", "-l", user}, NeedsRoot: true,
			Explain: []run.Line{{Token: "passwd -l", Meaning: "kunci login dengan password"}},
			Effect:  "PENTING: login dengan SSH key masih bisa. Untuk menutup akun sepenuhnya, pakai \"nonaktifkan akun\".",
			Risk:    risk.Caution,
		})
	}
	return run.Single(run.Command{Title: "Buka kunci password " + user, Argv: []string{"passwd", "-u", user}, NeedsRoot: true,
		Explain: []run.Line{{Token: "passwd -u", Meaning: "buka kembali login dengan password"}}, Risk: risk.Caution})
}

// ExpirePlan menonaktifkan akun sepenuhnya (password maupun key).
func ExpirePlan(user string) run.Plan {
	return run.Single(run.Command{
		Title: "Nonaktifkan akun " + user, Argv: []string{"usermod", "--expiredate", "1", user}, NeedsRoot: true,
		Explain: []run.Line{{Token: "--expiredate 1", Meaning: "tandai akun kedaluwarsa sejak 1970: semua cara login ditolak, data tetap ada"}},
		Effect:  "Buka lagi dengan: sudo usermod --expiredate '' " + user,
		Risk:    risk.Dangerous,
	})
}

// DeleteUserPlan menghapus user beserta folder home.
func DeleteUserPlan(user string) run.Plan {
	return run.Single(run.Command{
		Title: "Hapus user " + user, Argv: []string{"deluser", "--remove-home", user}, NeedsRoot: true,
		Explain: []run.Line{{Token: "--remove-home", Meaning: "ikut hapus /home/" + user + " beserta semua isinya"}},
		Effect:  "File di folder home hilang permanen. Proses milik user ini sebaiknya dihentikan dulu.",
		Safer:   "nonaktifkan akun dulu (data tetap ada), hapus setelah yakin",
		Risk:    risk.Dangerous,
	})
}

// sshDirSteps menyiapkan ~/.ssh dengan izin yang benar.
func sshDirSteps(user, home string, root bool) []run.Command {
	dir := filepath.Join(home, ".ssh")
	steps := []run.Command{
		{Title: "Pastikan folder ~/.ssh ada", Argv: []string{"install", "-d", "-m", "700", dir}, NeedsRoot: root,
			Explain: []run.Line{{Token: "install -d -m 700", Meaning: "buat folder bila belum ada dengan izin hanya pemilik"}}},
	}
	if root {
		steps = append(steps, run.Command{Title: "Pemilik folder ~/.ssh", Argv: []string{"chown", user + ":", dir}, NeedsRoot: true,
			Explain: []run.Line{{Token: "chown " + user + ":", Meaning: "folder harus milik user itu sendiri, bila tidak sshd menolak key"}}})
	}
	return steps
}

// AddKeyPlan menambahkan public key ke authorized_keys.
func AddKeyPlan(user, home string, key Key, self bool) run.Plan {
	root := !self
	file := filepath.Join(home, ".ssh", "authorized_keys")
	steps := sshDirSteps(user, home, root)
	steps = append(steps,
		run.Command{Title: "Tambahkan key", Argv: []string{"tee", "-a", file}, NeedsRoot: root,
			Stdin: key.Line + "\n", StdinLabel: "(" + key.Short() + ")",
			Explain: []run.Line{{Token: "tee -a", Meaning: "tambahkan satu baris ke akhir authorized_keys"}},
			Effect:  "Pemilik private key pasangannya bisa login sebagai " + user + " tanpa password.",
			Risk:    risk.Caution},
		run.Command{Title: "Kunci izin authorized_keys", Argv: []string{"chmod", "600", file}, NeedsRoot: root,
			Explain: []run.Line{{Token: "chmod 600", Meaning: "hanya pemilik yang bisa membaca/menulis"}}},
	)
	if root {
		steps = append(steps, run.Command{Title: "Pemilik authorized_keys", Argv: []string{"chown", user + ":", file}, NeedsRoot: true,
			Explain: []run.Line{{Token: "chown", Meaning: "file milik user itu sendiri"}}})
	}
	return run.Plan{Title: "Tambah SSH key untuk " + user, Steps: steps}
}

// RemoveKeyPlan menulis ulang authorized_keys tanpa key yang dihapus (dengan backup).
func RemoveKeyPlan(user, home string, all []Key, remove int, self bool, now time.Time) run.Plan {
	root := !self
	file := filepath.Join(home, ".ssh", "authorized_keys")
	var keep []string
	for i, k := range all {
		if i != remove {
			keep = append(keep, k.Line)
		}
	}
	content := strings.Join(keep, "\n")
	if content != "" {
		content += "\n"
	}
	backup := file + ".ubt-bak-" + now.Format("20060102-150405")
	return run.Plan{
		Title: "Hapus SSH key " + all[remove].Comment,
		Steps: []run.Command{
			{Title: "Cadangkan authorized_keys", Argv: []string{"cp", "-a", file, backup}, NeedsRoot: root,
				Explain: []run.Line{{Token: backup, Meaning: "salinan untuk dikembalikan bila salah hapus"}}},
			{Title: "Tulis ulang tanpa key tersebut", Argv: []string{"install", "-m", "600", "/dev/stdin", file}, NeedsRoot: root,
				Stdin: content, StdinLabel: fmt.Sprintf("(%d key tersisa)", len(keep)),
				Explain: []run.Line{{Token: "install -m 600", Meaning: "ganti isi file dengan key yang tersisa"}},
				Effect:  "Pemilik key " + all[remove].Short() + " tidak bisa login lagi sebagai " + user + ".",
				Risk:    risk.Dangerous},
		},
	}
}

// FixPermissionsPlan memperbaiki izin yang membuat sshd menolak key.
func FixPermissionsPlan(user, home string, self bool) run.Plan {
	root := !self
	dir := filepath.Join(home, ".ssh")
	steps := []run.Command{
		{Title: "Home tidak boleh bisa ditulis orang lain", Argv: []string{"chmod", "go-w", home}, NeedsRoot: root,
			Explain: []run.Line{{Token: "go-w", Meaning: "cabut izin tulis dari grup & user lain (sshd StrictModes)"}}},
		{Title: "Izin ~/.ssh", Argv: []string{"chmod", "700", dir}, NeedsRoot: root,
			Explain: []run.Line{{Token: "700", Meaning: "hanya pemilik"}}},
		{Title: "Izin authorized_keys", Argv: []string{"chmod", "600", filepath.Join(dir, "authorized_keys")}, NeedsRoot: root,
			Explain: []run.Line{{Token: "600", Meaning: "hanya pemilik bisa baca & tulis"}}},
	}
	if root {
		steps = append(steps, run.Command{Title: "Pemilik ~/.ssh", Argv: []string{"chown", "-R", user + ":", dir}, NeedsRoot: true,
			Explain: []run.Line{{Token: "chown -R", Meaning: "semua isi ~/.ssh milik user itu"}}})
	}
	return run.Plan{Title: "Perbaiki izin SSH untuk " + user, Steps: steps}
}

// GenerateKeyPlan membuat pasangan key ed25519 untuk user saat ini.
func GenerateKeyPlan(home, email string) run.Plan {
	path := filepath.Join(home, ".ssh", "id_ed25519")
	return run.Single(run.Command{
		Title: "Buat SSH key baru", Argv: []string{"ssh-keygen", "-t", "ed25519", "-C", email, "-f", path}, Interactive: true,
		Explain: []run.Line{
			{Token: "-t ed25519", Meaning: "jenis key modern: pendek, cepat, dan aman"},
			{Token: "-C " + email, Meaning: "komentar untuk mengenali key ini"},
			{Token: "-f " + path, Meaning: "lokasi private key; public key di " + path + ".pub"},
		},
		Effect: "ssh-keygen meminta passphrase (disarankan diisi). Private key tidak boleh dibagikan; yang di-copy ke server lain hanya file .pub.",
	})
}

// CopyIDPlan mengirim public key ke server lain.
func CopyIDPlan(pub, target string) run.Plan {
	return run.Single(run.Command{
		Title: "Pasang key ke " + target, Argv: []string{"ssh-copy-id", "-i", pub, target}, Interactive: true,
		Explain: []run.Line{{Token: "ssh-copy-id", Meaning: "login ke server tujuan (dengan password) lalu tambahkan key ini ke authorized_keys di sana"}},
		Risk:    risk.Caution,
	})
}
