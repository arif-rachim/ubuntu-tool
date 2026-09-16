# `ubt` — Interactive Ubuntu Helper CLI

## Context

Repo `arif-rachim/ubuntu-tool` masih kosong total (fresh git repo, 0 commit, 0 file). Tujuannya
membangun dari nol sebuah **CLI interaktif wizard-style untuk pemula Linux di Ubuntu 24.04** —
user mengetik satu kata (`ubt`), lalu dituntun lewat menu panah/list/konfirmasi, bukan menghafal
flag. Rasanya seperti prompt interaktif Claude Code.

Masalah yang diselesaikan: pemula tahu *apa* yang ingin dilakukan ("port 8080 kepakai siapa?",
"matikan proses ini", "buka port di firewall", "coba-coba di dalam container") tapi tidak tahu
*command*-nya, dan takut salah ketik lalu merusak sistem.

Hasil yang diinginkan: tool yang (a) menemukan jawabannya untuk user, dan (b) **selalu menampilkan
command asli yang akan dijalankan + penjelasan bahasa manusia + konfirmasi** sebelum mengeksekusi —
sehingga sekaligus jadi alat belajar. Lama-lama user hafal commandnya sendiri.

### Keputusan yang sudah dikunci user

| Hal | Keputusan |
|---|---|
| Bahasa/TUI | **Go + Bubble Tea** — satu binary statis, copy ke `/usr/local/bin`, tanpa runtime |
| Nama binary | **`ubt`** (module path tetap `github.com/arif-rachim/ubuntu-tool`) |
| Bahasa UI | **Bahasa Indonesia** (nama command/flag tetap asli agar user belajar yang sebenarnya) |
| Scope v1 | Keempat modul: **Ports & Proses, Firewall (ufw), Systemd, Docker** |
| Safety | **Tampilkan command persis + penjelasan + konfirmasi** sebelum setiap aksi mutasi |
| Sudo | **Auto-prefix `sudo`** saat aksi butuh root; sudo sendiri yang minta password di terminal |
| Non-TUI | Minimal: `ubt ports --json`, `ubt version`, `ubt doctor`; tetap waras saat di-pipe |

### Temuan environment (sudah diverifikasi di container Ubuntu 24.04.4 ini)

- `systemctl list-units` **tidak punya** `--json`. Flag `-o/--output` di systemctl hanya untuk mode
  journal (dipakai `systemctl status`). systemd 255 di Ubuntu 24.04. ⇒ list unit harus di-parse
  kolom (`--no-legend --plain --full --no-pager`), dan detail unit diambil lewat
  `systemctl show UNIT --property=...` yang outputnya `Key=Value` (stabil, aman di-parse).
- `/proc/net/tcp`, `/proc/PID/{exe,cwd,cmdline,status,cgroup,fd}` semuanya tersedia dan terbaca.
  `/proc/net/tcp6` & `udp6` **bisa tidak ada** (IPv6 mati) — kode wajib tahan file hilang.
- Di container ini `ss`, `ufw`, `ip` **tidak terinstall**, dan docker daemon tidak jalan. Ini bukti
  konkret bahwa tool harus mendeteksi binary/daemon yang hilang dan memberi pesan + saran
  `apt install`, bukan crash. ⇒ perintah `ubt doctor`.
- Go module proxy reachable. Versi terbaru: `bubbletea v1.3.10`, `bubbles v1.0.0`,
  `lipgloss v1.1.0`, `huh v1.0.0`.

---

## Arsitektur

### Struktur direktori

