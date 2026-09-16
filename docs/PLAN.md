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
| Scope v1 | Dua belas modul: **Ports & Proses, Firewall (ufw), Systemd, Docker** + (ditambah kemudian) **Resource, Disk & Storage, Log, Network, User & SSH, Paket (apt), Penjadwalan (cron & timer), Web & TLS**, plus menu **Diagnosa** berbasis gejala dan **ekspor riwayat jadi script** |
| Arah produk | Asisten interaktif yang *membantu* user menyelesaikan masalah server sehari-hari, sekaligus jembatan menuju automation (command yang dipelajari bisa diekspor jadi script) |
| Safety | **Tampilkan command persis + penjelasan + konfirmasi** sebelum setiap aksi mutasi |
| Sudo | **Auto-prefix `sudo`** saat aksi butuh root; sudo sendiri yang minta password di terminal |
| Non-TUI | Minimal: `ubt ports --json`, `ubt version`, `ubt doctor`; tetap waras saat di-pipe |

### Temuan environment (sudah diverifikasi di container Ubuntu 24.04.4 ini)

- ~~`systemctl list-units` **tidak punya** `--json`.~~ **Dikoreksi 2026-09-16:** di systemd
  `255.4-1ubuntu8.17` (Ubuntu 24.04.5), `systemctl list-units --output=json` dan
  `systemctl list-timers --output=json` **terbukti jalan** (array objek `unit/load/active/sub/description`).
  ⇒ pakai JSON sebagai jalur utama; parse kolom (`--no-legend --plain --full --no-pager`) tetap
  disimpan sebagai fallback bila JSON gagal di-decode. Detail unit tetap lewat
  `systemctl show UNIT --property=...` yang outputnya `Key=Value` (stabil, aman di-parse).
- `/proc/net/tcp`, `/proc/PID/{exe,cwd,cmdline,status,cgroup,fd}` semuanya tersedia dan terbaca.
  `/proc/net/tcp6` & `udp6` **bisa tidak ada** (IPv6 mati) — kode wajib tahan file hilang.
- Di container ini `ss`, `ufw`, `ip` **tidak terinstall**, dan docker daemon tidak jalan. Ini bukti
  konkret bahwa tool harus mendeteksi binary/daemon yang hilang dan memberi pesan + saran
  `apt install`, bukan crash. ⇒ perintah `ubt doctor`.
- Go module proxy reachable. Versi terbaru: `bubbletea v1.3.10`, `bubbles v1.0.0`,
  `lipgloss v1.1.0`, `huh v1.0.0`.

### Temuan environment kedua (Ubuntu 24.04.5 desktop, dicek 2026-09-16 untuk modul tambahan)

- Tersedia: `df`, `du`, `lsblk`, `findmnt`, `free`, `vmstat`, `ps`, `journalctl`, `ip`, `ss`, `ping`,
  `dig`, `resolvectl`, `curl`, `tracepath`, `nc`, `getent`, `adduser`, `usermod`, `chage`, `ssh`,
  `ssh-keygen`, `last`, `lastlog`, `who`, `w`, `visudo`, `logrotate`.
- **Tidak ada**: `ncdu`, `traceroute`, `sshd` (openssh-server belum terinstall). ⇒ pakai `tracepath`
  sebagai default, `du` di-drill-down native, dan modul SSH server harus mendeteksi paket hilang.
- Output JSON yang **terbukti jalan**: `ip -j addr`, `ip -j route`, `lsblk --json -b`,
  `findmnt --json --df`, `journalctl -o json` (satu objek per baris). Pakai ini, jangan parse teks.
- `resolvectl status` hanya teks (mode stub `127.0.0.53`). `/proc/{meminfo,loadavg,stat}` dan
  `/proc/pressure/{cpu,io,memory}` (PSI) tersedia.
- User biasa di grup `adm` bisa membaca journal sistem dan `/var/log/auth.log` tanpa sudo — bila tidak,
  sarankan `sudo usermod -aG adm $USER`, bukan langsung sudo.
- Paket: `apt 2.8.3`, `dpkg-query`, `apt-cache`, `snap`, `unattended-upgrade` tersedia;
  `needrestart` **tidak ada**. Auto-upgrade aktif via `/etc/apt/apt.conf.d/20auto-upgrades`.
  Sumber repo pakai format deb822 (`/etc/apt/sources.list.d/ubuntu.sources`), bukan `sources.list`.
- Penjadwalan: `crontab`, `anacron`, `systemd-analyze` tersedia. `systemd-analyze calendar "<ekspresi>"`
  memvalidasi & menampilkan "Next elapse" — sempurna untuk preview jadwal timer. User belum punya
  crontab (`no crontab for developer` harus ditangani sebagai kosong, bukan error).
