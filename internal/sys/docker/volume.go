package docker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/arif-rachim/ubuntu-tool/internal/risk"
	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Volume adalah satu baris `docker volume ls`.
type Volume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
	Scope      string `json:"Scope"`
	Labels     string `json:"Labels"`
	Size       string `json:"Size"` // hanya terisi pada `docker system df -v`
}

// Network adalah satu baris `docker network ls`.
type Network struct {
	ID     string `json:"ID"`
	Name   string `json:"Name"`
	Driver string `json:"Driver"`
	Scope  string `json:"Scope"`
	Labels string `json:"Labels"`
}

// Builtin melaporkan network bawaan docker yang tidak boleh dihapus.
func (n Network) Builtin() bool {
	return n.Name == "bridge" || n.Name == "host" || n.Name == "none"
}

// ParseVolumes membaca output `docker volume ls --format '{{json .}}'`.
func ParseVolumes(out string) []Volume { return parseLines[Volume](out) }

// ParseNetworks membaca output `docker network ls --format '{{json .}}'`.
func ParseNetworks(out string) []Network { return parseLines[Network](out) }

// VolumeUsers mengembalikan nama container yang memakai volume tertentu, dibaca dari kolom Mounts
// `docker ps -a`.
func VolumeUsers(name string, cs []Container) []string {
	var out []string
	for _, c := range cs {
		for _, m := range strings.Split(c.Mounts, ",") {
			if strings.TrimSpace(m) == name {
				out = append(out, c.Name())
				break
			}
		}
	}
	return out
}

// NetworkUsers mengembalikan nama container yang tersambung ke sebuah network.
func NetworkUsers(name string, cs []Container) []string {
	var out []string
	for _, c := range cs {
		for _, n := range strings.Split(c.Networks, ",") {
			if strings.TrimSpace(n) == name {
				out = append(out, c.Name())
				break
			}
		}
	}
	return out
}

// ReadVolumes membaca daftar volume.
func (c Client) ReadVolumes(ctx context.Context, r run.Runner) []Volume {
	out, _, err := r.Capture(ctx, c.Command("volume", "ls", "--format", "{{json .}}"))
	if err != nil {
		return nil
	}
	return ParseVolumes(out)
}

// ReadNetworks membaca daftar network.
func (c Client) ReadNetworks(ctx context.Context, r run.Runner) []Network {
	out, _, err := r.Capture(ctx, c.Command("network", "ls", "--format", "{{json .}}"))
	if err != nil {
		return nil
	}
	return ParseNetworks(out)
}

// CreateVolumePlan membuat volume bernama.
func (c Client) CreateVolumePlan(name string) run.Plan {
	return run.Single(c.cmd("Buat volume "+name, risk.Safe,
		"Volume kosong siap dipakai container, mis. -v "+name+":/var/lib/postgresql/data. Docker menyimpan isinya di /var/lib/docker/volumes/"+name+"/_data.",
		[]run.Line{
			{Token: "docker volume create", Meaning: "buat penyimpanan yang umurnya terpisah dari container"},
			{Token: name, Meaning: "nama volume; dipakai di -v NAMA:/path"},
		}, "volume", "create", name))
}

// RemoveVolumePlan menghapus volume beserta seluruh isinya.
func (c Client) RemoveVolumePlan(v Volume, users []string) run.Plan {
	cmd := c.cmd("Hapus volume "+v.Name, risk.Dangerous,
		"SEMUA data di dalam volume hilang permanen dan tidak bisa dikembalikan. Docker menolak bila masih dipakai container.",
		[]run.Line{
			{Token: "docker volume rm", Meaning: "hapus volume beserta isinya"},
			{Token: v.Name, Meaning: "volume yang dihapus"},
		}, "volume", "rm", v.Name)
	cmd.Safer = "Cadangkan dulu isinya jadi arsip lewat menu volume (tombol b)."
	if len(users) > 0 {
		cmd.Effect += " Volume ini masih terpasang di container: " + strings.Join(users, ", ") + "."
	}
	return run.Single(cmd)
}

// PruneVolumesPlan menghapus semua volume yang tidak dipakai container mana pun.
func (c Client) PruneVolumesPlan() run.Plan {
	cmd := c.cmd("Hapus semua volume tak terpakai", risk.Dangerous,
		"Volume yang tidak sedang terpasang di container mana pun dihapus beserta datanya — termasuk volume database dari container yang kebetulan sedang dihapus/dibuat ulang.",
		[]run.Line{
			{Token: "docker volume prune", Meaning: "hapus volume yang tidak dipakai container mana pun"},
			{Token: "-f", Meaning: "tanpa bertanya lagi (sudah dikonfirmasi di ubt)"},
		}, "volume", "prune", "-f")
	cmd.Safer = "Lihat dulu mana yang akan hilang: docker volume ls -f dangling=true"
	return run.Single(cmd)
}