```
ubuntu-tool/
├── cmd/ubt/main.go              # entrypoint: parse subcommand minimal, lalu launch TUI
├── internal/
│   ├── app/                     # root Bubble Tea model
│   │   ├── app.go               #   screen stack, routing, resize, global keys
│   │   ├── keys.go              #   keymap global (esc/q/?/r//)
│   │   └── screen.go            #   interface Screen + pesan navigasi (Push/Pop/Replace)
│   ├── ui/                      # komponen & tema bersama (tak tahu soal domain)
│   │   ├── theme.go             #   palet lipgloss, level bahaya (aman/hati2/bahaya)
│   │   ├── layout.go            #   header+breadcrumb / body / footer keybind
│   │   ├── confirm.go           #   ★ layar "preview command + penjelasan + konfirmasi"
│   │   ├── picker.go            #   list bisa difilter (bubbles/list)
│   │   ├── datatable.go         #   tabel scrollable (bubbles/table)
│   │   ├── detail.go            #   panel key/value + viewport
│   │   ├── form.go              #   host untuk huh.Form (input port, nama image, dll)
│   │   └── status.go            #   spinner, toast, empty state, error state
│   ├── i18n/strings.go          # semua teks Indonesia di satu tempat (siap ditambah EN nanti)
│   ├── run/                     # ★ lapisan eksekusi
│   │   ├── command.go           #   type Command{Argv, NeedsRoot, Title, Explain, Danger, Interactive}
│   │   ├── runner.go            #   interface Runner + real + fake (untuk test)
│   │   ├── sudo.go              #   deteksi root, prefix sudo, cek `sudo -n true`
│   │   ├── interactive.go       #   integrasi tea.ExecProcess (lepas & rebut kembali terminal)
│   │   └── history.go           #   log ke ~/.config/ubt/history.log
│   ├── sys/                     # ★ collector murni — TIDAK boleh import bubbletea
│   │   ├── ports/               #   parse /proc/net/*, map inode→PID, fallback `ss`
│   │   ├── procs/               #   /proc/PID/{exe,cwd,cmdline,status,cgroup}
│   │   ├── systemd/             #   list-units, show, journalctl
│   │   ├── ufw/                 #   `ufw status numbered|verbose`
│   │   └── docker/              #   docker CLI dengan `--format '{{json .}}'`
│   ├── screens/                 # satu package per modul
│   │   ├── home/  ports/  firewall/  services/  dockerui/
│   └── version/version.go
├── testdata/                    # output asli ss/systemctl/ufw/docker untuk test parser
├── Makefile  .gitignore  README.md  LICENSE
└── .github/workflows/ci.yml
```

Aturan ketergantungan (dijaga supaya mudah menambah modul ke-5):
`screens/*` → `sys/*` + `run` + `ui` + `i18n`. `sys/*` **tidak** import `ui`/`app`/bubbletea.

### Pola Bubble Tea: satu root model + screen stack

`internal/app` memegang `[]Screen`. `Screen` adalah interface kecil:

```go
type Screen interface {
    Init() tea.Cmd
    Update(tea.Msg) (Screen, tea.Cmd)
    View(w, h int) string
    Title() string              // untuk breadcrumb
    Keys() []key.Binding        // untuk footer + layar help
}
```

- Navigasi lewat pesan: `PushMsg{Screen}`, `PopMsg{Result any}`, `ReplaceMsg{Screen}`. Root yang
  memanipulasi stack, screen tidak pernah menyentuh stack langsung.
- **Mengembalikan hasil ke atas**: `PopMsg` membawa `Result any`. Root mem-pop, lalu mengirim
  `ResumedMsg{Result}` ke screen yang sekarang jadi puncak. Contoh: layar konfirmasi di-pop dengan
  `ConfirmResult{Approved: true}`, layar Ports menerimanya dan menjalankan command.
- `tea.WindowSizeMsg` disimpan di root dan diteruskan ke seluruh stack (bukan hanya puncak) supaya
  layar di bawah tetap benar ukurannya saat di-pop.
- Keybinding global ditangani root **sebelum** diteruskan: `esc` back, `q`/`ctrl+c` quit,
  `?` help overlay, `r` refresh. `/` (filter) sengaja diteruskan ke screen.

### ★ Lapisan eksekusi (`internal/run`) — inti dari model keamanan & belajar

```go
type Danger int // Safe, Caution, Dangerous

type Command struct {
    Argv        []string // {"kill","-TERM","1043"} — TANPA sudo
    NeedsRoot   bool     // kalau true dan kita bukan root → prefix "sudo"
    Title       string   // "Hentikan proses nginx (PID 1043)"
    Explain     []Line   // penjelasan per-token: {"-TERM", "sinyal 'berhenti baik-baik'"}
    Effect      string   // "Port 80 jadi kosong. Kalau dikelola systemd bisa hidup lagi."
    Safer       string   // saran alternatif yang lebih aman (boleh kosong)
    Danger      Danger
    Interactive bool     // butuh TTY penuh: docker run -it, docker exec -it, sudo password
}

func (c Command) Preview() string  // persis seperti yang diketik user, quoting rapi
```