- Web/TLS: `openssl 3.0.13` tersedia (`x509 -enddate`, `-checkend`). `nginx`, `apache2`, `caddy`,
  `certbot` **tidak terinstall** ⇒ modul Web harus mendeteksi web server mana yang ada, dan tetap
  berguna tanpa satu pun (cek sertifikat domain remote tetap bisa).

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
│   │   ├── ask/                 #   ★ pertanyaan interaktif gaya Claude Code (lihat bagian khusus)
│   │   │   ├── question.go      #     tipe Question/Option/Answers — data murni, tanpa rendering
│   │   │   ├── model.go         #     state machine: fokus, pilihan, "Lainnya…", navigasi antar pertanyaan
│   │   │   ├── view.go          #     render opsi+penjelasan, chip header, panel preview, ringkasan
│   │   │   └── keys.go          #     ↑↓ 1-9 space enter tab esc
│   │   └── status.go            #   spinner, toast, empty state, error state
│   ├── i18n/strings.go          # semua teks Indonesia di satu tempat (siap ditambah EN nanti)
│   ├── run/                     # ★ lapisan eksekusi
│   │   ├── command.go           #   type Command{Argv, NeedsRoot, Title, Explain, Danger, Interactive}
│   │   ├── runner.go            #   interface Runner + real + fake (untuk test)
│   │   ├── sudo.go              #   deteksi root, prefix sudo, cek `sudo -n true`
│   │   ├── interactive.go       #   integrasi tea.ExecProcess (lepas & rebut kembali terminal)
│   │   ├── plan.go              #   Plan = beberapa Command berurutan, satu konfirmasi, stop saat gagal
│   │   ├── stream.go            #   command berjalan lama → baris demi baris ke UI (journalctl -f, du)
│   │   ├── history.go           #   log ke ~/.config/ubt/history.log
│   │   └── export.go            #   riwayat → script bash (set -euo pipefail + komentar penjelasan)
│   ├── sys/                     # ★ collector murni — TIDAK boleh import bubbletea
│   │   ├── ports/               #   parse /proc/net/*, map inode→PID, fallback `ss`
│   │   ├── procs/               #   /proc/PID/{exe,cwd,cmdline,status,cgroup}
│   │   ├── resource/            #   /proc/{loadavg,meminfo,stat,pressure}, sampling CPU per proses
│   │   ├── disk/                #   findmnt/lsblk JSON, df -i, walker du native, file terhapus-tapi-terbuka
│   │   ├── logs/                #   journalctl -o json, daftar & tail /var/log
│   │   ├── netinfo/             #   ip -j, resolvectl, cek konektivitas bertahap
│   │   ├── users/               #   getent passwd/group, sudoers.d, who/last/lastlog
│   │   ├── ssh/                 #   ~/.ssh, authorized_keys, `sshd -T`, drop-in config
│   │   ├── packages/            #   dpkg-query, apt list --upgradable, apt policy, snap, reboot-required
│   │   ├── schedule/            #   crontab (user & /etc/cron.*), list-timers JSON, systemd-analyze calendar
│   │   ├── web/                 #   deteksi nginx/apache/caddy, `nginx -T`, certbot, cek TLS native (crypto/tls)
│   │   ├── systemd/             #   list-units, show, journalctl
│   │   ├── ufw/                 #   `ufw status numbered|verbose`
│   │   └── docker/              #   docker CLI dengan `--format '{{json .}}'`
│   ├── diagnose/                # wizard berbasis gejala; merangkai sys/* lintas modul
│   ├── screens/                 # satu package per modul
│   │   ├── home/  ports/  resource/  disk/  logs/  network/  users/  packages/  schedule/  web/
│   │   ├── firewall/  services/  dockerui/  diagnose/  history/
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
- **Kecuali saat user sedang mengetik**: `Screen` punya method opsional `Typing() bool`. Bila true
  (filter aktif, text input `ui/ask`, input host/port), hanya `ctrl+c` yang global; `q`/`r`/`?` diteruskan
  sebagai huruf biasa. Modul baru penuh input teks (host, username, path), jadi ini wajib.

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
    Stdin       string   // isi yang dialirkan ke stdin, mis. menulis file root lewat `sudo tee`
    StdinLabel  string   // yang ditampilkan di preview: "(isi public key, 1 baris)" — jangan dump rahasia
}

func (c Command) Preview() string  // persis seperti yang diketik user, quoting rapi

// Plan: beberapa langkah yang harus jalan berurutan (buat swapfile, ganti port SSH, pasang cron).
// Layar konfirmasi menampilkan SEMUA langkah bernomor sekaligus, satu persetujuan, berhenti di langkah
// pertama yang gagal dan melaporkan langkah mana yang sudah terlanjur jalan.
type Plan struct {
    Title string
    Steps []Command
    Check *Command // opsional: validasi sebelum langkah terakhir (sshd -t, nginx -t, visudo -c)
}
```

**Menulis file sistem** (sshd drop-in, cron.d, nginx site, sudoers) tidak pernah lewat `os.WriteFile`
langsung: selalu Plan `sudo install -m MODE /dev/stdin PATH` (atau tulis ke file sementara lalu
`install`) + langkah validasi + backup `PATH.ubt-bak-<timestamp>` bila file sudah ada. Dengan begitu
semuanya tampil sebagai command asli, tercatat di riwayat, dan bisa diekspor.

**Stream** (`stream.go`): untuk command berjalan lama yang outputnya perlu tampil bertahap
(`journalctl -f`, scan `du`, `apt upgrade`, `ping`). Goroutine membaca stdout per baris → `tea.Msg`,
dibatalkan lewat `context` saat user menekan `esc`. Buffer dibatasi (mis. 5.000 baris terakhir).

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
`~/.config/ubt/history.log` — menjadi sumber menu "Riwayat perintah" di home. Format JSON-per-baris
(waktu, argv, needsRoot, title, exit code, isi stdin **hanya bila bukan rahasia**).

**Ekspor jadi script** (`export.go`) — jembatan dari "klik" ke automation: di layar Riwayat user
memilih beberapa entri sukses → `ubt` menghasilkan `.sh` dengan `#!/usr/bin/env bash`,
`set -euo pipefail`, komentar berisi `Title` + `Effect` per langkah, dan `sudo` hanya di langkah yang
memerlukannya. File ditulis lewat layar konfirmasi yang sama, tidak menimpa tanpa izin.
Juga tersedia non-TUI: `ubt history export --last 5 > setup.sh`.

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
- list: `systemctl list-units --type=service --all --no-pager --output=json` (jalur utama).
  Fallback: `--no-legend --plain --full --no-pager` (5 kolom: UNIT LOAD ACTIVE SUB DESCRIPTION;
  deskripsi boleh mengandung spasi ⇒ split maksimal 5).
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
- **Wizard generate file**: `ui/ask` mengumpulkan image/port/volume/env/nama service, lalu
  merender `Dockerfile` / `docker-compose.yml` dari `text/template`. File ditulis hanya setelah
  layar konfirmasi yang sama (preview isi file + path tujuan), dan **tidak pernah menimpa** file
  yang sudah ada tanpa persetujuan eksplisit.
- **Jalankan modul Python di container**: form menanyakan image (default `python:3.12-slim`),
  direktori host untuk di-mount, dan nama modul → `docker run -it --rm -v <dir>:/app -w /app
  <image> python -m <modul>`.

> Catatan: `sudo` membaca password dari `/dev/tty`, bukan stdin, jadi `Command.Stdin` tetap aman
> dipakai bersama `sudo` di jalur `tea.ExecProcess`. Tetap diverifikasi di fase lapisan eksekusi.

---

### Modul tambahan

Prinsip untuk semua modul tambahan: **baca dulu, ubah belakangan**. Tiap modul dibangun read-only
lebih dulu (langsung bernilai, risiko nol), baru aksi mutasi lewat layar konfirmasi. Setiap layar
baca menampilkan baris *"command setara: `…`"* supaya user tetap belajar walau tidak mengeksekusi apa pun.

**Resource** (`sys/resource`) — "server saya lambat, kenapa?"
- Native dari `/proc`: `loadavg`, `meminfo`, `stat` (CPU% dari selisih dua sampel), `pressure/{cpu,io,memory}`
  (PSI), `uptime`, jumlah core. Auto-refresh tiap 2 detik lewat `tea.Tick`, bisa di-pause.