// CreateNetworkPlan membuat network bridge.
func (c Client) CreateNetworkPlan(name string) run.Plan {
	return run.Single(c.cmd("Buat network "+name, risk.Safe,
		"Container yang disambungkan ke network ini bisa saling memanggil lewat namanya, mis. http://api:3000, tanpa perlu membuka port ke server.",
		[]run.Line{
			{Token: "docker network create", Meaning: "buat jaringan privat antar container (driver bridge)"},
			{Token: name, Meaning: "nama network; dipakai di --network NAMA"},
		}, "network", "create", name))
}

// RemoveNetworkPlan menghapus network.
func (c Client) RemoveNetworkPlan(n Network, users []string) run.Plan {
	cmd := c.cmd("Hapus network "+n.Name, risk.Caution,
		"Container yang memakainya kehilangan jalur komunikasi antar-nama. Docker menolak bila masih ada container yang tersambung.",
		[]run.Line{{Token: "docker network rm", Meaning: "hapus jaringan privat antar container"}}, "network", "rm", n.Name)
	if len(users) > 0 {
		cmd.Effect += " Masih dipakai: " + strings.Join(users, ", ") + "."
	}
	return run.Single(cmd)
}

// helperImage adalah image kecil untuk menyalin isi volume ke/dari arsip.
const helperImage = "alpine:3.20"

// ValidBackupDir memeriksa direktori tujuan cadangan.
func ValidBackupDir(s string) error {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "/") {
		return errors.New("tulis path lengkap direktori, mis. /home/kamu/backup")
	}
	return nil
}

// BackupVolumePlan menyalin isi volume ke arsip .tar.gz di direktori host.
// Volume dipasang read-only, jadi isinya tidak mungkin berubah karena proses cadangan.
func (c Client) BackupVolumePlan(name, dir, file string) run.Plan {
	args := []string{"run", "--rm",
		"-v", name + ":/data:ro",
		"-v", dir + ":/backup",
		helperImage, "tar", "czf", "/backup/" + file, "-C", "/data", "."}
	cmd := c.cmd("Cadangkan volume "+name, risk.Caution,
		"Hasilnya: "+filepath.Join(dir, file)+". File milik root karena dibuat dari dalam container; ubah pemiliknya bila perlu dengan chown.",
		[]run.Line{
			{Token: "docker run --rm " + helperImage, Meaning: "container sementara berisi tar, dihapus otomatis setelah selesai"},
			{Token: "-v " + name + ":/data:ro", Meaning: "pasang volume yang dicadangkan sebagai hanya-baca (ro)"},
			{Token: "-v " + dir + ":/backup", Meaning: "pasang direktori host tempat arsip ditulis"},
			{Token: "tar czf /backup/" + file + " -C /data .", Meaning: "arsipkan seluruh isi volume ke satu file terkompresi"},
		}, args...)
	cmd.Safer = "Untuk database, hentikan dulu containernya (atau pakai pg_dump/mysqldump) supaya file tidak tersalin di tengah penulisan."
	return run.Single(cmd)
}

// RestoreVolumePlan menuangkan isi arsip ke dalam volume.
func (c Client) RestoreVolumePlan(name, dir, file string) run.Plan {
	args := []string{"run", "--rm",
		"-v", name + ":/data",
		"-v", dir + ":/backup:ro",
		helperImage, "tar", "xzf", "/backup/" + file, "-C", "/data"}
	cmd := c.cmd("Pulihkan volume "+name+" dari "+file, risk.Dangerous,
		"File di dalam volume yang namanya sama dengan isi arsip akan ditimpa. File lain di volume tidak dihapus.",
		[]run.Line{
			{Token: "docker run --rm " + helperImage, Meaning: "container sementara berisi tar"},
			{Token: "-v " + name + ":/data", Meaning: "volume tujuan, bisa ditulis"},
			{Token: "-v " + dir + ":/backup:ro", Meaning: "direktori arsip, dipasang hanya-baca"},
			{Token: "tar xzf /backup/" + file + " -C /data", Meaning: "buka arsip ke dalam volume"},
		}, args...)
	cmd.Safer = "Hentikan dulu container yang memakai volume ini, supaya tidak menulis bersamaan saat dipulihkan."
	return run.Single(cmd)
}