`Runner` interface supaya bisa di-fake saat test:

```go
type Runner interface {
    Capture(ctx context.Context, c Command) (stdout, stderr string, err error)
}
```

**Command interaktif** (sudo yang minta password, `docker run -it`, `docker exec -it`) tidak bisa
lewat `Capture` — TUI memegang terminal. Solusinya `tea.ExecProcess`, yang menghentikan renderer,
mengembalikan terminal ke child process, lalu merestore TUI setelah child selesai:

```go
// Perlu diverifikasi saat implementasi terhadap bubbletea v1.3.10:
//   func ExecProcess(c *exec.Cmd, fn ExecCallback) Cmd
//   type ExecCallback func(error) Msg
cmd := exec.Command(argv[0], argv[1:]...)
return tea.ExecProcess(cmd, func(err error) tea.Msg { return ExecDoneMsg{Cmd: c, Err: err} })
```

Karena `sudo` akan minta password di layar bersih, **semua command `NeedsRoot` diperlakukan sebagai
interaktif** — lebih sederhana dan selalu benar. Sebelum menjalankan, `sudo.go` menjalankan
`sudo -n true` untuk tahu apakah kredensial masih ter-cache, lalu menampilkan di layar konfirmasi
"sudo akan meminta password" atau tidak.

`history.go` menulis setiap command yang **benar-benar dieksekusi** (beserta exit code) ke
`~/.config/ubt/history.log` — menjadi sumber menu "Riwayat perintah" di home.

### Pengumpulan data per modul

**Ports** (`sys/ports`) — native Go, tanpa dependensi eksternal:
1. Parse `/proc/net/tcp`, `tcp6`, `udp`, `udp6` (lewati file yang tidak ada). Ambil baris dengan
   state `0A` (LISTEN) untuk TCP; untuk UDP ambil semua socket ber-bind. Alamat/port heksadesimal
   little-endian — butuh test khusus untuk pembacaan ini (v4 dan v6 beda susunan word).
2. Bangun peta `inode → PID` dengan menelusuri `/proc/*/fd/*`; symlink socket berbentuk
   `socket:[12345]`. `readlink` proses milik user lain gagal dengan EACCES bila bukan root —
   **degrade dengan anggun**: baris tetap tampil dengan PID `?` plus hint di footer
   "jalankan `sudo ubt` untuk melihat pemilik semua port".
3. Fallback opsional `ss -tulpnH` bila tersedia dan hasil native kosong.
4. UI tetap menampilkan "command setara: `sudo ss -tulpn`" sebagai bahan belajar.

**Proses** (`sys/procs`): `/proc/PID/exe` & `cwd` (readlink), `cmdline` (dipisah NUL),
`status` (Name/Uid/Gid/PPid → username via `os/user.LookupId`), `stat` (start time),
`cgroup` → unit systemd (baris `0::/system.slice/nginx.service`, juga tangani pola
`user@1000.service/…` dan cgroup v1 lama).

**Systemd** (`sys/systemd`):
- list: `systemctl list-units --type=service --all --no-legend --plain --full --no-pager`
  (5 kolom: UNIT LOAD ACTIVE SUB DESCRIPTION; deskripsi boleh mengandung spasi ⇒ split maksimal 5).
- detail: `systemctl show UNIT --property=Id,Description,LoadState,ActiveState,SubState,UnitFileState,MainPID,ExecStart,WorkingDirectory,User,Group,FragmentPath,Environment,Restart` → `Key=Value` per baris.
- log: `journalctl -u UNIT -n 100 --no-pager -o short-iso` ke viewport.
- aksi: start/stop/restart/reload/enable/disable (semua `NeedsRoot`).
- Deteksi "systemd bukan PID 1" (pesan *System has not been booted with systemd*) dan tampilkan
  empty state yang jelas, bukan error mentah.