- Top proses by CPU / RAM: selisih `utime+stime` dari `/proc/PID/stat`, `VmRSS` dari `status`.
  Command setara: `top`, `free -h`, `uptime`, `ps aux --sort=-%cpu | head`.
- **Penjelasan manusia** (ini nilai utamanya): load average dibanding jumlah core ("load 4.0 di 2 core
  = antrian 2× kapasitas"); pakai `MemAvailable` bukan `MemFree` dan jelaskan "RAM terpakai untuk
  cache itu normal"; swap terpakai + PSI memory tinggi = benar-benar kurang RAM; PSI io tinggi = disk
  jadi bottleneck (arahkan ke modul Disk).
- Riwayat OOM killer: `journalctl -k -b --grep 'Out of memory|oom-kill'` → "proses X pernah dibunuh
  kernel karena RAM habis". (Verifikasi `--grep` tersedia di build journalctl Ubuntu.)
- Aksi dari baris proses: kill (pakai ulang alur Ports), `renice +10 -p PID`, buka unit systemd pemiliknya.

**Disk & Storage** (`sys/disk`) — "disk penuh, apa yang makan tempat?"
- Ringkasan filesystem: `findmnt --json --df -b` (terverifikasi). Sembunyikan pseudo-fs (`tmpfs`,
  `squashfs` snap, `overlay`, `devtmpfs`) di balik toggle. Kuning ≥80%, merah ≥90%.
- **Inode**: `df -i` — jebakan klasik "disk 40% tapi tidak bisa tulis file" karena inode habis.
- Block device: `lsblk --json -b -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,ROTA` (disk mana SSD/HDD,
  mana belum di-mount). `MOUNTPOINTS` jamak di util-linux 2.39 — verifikasi saat implementasi.
- **Penjelajah "apa yang makan tempat"**: walker native Go (`filepath.WalkDir`, tetap di satu
  filesystem seperti `du -x`, bisa dibatalkan, progres streaming), drill-down per direktori, urut
  terbesar. Direktori yang EACCES ditandai, hint `sudo ubt`. Command setara:
  `sudo du -xh -d1 /var | sort -h`.
- Cari file besar: `find / -xdev -type f -size +100M` (native, sama seperti walker).
- **File terhapus tapi masih dibuka proses** (df bilang penuh, du tidak menemukan): pindai
  `/proc/*/fd` yang target symlink-nya berakhiran `(deleted)`, pakai ulang `sys/procs`. Solusi yang
  disarankan: restart service pemiliknya, bukan hapus file.
- **Bersih-bersih terpandu** (tiap kandidat menampilkan perkiraan ruang yang kembali):
  `apt clean`, `apt autoremove --purge`, `journalctl --vacuum-size=200M`, revisi snap nonaktif
  (`snap list --all` → `snap remove NAME --revision=N`), `docker system prune` (link ke modul Docker),
  log rotasi lama `/var/log/*.gz`. Tidak pernah menawarkan `rm -rf` bebas.
- Swap: `swapon --show`; wizard **buat swapfile** sebagai Plan: `fallocate -l 2G /swapfile` →
  `chmod 600` → `mkswap` → `swapon` → tambah baris ke `/etc/fstab` (backup dulu). Sangat umum di VPS kecil.
- v1 **tidak** menyediakan partisi/format/mount disk baru (terlalu mudah merusak data); cukup tampilkan
  `lsblk` + penjelasan + link dokumentasi. Kandidat v2.

**Log** (`sys/logs`) — "ada error apa di server ini?"
- Sumber utama journal: `journalctl -o json` (satu objek per baris, terverifikasi). Parser wajib tahan
  `MESSAGE` berupa **array byte** (bukan string) untuk pesan non-UTF-8, dan field yang hilang.
- Tampilan siap pakai: *Error sejak boot* (`-p err -b`), *Kernel* (`-k`), *Boot sebelumnya*
  (`-b -1` — "kenapa server restart tadi malam?"), per unit (dipakai juga modul Systemd), rentang waktu
  (`--since "1 hour ago"`), cari kata kunci (`--grep`). Warna per `PRIORITY`.
- Mode ikuti langsung: `journalctl -f -o json` lewat `run.Stream`.
- File `/var/log`: daftar file + ukuran, tail isinya (`auth.log`, `syslog`, `nginx/*`, `apt/history.log`).
  Tidak terbaca → sarankan `sudo usermod -aG adm $USER` (lebih tepat dari selalu sudo).
- Kesehatan journal: `journalctl --disk-usage`; cek `/var/log/journal` ada (kalau tidak, log hilang
  saat reboot — jelaskan); aksi vacuum.

**Network** (`sys/netinfo`) — "kenapa tidak bisa konek?"
- Info: `ip -j addr`, `ip -j route` (terverifikasi) → interface, IP, gateway default, status up/down.
  DNS: `resolvectl status` (teks) + penjelasan stub `127.0.0.53`. `/etc/hosts` read-only.
  Netplan `/etc/netplan/*.yaml` read-only (salah edit = server hilang dari jaringan; v1 hanya tampilkan
  + jelaskan `netplan try`).
- IP publik: aksi **opsional** yang menghubungi layanan luar (`curl -s https://ifconfig.me`) — hanya
  setelah konfirmasi karena mengirim request keluar.
- Koneksi aktif: pakai ulang parser `sys/ports` untuk state ESTABLISHED, dikelompokkan per IP remote
  (membantu melihat IP yang membanjiri server). Command setara: `ss -tnp state established`.
- ★ **Wizard "Tidak bisa konek ke X"** (keluar): input host/URL/port, lalu cek bertahap, tiap langkah
  menampilkan command setara + hasil + artinya, berhenti dan menjelaskan di langkah pertama yang gagal:
  1. interface up & punya IP → 2. `ping` gateway → 3. resolve DNS (`resolvectl query HOST`, pembanding
  `dig +short HOST @1.1.1.1` untuk membedakan DNS lokal rusak vs domain salah) → 4. `ping HOST`
  (ICMP boleh diblok — bukan vonis) → 5. port TCP (native `net.DialTimeout`, setara `nc -zv -w3 HOST PORT`)
  → 6. HTTP (`curl -sS -o /dev/null -w '%{http_code} %{time_total}' URL`) → 7. TLS (lihat modul Web).
  Rute: `tracepath HOST` (tersedia), `traceroute`/`mtr` bila terinstall.
- ★ **Wizard "Service saya tidak bisa diakses dari luar"** (masuk), lintas modul:
  1. apakah prosesnya listening? (Ports) → 2. **bind ke `127.0.0.1` bukan `0.0.0.0`** — kesalahan pemula
  nomor satu, jelaskan → 3. ufw mengizinkan port itu? (Firewall) → 4. test dari server sendiri ke IP
  publiknya → 5. ingatkan firewall di luar server (security group cloud / router) yang tidak bisa dicek `ubt`.

**User & SSH** (`sys/users`, `sys/ssh`) — "tambah user, pasang SSH key, amankan SSH"
- User: `getent passwd` (UID ≥1000 + root), grup, anggota `sudo`/`docker`/`adm`, shell, login terakhir
  (`lastlog`; tangani bila digantikan `lastlog2` di rilis mendatang), yang sedang login (`who`/`w`),
  riwayat login (`last`). Status terkunci `passwd -S` (butuh root).
- Aksi user: `adduser NAMA` (interaktif, minta password → `tea.ExecProcess`), `usermod -aG GRUP NAMA`
  (+ pengingat "berlaku setelah logout/login"), `passwd -l`/`-u`, `chage -l`, `deluser --remove-home`
  (Dangerous). **Pengaman**: tolak menghapus/mengunci user yang sedang dipakai, dan tolak mengeluarkan
  anggota terakhir grup `sudo`.
- sudoers: tampilkan `/etc/sudoers.d/*`; mengedit **hanya** lewat `visudo -f FILE` (interaktif) atau
  Plan tulis-file + `visudo -cf FILE` sebagai Check. Tidak pernah menulis sudoers tanpa validasi.
- SSH key (sisi klien, user saat ini): daftar key di `~/.ssh` + fingerprint (`ssh-keygen -lf`); wizard
  `ssh-keygen -t ed25519 -C EMAIL` (interaktif untuk passphrase); tampilkan public key untuk disalin;
  `ssh-copy-id USER@HOST`.
- `authorized_keys` per user: daftar key (tipe, komentar, fingerprint), tambah key (paste → validasi
  dengan `ssh-keygen -lf -` via Stdin sebelum ditulis), hapus key. **Perbaiki permission** satu klik
  (`~/.ssh` 700, `authorized_keys` 600, owner benar) — penyebab paling umum "key sudah dipasang tapi
  tetap minta password".
- SSH server: deteksi `openssh-server` (tidak ada di mesin uji → saran `apt install openssh-server`).
  Config efektif `sudo sshd -T` (`key value` huruf kecil) → checklist hardening dengan penjelasan:
  `permitrootlogin`, `passwordauthentication`, `pubkeyauthentication`, `port`, `maxauthtries`.
  Perubahan ditulis ke drop-in `/etc/ssh/sshd_config.d/60-ubt.conf` (bukan file utama), Plan:
  tulis → `sshd -t` (Check) → reload. **Ubuntu 24.04 memakai socket activation (`ssh.socket`)**: ganti
  port butuh `systemctl daemon-reload` + `systemctl restart ssh.socket`, bukan sekadar reload service —
  verifikasi saat implementasi.
- ★ **Pengaman anti-terkunci SSH** (sejalan dengan pengaman ufw):
  mematikan password auth → wajib ada minimal satu key valid di `authorized_keys` user sudo;
  ganti port → cek ufw sudah allow port baru, dan tambahkan otomatis ke Plan bila belum;
  selalu tampilkan "**jangan tutup sesi ini, tes login dari terminal baru dulu**".
- Audit: login gagal dari journal (`journalctl -u ssh --grep 'Failed password|Invalid user'`),
  dikelompokkan per IP + saran `fail2ban`.

**Paket** (`sys/packages`) — "install, update, dan amankan paket"
- Terinstall: `dpkg-query -W -f='${Package}\t${Version}\t${Installed-Size}\n'` (terverifikasi) — cari,
  urut ukuran terbesar. Snap: `snap list`.
- Cari & detail paket: `apt-cache search KATA`, `apt-cache policy PAKET` (versi terpasang vs kandidat,
  dari repo mana), `apt show PAKET`. Install/remove lewat konfirmasi; `apt remove` vs `apt purge`
  dijelaskan bedanya.
- Update: `apt update` (Stream) → `apt list --upgradable` → `apt upgrade` (Stream, interaktif karena
  bisa bertanya soal config). Tandai upgrade **keamanan** (sumber `*-security`) terpisah.
- `/var/run/reboot-required` + `reboot-required.pkgs` → banner "perlu reboot karena paket X"
  (tidak ada di mesin uji — tangani file hilang sebagai "tidak perlu"). Service yang perlu restart:
  pakai `needrestart` bila terinstall, bila tidak sarankan install.
- Auto-upgrade: baca `/etc/apt/apt.conf.d/20auto-upgrades` (terverifikasi aktif di mesin uji), tampilkan
  status + riwayat `/var/log/unattended-upgrades/`, aksi aktifkan (`dpkg-reconfigure -plow unattended-upgrades`, interaktif).
- Repo: daftar `/etc/apt/sources.list.d/*` — **format deb822 `.sources`** di 24.04 plus `.list` lama.
  Tambah repo pihak ketiga v1 hanya lewat `add-apt-repository ppa:…` dengan peringatan keamanan.
- Riwayat instalasi: `/var/log/apt/history.log` ("siapa install apa, kapan") — sangat berguna saat
  sesuatu tiba-tiba rusak setelah update.
- Paket rusak: deteksi dpkg terputus (`dpkg --audit`) → saran `sudo dpkg --configure -a` /
  `sudo apt --fix-broken install`. Lock apt terkunci (proses unattended-upgrades sedang jalan) →
  tunjukkan PID pemegang lock, jangan sarankan hapus file lock.

**Penjadwalan** (`sys/schedule`) — "jalankan backup tiap jam 2 pagi"
- Daftar terpadu semua jadwal dalam satu tabel: crontab user (`crontab -l`; "no crontab for" = kosong),
  crontab user lain (root), `/etc/crontab`, `/etc/cron.d/*`, `/etc/cron.{hourly,daily,weekly,monthly}`,
  dan systemd timer (`systemctl list-timers --all --output=json`, terverifikasi: next/last/unit/activates).
- **Penjelas ekspresi cron** native Go: `0 2 * * 1` → "setiap Senin pukul 02:00" + 5 jadwal
  berikutnya. Untuk timer: `systemd-analyze calendar "EKSPRESI"` (terverifikasi, memberi "Next elapse").
- ★ **Wizard buat jadwal**: pilih "tiap X menit / harian jam / mingguan / custom", command yang
  dijalankan, user. Tawarkan dua bentuk dan jelaskan bedanya (timer: log di journal, bisa
  `Persistent=true` saat server mati; cron: lebih sederhana):
  - cron → file `/etc/cron.d/ubt-NAMA` (bukan `crontab -e` agar bisa di-preview persis), ingatkan
    `PATH` minim di cron dan redirect output ke log.
  - systemd timer → Plan: tulis `NAMA.service` + `NAMA.timer` → `systemd-analyze verify` (Check) →
    `daemon-reload` → `enable --now NAMA.timer`.
- Aksi: jalankan sekarang untuk tes (`systemctl start NAMA.service` / jalankan command cron sebagai
  user-nya), lihat log terakhir (link modul Log), nonaktifkan, hapus.
- Deteksi masalah umum: jadwal cron yang command-nya tidak ditemukan / tidak executable, timer yang
  service terakhirnya gagal.

**Web & TLS** (`sys/web`) — "pasang domain + HTTPS untuk aplikasi saya"
- Deteksi web server: `nginx`, `apache2`, `caddy` (binary + status unit). Tidak ada satu pun (mesin uji)
  → empty state + wizard install nginx.
- nginx: daftar site `sites-available` vs `sites-enabled`, config efektif `nginx -T`, validasi
  `nginx -t`, enable/disable site (symlink), `reload`. Apache: `apache2ctl -S`, `a2ensite`/`a2dissite`,
  `apache2ctl configtest`.
- ★ **Wizard reverse proxy** (kasus paling sering: aplikasi di `localhost:3000` → domain): input domain,
  port aplikasi → render site nginx dari `text/template` (proxy_pass, header `Host`/`X-Forwarded-*`,
  websocket opsional) → Plan: tulis `sites-available/DOMAIN` → symlink → `nginx -t` (Check) → reload →
  cek ufw `Nginx Full` / 80+443 (link Firewall) → cek DNS domain mengarah ke IP server (link Network).
- ★ **HTTPS dengan Let's Encrypt**: deteksi `certbot` (tidak ada → saran `snap install --classic certbot`,
  cara resmi). Prasyarat dicek dulu: DNS domain → IP server ini, port 80 terbuka. Lalu
  `certbot --nginx -d DOMAIN` (interaktif, minta email & persetujuan ToS). Daftar sertifikat
  `certbot certificates`, uji perpanjangan `certbot renew --dry-run`, cek timer renew ada (link Penjadwalan).
