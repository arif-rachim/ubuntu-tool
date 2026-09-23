package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Stat adalah satu baris `docker stats --no-stream`.
type Stat struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     string `json:"PIDs"`
}

// Percent membaca "12.34%" menjadi angka; -1 bila tidak terbaca.
func Percent(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return -1
	}
	return v
}

// ParseStats membaca output `docker stats --no-stream --format '{{json .}}'`, terberat dulu.
func ParseStats(out string) []Stat {
	ss := parseLines[Stat](out)
	sort.SliceStable(ss, func(a, b int) bool { return Percent(ss[a].CPUPerc) > Percent(ss[b].CPUPerc) })
	return ss
}

// ReadStats membaca pemakaian CPU/RAM container yang sedang berjalan (sekali ambil, tidak streaming).
func (c Client) ReadStats(ctx context.Context, r run.Runner) ([]Stat, error) {
	out, stderr, err := r.Capture(ctx, c.Command("stats", "--no-stream", "--format", "{{json .}}"))
	if err != nil {
		return nil, errors.New(strings.TrimSpace(firstLine(stderr) + " " + err.Error()))
	}
	return ParseStats(out), nil
}

// Inspect adalah bagian `docker inspect` yang dipakai ubt.
type Inspect struct {
	Name         string   `json:"Name"`
	Created      string   `json:"Created"`
	RestartCount int      `json:"RestartCount"`
	Path         string   `json:"Path"`
	Args         []string `json:"Args"`
	State        struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		OOMKilled  bool   `json:"OOMKilled"`
		Dead       bool   `json:"Dead"`
		ExitCode   int    `json:"ExitCode"`
		Error      string `json:"Error"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status        string `json:"Status"`
			FailingStreak int    `json:"FailingStreak"`
			Log           []struct {
				ExitCode int    `json:"ExitCode"`
				Output   string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		WorkingDir string            `json:"WorkingDir"`
		User       string            `json:"User"`
		Entrypoint []string          `json:"Entrypoint"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		RestartPolicy struct {
			Name              string `json:"Name"`
			MaximumRetryCount int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		Memory       int64    `json:"Memory"`
		NanoCpus     int64    `json:"NanoCpus"`
		NetworkMode  string   `json:"NetworkMode"`
		Binds        []string `json:"Binds"`
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

// ParseInspect membaca output `docker inspect NAMA` (array berisi satu objek).
func ParseInspect(out string) (Inspect, error) {
	var arr []Inspect
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		return Inspect{}, err
	}
	if len(arr) == 0 {
		return Inspect{}, errors.New("docker inspect tidak mengembalikan data")
	}
	return arr[0], nil
}

// ReadInspect membaca detail satu container.
func (c Client) ReadInspect(ctx context.Context, r run.Runner, name string) (Inspect, error) {
	out, stderr, err := r.Capture(ctx, c.Command("inspect", name))
	if err != nil {
		return Inspect{}, errors.New(strings.TrimSpace(firstLine(stderr) + " " + err.Error()))
	}
	return ParseInspect(out)
}

// ShortName membuang garis miring di depan nama container hasil inspect.
func (i Inspect) ShortName() string { return strings.TrimPrefix(i.Name, "/") }

// ExitMeaning menjelaskan exit code container dalam bahasa manusia.
func ExitMeaning(code int) string {
	switch code {
	case 0:
		return "program selesai normal (bukan error)"
	case 1:
		return "program berhenti karena error; penyebabnya biasanya tertulis di baris terakhir log"
	case 125:
		return "docker sendiri menolak menjalankan container (opsi docker run salah, port bentrok, volume tidak ada)"
	case 126:
		return "file command ditemukan tetapi tidak bisa dijalankan (tidak executable atau salah arsitektur)"
	case 127:
		return "command atau file yang diminta tidak ada di dalam image"
	case 137:
		return "proses dimatikan paksa (SIGKILL) — paling sering karena kehabisan memori (OOM) atau docker stop yang melewati batas waktu"
	case 139:
		return "program crash karena segmentation fault (bug di program atau arsitektur image tidak cocok)"
	case 143:
		return "program dihentikan dengan SIGTERM, mis. lewat docker stop"
	}
	if code > 128 && code < 165 {
		return fmt.Sprintf("program dihentikan sinyal %d", code-128)
	}
	return "kode keluar dari program di dalam container; artinya ditentukan program itu sendiri"
}

// Finding adalah satu temuan diagnosa container.
type Finding struct {
	Symptom string // apa yang terlihat
	Why     string // artinya
	Fix     string // apa yang bisa dilakukan
}

// Troubleshoot membaca hasil inspect (dan beberapa baris log terakhir) lalu menyimpulkan
// kenapa container bermasalah. Urutan temuan: yang paling menentukan dulu.
func Troubleshoot(i Inspect, logs string) []Finding {
	var out []Finding
	add := func(s, w, f string) { out = append(out, Finding{Symptom: s, Why: w, Fix: f}) }
	st := i.State

	if st.OOMKilled {
		limit := "tanpa batas -m, berarti RAM server yang habis"
		if i.HostConfig.Memory > 0 {
			limit = "batas -m " + humanBytes(i.HostConfig.Memory)
		}
		add("Container dimatikan kernel karena kehabisan memori (OOMKilled)",
			"Program memakai RAM melebihi batas yang tersedia ("+limit+").",
			"Naikkan batas memori saat membuat ulang container, atau perbaiki pemakaian memori program. Lihat tekanan memori server di modul Resource.")
	}
	if st.Status == "restarting" || (i.RestartCount >= 3 && !st.Running) {
		add(fmt.Sprintf("Container sudah restart %d kali", i.RestartCount),
			"Program di dalamnya berhenti terus dan kebijakan restart \""+i.HostConfig.RestartPolicy.Name+"\" menjalankannya lagi. Ini menyamarkan error aslinya.",
			"Baca log container (tombol l) — penyebab sebenarnya ada di baris terakhir sebelum tiap restart.")
	}
	if !st.Running && st.ExitCode != 0 {
		add(fmt.Sprintf("Berhenti dengan exit code %d", st.ExitCode), ExitMeaning(st.ExitCode),
			"Baca log container, lalu perbaiki konfigurasi atau image sebelum menjalankannya lagi.")
	}
	if st.Error != "" {
		add("Pesan error dari docker: "+strings.TrimSpace(st.Error),
			"Docker gagal menyiapkan atau menjalankan container.",
			"Biasanya soal port yang sudah dipakai, volume/berkas yang tidak ada, atau image yang tidak cocok.")
	}
	if h := st.Health; h != nil && h.Status == "unhealthy" {
		last := ""
		if n := len(h.Log); n > 0 {
			last = strings.TrimSpace(h.Log[n-1].Output)
			if len(last) > 200 {
				last = last[:200] + "…"
			}
		}
		add(fmt.Sprintf("Healthcheck gagal %d kali berturut-turut", h.FailingStreak),
			"Container berjalan, tetapi command healthcheck-nya melaporkan gagal. Output terakhir: "+last,
			"Periksa apakah aplikasinya benar-benar siap melayani, atau healthcheck-nya yang perlu diberi waktu lebih lama (--health-start-period).")
	}
	if st.Running && i.HostConfig.RestartPolicy.Name == "" {
		add("Tanpa kebijakan restart", "Container tidak akan menyala lagi setelah server reboot atau daemon docker restart.",
			"Buat ulang container dengan --restart unless-stopped lewat wizard jalankan container.")
	}
	for _, l := range strings.Split(logs, "\n") {
		low := strings.ToLower(l)
		switch {
		case strings.Contains(low, "address already in use"), strings.Contains(low, "bind: address already in use"):
			add("Log menyebut \"address already in use\"", "Port yang dipakai program sudah dipakai proses lain di server.",
				"Cari pemakainya di modul Ports & Proses, atau ganti port host di -p.")
			return dedupeFindings(out)
		case strings.Contains(low, "permission denied"):
			add("Log menyebut \"permission denied\"", "Proses di dalam container tidak boleh membaca/menulis file yang dipasang dari host.",
				"Cocokkan pemilik direktori host dengan --user di container, atau perbaiki izin direktori tersebut.")
			return dedupeFindings(out)
		case strings.Contains(low, "connection refused"):
			add("Log menyebut \"connection refused\"", "Container mencoba menghubungi layanan lain yang tidak menerima koneksi.",
				"Bila tujuannya container lain, sambungkan keduanya ke satu network dan panggil lewat nama container, bukan localhost.")
			return dedupeFindings(out)
		}
	}
	return dedupeFindings(out)
}

func dedupeFindings(fs []Finding) []Finding {
	seen := map[string]bool{}
	var out []Finding
	for _, f := range fs {
		if !seen[f.Symptom] {
			seen[f.Symptom] = true
			out = append(out, f)
		}
	}
	return out
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n/(1<<20))
	}
	return fmt.Sprintf("%d B", n)
}