**ufw** (`sys/ufw`): `ufw status numbered` dan `ufw status verbose` — **keduanya butuh root**,
jadi layar Firewall langsung memakai jalur sudo bahkan untuk membaca. Parse blok `[ 1] 22/tcp
ALLOW IN Anywhere`. Aksi: `ufw allow <port>/<proto>`, `ufw deny`, `ufw delete <num>`,
`ufw enable`/`disable`, `ufw reset`.
**Pengaman anti-terkunci**: sebelum `ufw enable`, atau sebelum deny/delete apa pun yang menyentuh
port SSH aktif (dideteksi dari `$SSH_CONNECTION` dan dari daftar port listening), tampilkan
peringatan merah bahwa sesi SSH bisa terputus, dan sarankan `sudo ufw allow OpenSSH` lebih dulu.

**Docker** (`sys/docker`): selalu `--format '{{json .}}'`, satu objek JSON per baris.
- ketersediaan: `docker info --format '{{json .ServerVersion}}'`. Bedakan tiga kegagalan —
  binary tidak ada (saran `apt install docker.io`), daemon mati (saran
  `sudo systemctl start docker`), dan permission denied pada socket (saran
  `sudo usermod -aG docker $USER` + logout/login). Ini yang paling sering menjegal pemula.
- `docker images`, `docker ps -a`, `docker pull`, `docker logs`, `docker stop/rm`,
  `docker commit <ctr> <image>`, `docker system prune`.
- **Shell interaktif**: `docker run -it --rm <image> /bin/bash` dan
  `docker exec -it <ctr> /bin/bash` lewat `tea.ExecProcess`; sediakan fallback ke `/bin/sh`
  saat bash tidak ada di image.
- **Wizard generate file**: form huh mengumpulkan image/port/volume/env/nama service, lalu
  merender `Dockerfile` / `docker-compose.yml` dari `text/template`. File ditulis hanya setelah
  layar konfirmasi yang sama (preview isi file + path tujuan), dan **tidak pernah menimpa** file
  yang sudah ada tanpa persetujuan eksplisit.
- **Jalankan modul Python di container**: form menanyakan image (default `python:3.12-slim`),
  direktori host untuk di-mount, dan nama modul → `docker run -it --rm -v <dir>:/app -w /app
  <image> python -m <modul>`.

### Tampilan

Layout konsisten tiga bagian: header (judul + breadcrumb + `user@host` + badge `root`/`non-root`),
body (tabel/list/detail/form), footer (keybinding kontekstual). Warna per level bahaya:
hijau *aman*, kuning *hati-hati*, merah *bahaya*. Setiap layar punya empty state & error state yang
menyebut penyebab + saran perbaikan. Contoh layar konfirmasi (inti tool ini):

```
┌─ Konfirmasi ────────────────────────────────────────────┐
│ ⚠  BERISIKO                                             │
│ Perintah yang akan dijalankan:                          │
│     sudo kill -TERM 1043                                │
│ Artinya:                                                │
│   kill   → kirim sinyal ke sebuah proses                │
│   -TERM  → sinyal "tolong berhenti baik-baik"           │
│   1043   → PID dari nginx                               │
│ Efek: nginx berhenti, port 80 kosong. Bila dikelola     │
│   systemd, ia bisa start otomatis lagi.                 │
│ Lebih aman: hentikan unit-nya → sudo systemctl stop …   │
│ sudo akan meminta password kamu.                        │
│   [ y Jalankan ]   [ n Batal ]   [ c Salin command ]    │
└─────────────────────────────────────────────────────────┘
```

