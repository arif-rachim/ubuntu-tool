# ubt — asisten interaktif untuk server Ubuntu

`ubt` membantu kamu mengelola server Ubuntu 24.04 tanpa harus hafal command. Ketik `ubt`, pilih
menu, jawab beberapa pertanyaan, lalu `ubt` menampilkan **command persis** yang akan dijalankan
beserta penjelasan tiap bagiannya, efeknya, dan meminta konfirmasi. Sambil menyelesaikan masalah,
kamu belajar command aslinya.

```
 ⚠  BERISIKO  Buat reverse proxy app.contoh.com
 Perintah yang akan dijalankan:
    1. $  sudo install -m 0644 /dev/stdin /etc/nginx/sites-available/app.contoh.com
    2. $  sudo ln -sfn /etc/nginx/sites-available/app.contoh.com /etc/nginx/sites-enabled/app.contoh.com
    3. $  sudo nginx -t                 (validasi — langkah 4 tidak dijalankan bila gagal)
    4. $  sudo systemctl reload nginx
 [ y Jalankan ]   [ n Batal ]   [ c Salin command ]
```

## Prinsip

- **Tidak ada yang dijalankan diam-diam.** Setiap perubahan menampilkan command, penjelasan, efek,
  alternatif yang lebih aman, dan tingkat risikonya. Aksi berbahaya butuh konfirmasi dua kali.
- **Pengaman anti-terkunci.** Mengaktifkan firewall otomatis mengizinkan port SSH; mematikan login
  password ditolak bila belum ada SSH key; perubahan sshd divalidasi `sshd -t` sebelum restart.
- **sudo hanya saat perlu**, password diminta sekali oleh sudo sendiri (ubt tidak pernah menyimpannya).
- **Riwayat** setiap command tersimpan di `~/.config/ubt/history.log` (izin 0600) dan bisa diekspor
  jadi script bash. Isi rahasia tidak pernah dicatat.
- **Beberapa versi berdampingan didukung.** Server dengan PostgreSQL 16 bawaan Ubuntu dan 18 dari
  repository resmi sekaligus akan terbaca keduanya; ubt memilih cluster yang berjalan dan bisa
  dipindah kapan saja.
- **Password tidak pernah lewat ubt.** Login registry diserahkan ke `docker login`, password role
  PostgreSQL ke `createuser --pwprompt` / `\password` — ubt hanya mencatat alamat & username.

## Modul

| Kelompok | Modul | Contoh yang bisa dilakukan |
|---|---|---|
| 🩺 Diagnosa | Diagnosa berdasarkan gejala | Disk penuh, server lambat, service crash loop, SSH gagal login, habis update rusak, cek kesehatan umum |
| 💻 Sistem | Resource | CPU/RAM/load/tekanan IO dijelaskan, proses terberat, renice & hentikan proses |
| | Disk & Storage | Apa yang makan tempat, file terhapus yang masih dibuka, bersih-bersih terpandu, swapfile |
| | Log | Error sejak boot, log per service, ikuti log secara langsung |
| | Service (systemd) | Start/stop/restart/enable dengan peringatan untuk service kritis |
| | Penjadwalan | Cron & systemd timer dijelaskan dalam bahasa manusia, wizard jadwal baru |
| | Paket (apt) | Update keamanan, cari & install, perbaiki paket rusak, auto-update |
| 🌐 Jaringan | Network & konektivitas | Wizard "kenapa tidak bisa konek" dan "kenapa port tidak bisa diakses" |
| | Ports & Proses | Port mana dipakai proses apa, berapa koneksi yang sedang terbuka dan dari mana, container di balik port yang dipublikasikan Docker, hentikan dengan aman |
| | Firewall (ufw) | Allow/deny dengan preset, hapus aturan, aktifkan tanpa memutus SSH |
| | Web & TLS | Reverse proxy nginx, HTTPS Let's Encrypt, cek sertifikat domain mana pun |
| 🔑 Akses | User & SSH | Tambah user, sudo, SSH key, amankan sshd |
| 📦 Container | Docker | Wizard jalankan container (port, volume, env, workdir, command, limit), build & registry Nexus (login/push/pull), muat & simpan image dari berkas, volume & network, commit, periksa & buat ulang container |
| 🗄 Database | PostgreSQL | Install versi pilihan (bawaan Ubuntu atau PGDG, mis. 18), database/role/hak akses, editor query dengan saran nama tabel/kolom & bentuk nilai waktu dan hasil berbentuk tabel (bisa digulir, disaring, dibuka per baris), cadangan + jadwal otomatis, akses dari jaringan sekalian aturan ufw, monitor koneksi & query lambat, penyetelan parameter |
| 📜 Riwayat | Riwayat perintah | Lihat apa yang pernah diubah, ekspor jadi script |

