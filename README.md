# ubt: an interactive assistant for Ubuntu servers

`ubt` is a terminal UI that helps you manage an Ubuntu 24.04 server without having to memorise commands. You type `ubt`, pick a menu, answer a few questions, and `ubt` shows the **exact command** it is about to run, with an explanation of every part, its effect, a safer alternative where one exists, and a risk level, then asks for confirmation. It is aimed at people who run their own servers but are not full-time Linux administrators, so while they fix a problem they also learn the real commands behind it. Built-in guards prevent the usual ways of locking yourself out: enabling the firewall always allows SSH first, and sshd changes are validated before a restart. It is a single static Go binary built on Bubble Tea v2, Bubbles and Lip Gloss, covering diagnosis, resources, disk, logs, systemd, scheduling, apt, networking, ports, ufw, nginx/TLS, users and SSH, and Docker. The interface text is currently in Indonesian only; nightly binaries are published for amd64 and arm64.

> Status: active development. The interface is Indonesian only (`internal/i18n`); Linux command names and flags are not translated.

```text
 ⚠  BERISIKO  Buat reverse proxy app.contoh.com
 Perintah yang akan dijalankan:
    1. $  sudo install -m 0644 /dev/stdin /etc/nginx/sites-available/app.contoh.com
    2. $  sudo ln -sfn /etc/nginx/sites-available/app.contoh.com /etc/nginx/sites-enabled/app.contoh.com
    3. $  sudo nginx -t                 (validasi — langkah 4 tidak dijalankan bila gagal)
    4. $  sudo systemctl reload nginx
 [ y Jalankan ]   [ n Batal ]   [ c Salin command ]
```

(An example confirmation screen: a risky action, "create reverse proxy", listing the commands; step 4 is skipped if the `nginx -t` validation fails. The buttons are Run, Cancel and Copy command.)

## Principles

- **Nothing runs silently.** Every change shows the command, an explanation, its effect, a safer alternative and its risk level. Dangerous actions need a second confirmation.
- **Lock-out guards.** Enabling the firewall automatically allows the SSH port; disabling password login is refused if no SSH key is set up yet; sshd changes are validated with `sshd -t` before the restart.
- **sudo only when needed.** The password is asked for once by sudo itself; ubt never stores it.
- **History.** Every command is recorded in `~/.config/ubt/history.log` (or `$XDG_CONFIG_HOME/ubt/history.log`, file mode 0600) and can be exported as a bash script. Secret contents are never logged.

## Modules

| Group | Module | Examples of what you can do |
|---|---|---|
| 🩺 Diagnosis | Symptom-based diagnosis | Full disk, slow server, service crash loop, failed SSH login, broken after an update, general health check |
| 💻 System | Resources | CPU/RAM/load/IO pressure explained, heaviest processes, renice and stop processes |
| | Disk & storage | What is using space, deleted files still held open, guided clean-up, swapfile |
| | Logs | Errors since boot, logs per service, follow logs live |
| | Services (systemd) | Start/stop/restart/enable with warnings for critical services |
| | Scheduling | Cron jobs and systemd timers explained in plain language, a wizard for new schedules |
| | Packages (apt) | Security updates, search and install, fix broken packages, automatic updates |
| 🌐 Network | Network & connectivity | "Why can't I connect" and "why is this port unreachable" wizards |
| | Ports & processes | Which process uses which port, stop it safely |
| | Firewall (ufw) | Allow/deny with presets, delete rules, enable without cutting off SSH |
| | Web & TLS | nginx reverse proxy, Let's Encrypt HTTPS, check the certificate of any domain |
| 🔑 Access | Users & SSH | Add users, sudo, SSH keys, harden sshd |
| 📦 Containers | Docker | Containers and images, interactive shell, commit, Dockerfile/compose wizard, run Python |
| 📜 History | Command history | See what has been changed, export it as a script |

The full plan and design notes are in [`docs/PLAN.md`](docs/PLAN.md) (Indonesian).

## Tech stack

Go 1.25 · Bubble Tea v2 · Bubbles v2 · Lip Gloss v2 · GitHub Actions

## Installation

### Release binaries

The release workflow publishes a **nightly** pre-release (static `ubt-linux-amd64` and `ubt-linux-arm64` binaries plus `SHA256SUMS`) on every push to `main`:

```bash
arch=$(dpkg --print-architecture)      # amd64 or arm64
base=https://github.com/arif-rachim/ubuntu-tool/releases/download/nightly
curl -fsSLo ubt-linux-$arch "$base/ubt-linux-$arch"
curl -fsSL "$base/SHA256SUMS" | grep " ubt-linux-$arch\$" | sha256sum -c -
sudo install -m 0755 ubt-linux-$arch /usr/local/bin/ubt
ubt doctor
```

Once a stable `vX.Y.Z` tag has been published, the same files are available under `https://github.com/arif-rachim/ubuntu-tool/releases/latest/download/`. Binaries can also be downloaded from the [Releases](https://github.com/arif-rachim/ubuntu-tool/releases) page.

### From source

You need Go (see the version in `go.mod`) and `make` (`sudo apt install make`).

```bash
make build            # output: ./bin/ubt
sudo make install     # installs to /usr/local/bin/ubt
```

## Usage

```bash
ubt                               # interactive menu
ubt doctor [--json]               # which programs each module needs, and their apt packages
ubt ports [--json]                # listening ports and their processes
ubt history [--last N] [--json]   # commands previously run through ubt
ubt history export --last 20 > setup-server.sh
ubt version [--json]
```

Inside the menu: `↑↓` move, `enter` select, `esc` back, `?` explains the current screen, `r` reload, `q` quit. The digits `1-9` pick an option directly in a question.

## Project structure

```text
cmd/ubt/            entry point, subcommands and screen registry
internal/app/       root Bubble Tea model and key bindings
internal/cli/       non-interactive subcommands: doctor, ports, history
internal/screens/   one package per menu module (disk, docker, firewall, logs, ...)
internal/sys/       system readers and command plans per area (systemd, ufw, apt, ...)
internal/run/       command plans, sudo handling, streaming runner, history log
internal/ui/        shared widgets: ask (question flow), runflow (confirm/execute), tables, theme
internal/safety/    cross-module safety tests
internal/i18n/      all interface strings
docs/PLAN.md        implementation plan
```

## Development

```bash
make all              # fmt-check + vet + test + build
make test
make lint             # gofmt + go vet (+ golangci-lint if installed)
make build-all        # static linux/amd64 and linux/arm64 binaries in ./dist
make release TAG=v0.1.0            # build a release + SHA256SUMS locally
make release TAG=v0.1.0 PUBLISH=1  # + push the tag so GitHub Actions publishes the release
```

Automatic build and release (`.github/workflows/release.yml`):

| Trigger | Result |
|---|---|
| push to `main` | the `nightly` pre-release is updated (amd64/arm64 binaries + `SHA256SUMS`) |
| push a `vX.Y.Z` tag | a `ubt vX.Y.Z` release with generated release notes |
| Actions → Release → *Run workflow* | updates `nightly` manually |

Every build runs gofmt, vet and `go test -race` first; binaries are not published if the tests fail. CI (`.github/workflows/ci.yml`) runs on pushes to `main` and `claude/**` branches and on pull requests.

## License

[MIT](LICENSE)