**Non-TTY**: `ubt` polos tanpa TTY → pesan jelas "butuh terminal interaktif; pakai `ubt ports
--json` untuk output yang bisa di-pipe", exit code 1. `--json` dan `doctor` tetap jalan tanpa TTY.

---

## Rencana eksekusi (bertahap, tiap fase bisa dijalankan)

0. **Commit plan dulu** *(diminta user — dikerjakan pertama)* — tulis dokumen ini ke repo sebagai
   `docs/PLAN.md`, commit, dan push ke branch `claude/exciting-mendel-pfvegk`. Ini menjadi commit
   pertama repo sekaligus rujukan saat implementasi berjalan.
1. **Fondasi** — `go mod init github.com/arif-rachim/ubuntu-tool` (Go 1.23), `.gitignore`,
   `Makefile` (build/install/test/lint/fmt, `-ldflags` inject versi), `README.md`, CI GitHub
   Actions (build linux/amd64 + linux/arm64, `go vet`, `go test ./...`).
2. **Kerangka TUI** — `app` (stack+keymap), `ui` (theme/layout/picker/datatable/detail/status),
   `i18n`, layar `home` dengan 6 menu (5 di antaranya masih placeholder). Sudah bisa `make run`.
3. **Lapisan eksekusi** — `run/*` lengkap termasuk `ui/confirm.go` dan `tea.ExecProcess`.
   Uji dengan satu aksi nyata yang aman (`systemctl --version`).
4. **Modul Ports & Proses** — `sys/ports` + `sys/procs` + layar list/detail + aksi kill
   (TERM lalu KILL) dan "stop unit systemd pemiliknya". Ini modul dengan nilai tertinggi.
5. **Modul Systemd** — list/filter, detail, log journalctl, aksi lifecycle.
6. **Modul Firewall** — status, wizard allow/deny, delete by number, enable/disable + pengaman SSH.
7. **Modul Docker** — pengecekan ketersediaan, images, containers, shell interaktif, commit,
   wizard Dockerfile/compose, runner modul Python.
8. **Pemolesan** — `ubt doctor`, `ubt ports --json`, `ubt version`, layar Riwayat, help overlay,
   README dengan cara install (`curl` binary rilis + `make install`), dan skrip rilis.

Tiap fase = satu commit yang jelas di branch `claude/exciting-mendel-pfvegk`.

## Strategi test

Karena hampir semuanya memanggil program luar, kuncinya memisahkan **parser murni** dari **I/O**:

- `testdata/` menyimpan output asli: `ss-tulpn.txt`, `proc-net-tcp.txt`, `systemctl-list-units.txt`,
  `systemctl-show-nginx.txt`, `ufw-status-numbered.txt`, `docker-ps-json.txt`, `docker-images-json.txt`.
- Test table-driven untuk parser yang paling rawan: hex address `/proc/net/tcp` (v4 **dan** v6),
  pemetaan inode→PID, kolom `list-units` yang deskripsinya berspasi, `Key=Value` dari
  `systemctl show` (nilai boleh kosong/mengandung `=`), rule bernomor ufw, dan JSON-per-baris docker.
- `run.Command.Preview()` diuji: quoting argumen berspasi, dan prefix `sudo` muncul tepat saat
  `NeedsRoot && !isRoot`.
- `fakeRunner` mengembalikan isi `testdata/` sehingga lapisan `sys/*` bisa diuji tanpa sistem nyata.
- Logika TUI tidak diuji unit; verifikasi manual (lihat di bawah).

## Verifikasi

Otomatis:
```bash
make fmt && go vet ./... && go test ./... && make build
./bin/ubt version && ./bin/ubt doctor && ./bin/ubt ports --json | head
./bin/ubt < /dev/null        # harus memberi pesan non-TTY yang jelas, bukan panic
```

Manual di Ubuntu 24.04 nyata (jalankan `./bin/ubt`):
1. **Ports** — jalankan `python3 -m http.server 8080 &`, buka menu Ports, pastikan 8080 muncul
   lengkap dengan PID/user/exe/cwd/cmdline; tekan kill, **verifikasi layar konfirmasi menampilkan
   `kill -TERM <pid>`**, setujui, pastikan port hilang setelah refresh.
2. **Sudo** — lakukan aksi yang butuh root sebagai user biasa; pastikan TUI menghilang rapi,
   prompt password sudo muncul, lalu TUI kembali utuh tanpa layar rusak.
3. **Systemd** — buka `ssh.service`, cek ExecStart/WorkingDirectory/User terbaca, log tampil,
   lakukan `restart` lewat konfirmasi.
4. **Firewall** — `ufw status`, tambah `allow 8080/tcp`, lihat rule bernomor, hapus lagi.
   Pastikan peringatan SSH muncul saat mencoba `ufw enable` dari sesi SSH.
5. **Docker** — pull `alpine`, jalankan shell interaktif, buat file di dalamnya, keluar, `commit`
   jadi image baru, lalu generate `docker-compose.yml` lewat wizard dan periksa isinya.
6. **Degradasi** — sementara samarkan `ufw`/`docker` dari `PATH` dan pastikan tool memberi saran
   `apt install`, bukan crash (kondisi ini persis seperti container dev sekarang).