- **Cek sertifikat** lokal & remote, native `crypto/tls`: issuer, SAN, tanggal kedaluwarsa (kuning <30
  hari, merah <7), rantai lengkap atau tidak, hostname cocok. Command setara:
  `openssl s_client -connect HOST:443 -servername HOST </dev/null | openssl x509 -noout -dates -subject -issuer`
  dan `openssl x509 -enddate -noout -in FILE` (terverifikasi tersedia). Berguna walau tanpa web server.
- Error yang diterjemahkan: `502 Bad Gateway` = aplikasi di belakang proxy mati / port salah (link Ports),
  `413` = `client_max_body_size`, konflik port 80 dengan apache (link Ports).

**Diagnosa** (`internal/diagnose`) — menu teratas di home, untuk user yang hanya tahu gejalanya.
Tiap gejala = urutan pemeriksaan read-only yang memakai `sys/*` lintas modul, lalu hasil ringkas
"yang ditemukan → artinya → langkah yang disarankan" dengan tombol loncat ke layar modul terkait
(aksi mutasi tetap lewat konfirmasi di modul tersebut):
- *Disk penuh* → Disk (fs, inode, file besar, file terhapus-tapi-terbuka, journal, apt cache, docker).
- *Server lambat* → Resource (load vs core, RAM/swap/PSI, top proses, OOM) → Disk (PSI io).
- *Tidak bisa konek ke internet/host* → wizard Network keluar.
- *Aplikasi/web saya tidak bisa diakses* → wizard Network masuk → Web (config, 502) → Firewall.
- *Service mati terus / crash loop* → Systemd (`NRestarts`, `Result`) → Log unit → OOM.
- *SSH: tidak bisa login / mau diamankan* → User & SSH (permission, key, sshd -T, login gagal).
- *Habis update, ada yang rusak* → Paket (riwayat apt, dpkg audit, reboot-required) → Log boot.
- *Cek kesehatan umum* → ringkasan semua modul: disk, RAM, update keamanan tertunda, reboot
  diperlukan, sertifikat hampir kedaluwarsa, unit failed, timer gagal, root login SSH.