Rencana dan catatan desain lengkap ada di [`docs/PLAN.md`](docs/PLAN.md).

## Pemasangan

### Binary rilis

```bash
arch=$(dpkg --print-architecture)      # amd64 atau arm64
base=https://github.com/arif-rachim/ubuntu-tool/releases/latest/download
curl -fsSLo ubt-linux-$arch "$base/ubt-linux-$arch"
curl -fsSL "$base/SHA256SUMS" | grep " ubt-linux-$arch\$" | sha256sum -c -
sudo install -m 0755 ubt-linux-$arch /usr/local/bin/ubt
ubt doctor
```

Binary juga bisa diunduh langsung dari halaman
[Releases](https://github.com/arif-rachim/ubuntu-tool/releases):

- **Rilis stabil** (`vX.Y.Z`) — `releases/latest/download/ubt-linux-amd64` atau `…-arm64`.
- **Nightly** — build otomatis setiap ada perubahan di `main`, untuk mencoba fitur terbaru:
  `https://github.com/arif-rachim/ubuntu-tool/releases/download/nightly/ubt-linux-amd64`

### Dari source

Butuh Go (lihat versi di `go.mod`) dan `make` (`sudo apt install make`).

```bash
make build            # hasil: ./bin/ubt
sudo make install     # pasang ke /usr/local/bin/ubt
```

## Pemakaian

```bash
ubt                               # menu interaktif
ubt doctor                        # program apa yang dibutuhkan tiap modul, dan paket apt-nya
ubt ports [--json]                # port listening, prosesnya, dan koneksi yang sedang terbuka
ubt history [--last N] [--json]   # command yang pernah dijalankan lewat ubt
ubt history export --last 20 > setup-server.sh
ubt version [--json]
```

Di dalam menu: `↑↓` pindah, `enter` pilih, `esc` kembali, `?` penjelasan layar, `r` muat ulang,
`q` keluar. Angka `1-9` langsung memilih opsi di pertanyaan.

## Pengembangan

```bash
make all              # fmt-check + vet + test + build
make test
make lint             # gofmt + go vet (+ golangci-lint bila terinstall)
make build-all        # binary statis linux/amd64 & linux/arm64 di ./dist
make release TAG=v0.1.0            # build rilis + SHA256SUMS secara lokal
make release TAG=v0.1.0 PUBLISH=1  # + push tag → GitHub Actions menerbitkan rilis
```

Build & rilis otomatis (`.github/workflows/release.yml`):

| Pemicu | Hasil |
|---|---|
| push ke `main` | pra-rilis `nightly` diperbarui (binary amd64/arm64 + `SHA256SUMS`) |
| push tag `vX.Y.Z` | rilis `ubt vX.Y.Z` dengan catatan rilis otomatis |
| Actions → Release → *Run workflow* | memperbarui `nightly` secara manual |

Setiap build menjalankan gofmt, vet, dan `go test -race` dulu; binary tidak diterbitkan bila test gagal.

## Lisensi

[MIT](LICENSE)
