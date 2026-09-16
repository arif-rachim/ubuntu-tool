# ubt — asisten interaktif untuk server Ubuntu

`ubt` membantu kamu mengelola server Ubuntu 24.04 tanpa harus hafal command. Ketik `ubt`, pilih
menu, jawab beberapa pertanyaan, lalu `ubt` menampilkan **command persis** yang akan dijalankan
beserta penjelasan tiap bagiannya, efeknya, dan meminta konfirmasi. Sambil menyelesaikan masalah,
kamu belajar command aslinya.

> **Status: dalam pengembangan awal.** Menu interaktif belum tersedia. Rencana lengkap ada di
> [`docs/PLAN.md`](docs/PLAN.md).

## Modul yang direncanakan

| Kelompok | Modul |
|---|---|
| Diagnosa | Diagnosa berdasarkan gejala, cek kesehatan umum |
| Sistem | Resource, Disk & Storage, Log, Service (systemd), Penjadwalan (cron & timer), Paket (apt) |
| Jaringan | Network & konektivitas, Ports & Proses, Firewall (ufw), Web & TLS |
| Akses | User & SSH |
| Container | Docker |

## Membangun dari source

Butuh Go (lihat versi di `go.mod`) dan `make`.

```bash
make build            # hasil: ./bin/ubt
./bin/ubt version
sudo make install     # pasang ke /usr/local/bin/ubt
```

Perintah lain:

```bash
make test             # jalankan semua test
make lint             # gofmt + go vet (+ golangci-lint bila terinstall)
make build-all        # binary statis linux/amd64 & linux/arm64 di ./dist
```