### ★ Komponen pertanyaan interaktif (`ui/ask`) — gaya Claude Code

Semua interaksi "tanya user" di seluruh modul (wizard reverse proxy, swapfile, jadwal, tambah user,
allow port, pilih kandidat bersih-bersih, dll.) memakai **satu komponen** ini, supaya pengalamannya
seragam dan terasa seperti prompt pilihan di Claude Code: tiap opsi punya penjelasan, ada rekomendasi,
bisa pilih pakai angka, dan selalu ada jalan keluar "Lainnya…".

**Kenapa tidak cukup `huh`**: `huh.Option` hanya punya label + nilai, tanpa penjelasan per opsi, tanpa
panel preview, dan gaya visual form huh berbeda dari layout chip multi-pertanyaan. Mencampur dua gaya
membingungkan pemula. ⇒ `ui/ask` dibangun langsung di atas Bubble Tea + lipgloss, memakai
`bubbles/textinput`, `bubbles/textarea`, dan `bubbles/viewport` untuk bagian teks & preview.
**`huh` dikeluarkan dari dependensi** (keputusan ini menggantikan rujukan huh sebelumnya).

#### Tipe data

```go
type Kind int // Single, Multi, Text, TextArea, Confirm

type Option struct {
    Label       string    // "systemd timer"
    Value       string    // "timer"
    Description string    // "Log masuk journal, tetap jalan walau server sempat mati"
    Recommended bool      // dipindah ke urutan pertama + label "(Disarankan)"; maksimal satu
    Preview     string    // opsional: isi file/command yang akan dihasilkan bila opsi ini dipilih
    Danger      ui.Danger // opsi berisiko diberi warna + ikon, bukan warna saja
    Disabled    string    // alasan tidak bisa dipilih: "nginx belum terinstall" (tampil redup)
}

type Question struct {
    ID          string
    Header      string   // chip pendek ≤12 karakter: "Domain", "Port", "HTTPS"
    Prompt      string   // kalimat tanya lengkap, diakhiri "?"
    Help        string   // opsional, tampil saat `?` ditekan: penjelasan konsep untuk pemula
    Kind        Kind
    Options     []Option                                 // Single/Multi; Confirm memakai Ya/Tidak bawaan
    Load        func(ctx context.Context) ([]Option, error) // opsi dinamis: daftar user, unit, interface
    Other       bool     // tambah opsi "Lainnya…" yang berubah jadi text input
    Default     []string // nilai awal; bisa dihitung dari deteksi sistem (port yang sedang listening)
    Placeholder string
    Validate    func(string) error    // Text/TextArea/Other: "port harus 1-65535"
    Min, Max    int                   // Multi: jumlah minimum/maksimum pilihan
    When        func(Answers) bool    // pertanyaan kondisional: tanya email hanya bila HTTPS = ya
}

type Answer struct {
    Values []string // Single: 1 elemen; Multi: 0..n
    Other  string   // teks "Lainnya…" bila dipilih
    Text   string   // Text/TextArea
    Yes    bool     // Confirm
}
type Answers map[string]Answer

type Form struct {
    Title     string
    Questions []Question
    Review    bool // tampilkan layar ringkasan sebelum selesai (default true bila >1 pertanyaan)
}
```