// SpecFromInspect membangun resep container dari hasil inspect, supaya container yang dibuat di luar
// ubt tetap bisa dibuat ulang dengan setting yang sama.
func SpecFromInspect(i Inspect) RunSpec {
	s := RunSpec{Name: i.ShortName(), Image: i.Config.Image, Detach: true,
		Workdir: i.Config.WorkingDir, User: i.Config.User, Restart: i.HostConfig.RestartPolicy.Name}
	if s.Workdir == "/" {
		s.Workdir = ""
	}
	if n := i.HostConfig.NetworkMode; n != "" && n != "default" && n != "bridge" && n != "host" {
		s.Network = n
	}
	if i.HostConfig.Memory > 0 {
		s.Memory = strconv.FormatInt(i.HostConfig.Memory/(1<<20), 10) + "m"
	}
	if i.HostConfig.NanoCpus > 0 {
		s.CPUs = strconv.FormatFloat(float64(i.HostConfig.NanoCpus)/1e9, 'g', -1, 64)
	}
	var ports []string
	for ctrPort, binds := range i.HostConfig.PortBindings {
		port, proto, _ := strings.Cut(ctrPort, "/")
		for _, b := range binds {
			m := b.HostPort + ":" + port
			if b.HostIP != "" && b.HostIP != "0.0.0.0" {
				m = b.HostIP + ":" + m
			}
			if proto == "udp" {
				m += "/udp"
			}
			ports = append(ports, m)
		}
	}
	sort.Strings(ports)
	s.Ports = ports
	for _, m := range i.Mounts {
		src := m.Source
		if m.Type == "volume" {
			src = m.Name
		}
		bind := src + ":" + m.Destination
		if !m.RW {
			bind += ":ro"
		}
		s.Volumes = append(s.Volumes, bind)
	}
	sort.Strings(s.Volumes)
	// Env bawaan image (PATH, LANG, dll.) tidak ikut: hanya yang benar-benar di-set saat run yang
	// bisa dibedakan, dan itu tidak terlihat dari inspect. Biarkan user mengisinya lagi di wizard.
	return s
}

// Age menampilkan umur container dari timestamp inspect.
func Age(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil || t.IsZero() {
		return "—"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "baru saja"
	case d < time.Hour:
		return fmt.Sprintf("%d menit", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d jam", int(d.Hours()))
	}
	return fmt.Sprintf("%d hari", int(d.Hours()/24))
}