Hasil dikirim lewat mekanisme stack yang sudah ada: `PopMsg{Result: ask.Result{Answers, Cancelled}}`.
Wizard lalu membangun `run.Command`/`run.Plan` dari `Answers` → **layar konfirmasi command**.
Alur baku semua wizard: **tanya (`ui/ask`) → ringkasan jawaban → preview command persis + penjelasan
(`ui/confirm`) → jalankan**.

#### Tampilan per jenis

Single choice (penjelasan di bawah tiap opsi, rekomendasi di atas):
```
 Mau pakai jadwal jenis apa?

 ❯ 1. systemd timer (Disarankan)
      Log masuk journal, tetap jalan walau server sempat mati saat jadwalnya
   2. cron
      Lebih sederhana dan umum, tapi PATH minim dan log harus diatur sendiri
   3. Lainnya…
      Ketik sendiri

 ↑↓ pilih · 1-3 langsung · enter lanjut · ? bantuan · esc kembali
```

Multiple choice (checkbox, angka/space untuk toggle, info tambahan per opsi):
```
 Bersihkan apa saja? (pilih satu atau lebih)

 ❯ [✓] 1. Cache apt                                  ~1,2 GB
          File .deb yang sudah terpasang; aman dihapus, bisa diunduh ulang
   [ ] 2. Journal lama (sisakan 200 MB)              ~850 MB
          Log sistem lama; log terbaru tetap ada
   [✓] 3. Paket yang tidak dipakai lagi              ~310 MB
          Dependensi yatim hasil uninstall sebelumnya
   [ ] 4. Image & container Docker tak terpakai  ⚠  ~4,0 GB
          Container yang berhenti ikut terhapus, periksa dulu

 Total dipilih: ~1,5 GB
 ↑↓ pindah · space/1-4 centang · a semua · enter lanjut · esc kembali
```

Text input (validasi langsung saat mengetik, pesan error berbahasa manusia):
```
 Domain untuk aplikasi ini?

   app.contoh.com▌
   ✗ Domain belum mengarah ke server ini (DNS → 203.0.113.9, server ini 198.51.100.4)
     Tetap bisa lanjut, tapi HTTPS Let's Encrypt akan gagal sampai DNS diperbaiki.

 enter lanjut · esc kembali
```

Boolean (tetap dua opsi berpenjelasan, bukan sekadar y/n polos; `y`/`n` langsung memilih):
```
 Aktifkan dukungan WebSocket?

 ❯ 1. Ya
      Perlu bila aplikasi memakai socket.io / live update
   2. Tidak (Disarankan)
      Cukup untuk website dan REST API biasa
```

Panel preview (muncul bila ada opsi ber-`Preview`; di samping bila lebar ≥100 kolom, di bawah bila
sempit; bisa di-scroll). Preview dirender dari **template yang sama** dengan file sungguhan, jadi persis:
```
 Pilih template config nginx?
                                      ┌ /etc/nginx/sites-available/app.contoh.com ┐
 ❯ 1. Reverse proxy biasa (Disarankan)│ server {                                   │
   2. Reverse proxy + WebSocket       │     listen 80;                             │
   3. Situs statis                    │     server_name app.contoh.com;            │
   4. Lainnya…                        │     location / {                           │
                                      │         proxy_pass http://127.0.0.1:3000;  │
                                      │         proxy_set_header Host $host;       │
                                      └────────────────────────────────────────────┘
```

Multi-pertanyaan: chip di atas menunjukkan posisi & status, bisa maju-mundur tanpa kehilangan jawaban,
diakhiri layar ringkasan:
```
 Reverse proxy baru       ✓ Domain   ✓ Port   ● HTTPS   ○ WebSocket   ○ Ringkasan
```
```
 Ringkasan
   Domain      app.contoh.com
   Port        3000   (terdeteksi: node, PID 2211)
   HTTPS       Ya, Let's Encrypt — email admin@contoh.com
   WebSocket   Tidak

 ❯ Lanjut lihat command   ·   e ubah jawaban   ·   esc batal
```

#### Perilaku

- Tombol: `↑↓`/`j k` pindah; `1`–`9` pilih langsung (Single: pilih + lanjut; Multi: toggle);
  `space` toggle; `a` pilih/lepas semua (Multi); `enter` lanjut; `tab`/`shift+tab` atau `←→` pindah
  pertanyaan; `?` tampilkan `Help`; `esc` ke pertanyaan sebelumnya, di pertanyaan pertama = batal
  (minta konfirmasi bila sudah ada jawaban). `ctrl+c` selalu keluar.
- "Lainnya…" dipilih → baris itu berubah jadi text input di tempat; `Typing()` bernilai true supaya
  `q`/`r`/`?` global tidak mencuri huruf.
- Opsi lebih dari 9 (pilih dari 300 paket, 150 unit) → `/` untuk filter, angka tidak berlaku setelah 9.
- `Load` menampilkan spinner; bila gagal, error state + saran (pakai `ui/status`), tidak crash.
- `Disabled` tetap ditampilkan redup dengan alasannya — pemula belajar *kenapa* opsi itu tidak ada.
- Opsi `Recommended` dihitung dari kondisi sistem, bukan statis (mis. "timer" disarankan kecuali user
  meminta crontab pribadi).
- Informasi tidak pernah hanya lewat warna: selalu ada penanda `❯ ✓ ● ○ ⚠ ✗`.
- Semua teks lewat `i18n`. Lebar minimum 60 kolom; penjelasan di-wrap, tidak terpotong.

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

Menu home dikelompokkan supaya 12 modul tidak membingungkan pemula. Baris ringkas di atas menu
diisi cek cepat read-only (disk, RAM, update keamanan, reboot) yang langsung bisa ditekan:

```
 ubt · developer@server · non-root                         ? bantuan
 Disk / 91% ⚠   RAM 62%   Load 0.4 (4 core)   3 update keamanan   perlu reboot

 🩺 Ada masalah?
    Diagnosa berdasarkan gejala
 🖥  Sistem
    Resource (CPU, RAM, load)      Disk & Storage      Log
    Service (systemd)              Penjadwalan (cron & timer)
    Paket (apt)
 🌐 Jaringan
    Network & konektivitas         Ports & Proses      Firewall (ufw)
    Web & TLS (nginx, HTTPS)
 🔑 Akses
    User & SSH
 📦 Container
    Docker
 📜 Riwayat perintah & ekspor script
```

Layar konfirmasi untuk **Plan** menampilkan semua langkah bernomor + langkah validasi, contoh ganti
port SSH: `1. tulis /etc/ssh/sshd_config.d/60-ubt.conf` → `2. ufw allow 2222/tcp` →
`3. sshd -t (validasi)` → `4. systemctl daemon-reload` → `5. systemctl restart ssh.socket`, dengan
peringatan merah "jangan tutup sesi ini".

**Non-TTY**: `ubt` polos tanpa TTY → pesan jelas "butuh terminal interaktif; pakai `ubt ports
--json` untuk output yang bisa di-pipe", exit code 1. `--json` dan `doctor` tetap jalan tanpa TTY.

---

## Rencana eksekusi (bertahap, tiap fase bisa dijalankan)

0. **Commit plan dulu** *(diminta user — dikerjakan pertama)* — tulis dokumen ini ke repo sebagai
   `docs/PLAN.md`, commit, dan push ke branch `claude/exciting-mendel-pfvegk`. Ini menjadi commit
   pertama repo sekaligus rujukan saat implementasi berjalan.
1. **Fondasi** *(selesai 2026-09-16: directive `go 1.25.0`, dibangun dengan toolchain go1.27.1;
   LICENSE menunggu keputusan user)* — `go mod init github.com/arif-rachim/ubuntu-tool`, `.gitignore`,
   `Makefile` (build/install/test/lint/fmt, `-ldflags` inject versi), `README.md`, CI GitHub
   Actions (build linux/amd64 + linux/arm64, `go vet`, `go test ./...`).
2. **Kerangka TUI** — `app` (stack+keymap+`Typing()`), `ui` (theme/layout/picker/datatable/detail/status),
   **`ui/ask` lengkap** (Single, Multi, Text, TextArea, Confirm, "Lainnya…", preview, chip
   multi-pertanyaan, ringkasan), `i18n`, layar `home` dengan menu berkelompok (semua modul masih
   placeholder). Tambah layar demo tersembunyi `ubt --demo-ask` untuk mencoba semua jenis pertanyaan.
   Sudah bisa `make run`.
3. **Lapisan eksekusi** — `run/*` lengkap: `Command` (+Stdin), `Plan` (+Check, stop saat gagal),
   `Stream`, `ui/confirm.go`, `tea.ExecProcess`, history JSON-per-baris. Uji dengan satu aksi nyata
   yang aman (`systemctl --version`) dan satu Plan dua langkah yang aman.
4. **Modul Ports & Proses** — `sys/ports` + `sys/procs` + layar list/detail + aksi kill
   (TERM lalu KILL) dan "stop unit systemd pemiliknya". Ini modul dengan nilai tertinggi.
5. **Modul Resource** — hampir seluruhnya read-only dari `/proc`, memakai ulang `sys/procs`. Cepat & aman.
6. **Modul Disk & Storage** — ringkasan fs/inode/lsblk, walker drill-down, file terhapus-tapi-terbuka,
   bersih-bersih terpandu, wizard swapfile (Plan pertama yang nyata).
7. **Modul Log** — journal JSON, tampilan siap pakai, follow (Stream pertama yang nyata), `/var/log`.
8. **Modul Systemd** — list/filter (JSON), detail, log (pakai ulang modul Log), aksi lifecycle.
9. **Modul Network** — info interface/route/DNS, koneksi aktif, wizard konek keluar & diakses dari luar.
10. **Modul Paket** — daftar/cari/detail, update & upgrade (Stream), reboot-required, auto-upgrade,
    riwayat apt, perbaikan dpkg.
11. **Modul Penjadwalan** — tabel terpadu cron + timer, penjelas ekspresi, wizard cron.d & timer.
12. **Modul User & SSH** — user/grup/sudo, SSH key & authorized_keys, perbaikan permission,
    hardening sshd lewat drop-in + pengaman anti-terkunci, audit login gagal.
13. **Modul Firewall** — status, wizard allow/deny, delete by number, enable/disable + pengaman SSH
    (dijadikan satu fungsi pengaman bersama dengan modul SSH).
14. **Modul Web & TLS** — deteksi web server, site nginx, wizard reverse proxy, certbot, cek sertifikat.
15. **Modul Docker** — pengecekan ketersediaan, images, containers, shell interaktif, commit,
    wizard Dockerfile/compose, runner modul Python.
16. **Diagnosa** — wizard berbasis gejala + "Cek kesehatan umum" + baris ringkas di home.
17. **Pemolesan** — `ubt doctor` (cek semua binary tiap modul + nama paket apt-nya:
    `dig`→`bind9-dnsutils`, `nc`→`netcat-openbsd`, `sshd`→`openssh-server`, `traceroute`, `ncdu`,
    `needrestart`, `nginx`, `certbot`), `ubt ports --json`, `ubt version`, layar Riwayat + ekspor
    script (`ubt history export`), help overlay, README (`curl` binary rilis + `make install`), skrip rilis.

Urutan sengaja menaruh modul yang **mayoritas read-only** (Resource, Disk, Log) sebelum modul yang
berisiko mengunci user dari server (User & SSH, Firewall), supaya lapisan eksekusi, Plan, dan Stream
sudah teruji di kasus yang aman.

Tiap fase = satu commit yang jelas di branch `claude/exciting-mendel-pfvegk`.

## Strategi test

Karena hampir semuanya memanggil program luar, kuncinya memisahkan **parser murni** dari **I/O**:

- `testdata/` menyimpan output asli: `ss-tulpn.txt`, `proc-net-tcp.txt`, `systemctl-list-units.txt`,
  `systemctl-show-nginx.txt`, `ufw-status-numbered.txt`, `docker-ps-json.txt`, `docker-images-json.txt`.
- Tambahan untuk modul baru: `proc-meminfo.txt`, `proc-stat-{a,b}.txt` (dua sampel), `proc-pressure-*.txt`,
  `findmnt-df.json`, `lsblk.json`, `df-i.txt`, `journalctl.json` (termasuk `MESSAGE` array byte),
  `ip-addr.json`, `ip-route.json`, `resolvectl-status.txt`, `getent-passwd.txt`, `authorized_keys`
  (key valid, komentar, baris rusak, opsi `from=`), `sshd-T.txt`, `dpkg-query.txt`,
  `apt-list-upgradable.txt`, `apt-history.log`, `crontab-*.txt`, `list-timers.json`,
  `nginx-T.txt`, `certbot-certificates.txt`, sertifikat uji self-signed (valid, kedaluwarsa, hostname salah).
- Test table-driven untuk parser yang paling rawan: hex address `/proc/net/tcp` (v4 **dan** v6),
  pemetaan inode→PID, `list-units` JSON **dan** fallback kolom yang deskripsinya berspasi, `Key=Value` dari
  `systemctl show` (nilai boleh kosong/mengandung `=`), rule bernomor ufw, JSON-per-baris docker,
  perhitungan CPU% dari dua sampel, penjelas ekspresi cron (`*/15`, `1-5`, `@daily`, nama hari),
  `/etc/cron.d` (kolom user), `sshd -T`, deb822 `.sources`, dan pesan journal non-UTF-8.
- Pengaman diuji sebagai fungsi murni: tolak hapus user aktif / anggota sudo terakhir, tolak matikan
  password auth tanpa key, Plan ganti port SSH wajib menyertakan `ufw allow` bila ufw aktif.
- `run.Command.Preview()` diuji: quoting argumen berspasi, dan prefix `sudo` muncul tepat saat
  `NeedsRoot && !isRoot`. `run.Plan` diuji dengan fakeRunner: berhenti di langkah gagal, Check gagal
  mencegah langkah berikutnya. Ekspor script diuji dengan golden file.
- `fakeRunner` mengembalikan isi `testdata/` sehingga lapisan `sys/*` bisa diuji tanpa sistem nyata.
- `ui/ask` **diuji unit** (pengecualian, karena dipakai semua wizard): kirim `tea.KeyMsg` ke model lalu
  cek state — angka memilih opsi yang benar, `Recommended` naik ke atas, "Lainnya…" mengaktifkan
  `Typing()`, `Validate` memblokir lanjut, `Min/Max` Multi ditegakkan, `When` melewati pertanyaan,
  `esc` mundur tanpa menghapus jawaban, `Disabled` tidak bisa dipilih. Render diuji dengan golden file
  pada lebar 60 dan 120 kolom.
- Logika TUI lainnya tidak diuji unit; verifikasi manual (lihat di bawah).

## Verifikasi

Otomatis:
```bash
make fmt && go vet ./... && go test ./... && make build
./bin/ubt version && ./bin/ubt doctor && ./bin/ubt ports --json | head
./bin/ubt < /dev/null        # harus memberi pesan non-TTY yang jelas, bukan panic
```

Manual di Ubuntu 24.04 nyata (jalankan `./bin/ubt`):
0. **Pertanyaan interaktif** — `./bin/ubt --demo-ask`: coba tiap jenis pakai panah dan angka, pilih
   "Lainnya…" lalu ketik huruf `q` (tidak boleh keluar), isi input tidak valid (harus diblok dengan
   pesan jelas), mundur-maju antar chip (jawaban tetap), perkecil terminal ke 60 kolom (preview pindah
   ke bawah, teks tidak terpotong), dan jalankan lewat SSH dari terminal lain.
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
7. **Resource** — jalankan `stress-ng --cpu 2 --timeout 60` (atau `yes > /dev/null`), pastikan proses
   naik ke atas daftar, load & penjelasan core masuk akal, kill lewat konfirmasi.
8. **Disk** — `fallocate -l 1G /tmp/besar`, pastikan walker menemukannya; buka file itu dengan
   `tail -f /tmp/besar &`, hapus file, pastikan muncul di "terhapus tapi masih dibuka". Wizard swapfile
   di VM uji, cek `swapon --show` dan `/etc/fstab` (+ backup) benar.
9. **Log** — `logger -p user.err "tes ubt"`, pastikan muncul di *Error sejak boot* dan di mode follow.
10. **Network** — wizard keluar ke `example.com` (lolos semua), ke `tidakada.invalid` (berhenti di DNS
    dengan penjelasan), ke port tertutup (berhenti di TCP). Wizard masuk dengan
    `python3 -m http.server 8080 --bind 127.0.0.1` → harus mendeteksi bind localhost.
11. **User & SSH** (di VM, dengan sesi cadangan terbuka) — buat user, masukkan ke `sudo`, pasang key,
    rusak permission `~/.ssh` lalu perbaiki lewat ubt, login pakai key; coba matikan password auth
    tanpa key (harus ditolak), dengan key (berhasil, `sshd -t` jalan dulu); ganti port dengan ufw aktif
    (Plan harus menyertakan `ufw allow`), login ulang dari terminal baru.
12. **Paket** — `apt update` stream tampil, install & purge `cowsay`, cek riwayat apt mencatatnya;
    `touch /var/run/reboot-required` di VM → banner muncul.
13. **Penjadwalan** — buat timer "tiap 5 menit" menjalankan `logger ubt-timer`, cek next elapse,
    jalankan sekarang, lihat log; buat cron.d yang sama, cek penjelas ekspresi; hapus keduanya.
14. **Web & TLS** — install nginx, jalankan `python3 -m http.server 3000`, wizard reverse proxy ke
    domain uji (atau `localhost` via `/etc/hosts`), `curl` lewat nginx berhasil; matikan aplikasi →
    `502` diterjemahkan; cek sertifikat `example.com` dan `expired.badssl.com` (harus merah).
15. **Diagnosa & ekspor** — isi disk sampai >90% di VM, jalankan *Disk penuh* dan ikuti sarannya;
    ekspor 3 entri riwayat jadi `setup.sh`, jalankan di VM bersih dengan `bash -n` lalu sungguhan.

## Pertanyaan terbuka

- **Branch kerja**: fase 0 menyebut `claude/exciting-mendel-pfvegk`, tapi plan saat ini berada di `main`.
  Putuskan branch untuk fase 1 dst. sebelum mulai.
- **Clipboard** untuk tombol "Salin command": OSC 52 (jalan lewat SSH di terminal modern) dengan
  fallback menampilkan command untuk diseleksi manual.
- **Scope v1 membesar** (12 modul + Diagnosa): pertimbangkan rilis bertahap — v0.1 setelah fase 8
  (Ports, Resource, Disk, Log, Systemd), v0.2 setelah fase 13, v1.0 setelah fase 17.
