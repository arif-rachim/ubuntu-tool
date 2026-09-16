// Command ubt adalah asisten terminal interaktif untuk mengelola server Ubuntu 24.04.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/arif-rachim/ubuntu-tool/internal/app"
	"github.com/arif-rachim/ubuntu-tool/internal/cli"
	"github.com/arif-rachim/ubuntu-tool/internal/diagnose"
	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/demo"
	diagscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/diagnose"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/history"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/home"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/network"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/packages"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/resource"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/services"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/shared"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/users"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/web"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/runflow"
	"github.com/arif-rachim/ubuntu-tool/internal/version"
)

const usage = `ubt — asisten interaktif untuk server Ubuntu

Pemakaian:
  ubt                               buka menu interaktif
  ubt doctor [--json]               periksa program yang dibutuhkan tiap modul
  ubt ports [--json]                daftar port yang sedang listening dan prosesnya
  ubt history [--last N] [--json]   command yang pernah dijalankan lewat ubt
  ubt history export [--last N]     ekspor riwayat jadi script bash (ke stdout)
  ubt version [--json]              tampilkan versi
  ubt help                          tampilkan bantuan ini

Contoh:
  ubt history export --last 20 > setup-server.sh
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, isInteractive()))
}

func isInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func run(args []string, stdout, stderr io.Writer, interactive bool) int {
	if len(args) == 0 {
		if !interactive {
			fmt.Fprintln(stderr, i18n.NeedsTTY)
			return 1
		}
		env := shared.Default()
		denv := diagnose.DefaultEnv(env.Runner, env.ProcRoot, env.UID, env.Now)
		menu := home.New(home.Wire(home.Groups(), openers(env))).WithQuick(func(ctx context.Context) []string { return diagnose.Quick(ctx, denv) })
		return runTUI(menu, stderr, interactive)
	}

	switch args[0] {
	case "version", "--version", "-v":
		return cmdVersion(args[1:], stdout, stderr)
	case "doctor":
		return cli.Doctor(args[1:], stdout, stderr)
	case "ports":
		return cli.Ports(args[1:], stdout, stderr)
	case "history":
		return cli.History(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "--demo-ask":
		return runTUI(demo.New(), stderr, interactive)
	case "--demo-run":
		return runTUI(demo.NewRun(runflow.DefaultDeps()), stderr, interactive)
	default:
		fmt.Fprintf(stderr, "ubt: perintah tidak dikenal %q\n\n%s", args[0], usage)
		return 2
	}
}

// moduleNames memetakan ID modul ke label menu utama.
func moduleNames() map[string]string {
	names := map[string]string{}
	for _, g := range home.Groups() {
		for _, it := range g.Items {
			names[it.ID] = it.Label
		}
	}
	return names
}

// openers memetakan ID menu ke layar modul yang sudah tersedia.
func openers(env shared.Env) map[string]func() nav.Screen {
	var m map[string]func() nav.Screen
	// open membuka modul lain dari dalam modul (mis. wizard Network menyarankan modul Firewall).
	var open func(id string) nav.Screen
	open = func(id string) nav.Screen {
		if f, ok := m[id]; ok {
			return f()
		}
		// "network:inbound" membuka modul network langsung di wizard tertentu.
		if mod, wizard, ok := strings.Cut(id, ":"); ok && mod == "network" {
			return network.New(env, open).WithWizard(wizard)
		}
		return nil
	}
	m = map[string]func() nav.Screen{
		"diagnose": func() nav.Screen {
			return diagscreen.New(diagnose.DefaultEnv(env.Runner, env.ProcRoot, env.UID, env.Now), open, moduleNames())
		},
		"network":  func() nav.Screen { return network.New(env, open) },
		"ports":    func() nav.Screen { return ports.New(env) },
		"resource": func() nav.Screen { return resource.New(env) },
		"disk":     func() nav.Screen { return disk.New(env) },
		"logs":     func() nav.Screen { return logs.New(env) },
		"services": func() nav.Screen { return services.New(env) },
		"packages": func() nav.Screen { return packages.New(env) },
		"schedule": func() nav.Screen { return schedule.New(env) },
		"users":    func() nav.Screen { return users.New(env) },
		"firewall": func() nav.Screen { return firewall.New(env) },
		"web":      func() nav.Screen { return web.New(env) },
		"docker":   func() nav.Screen { return docker.New(env) },
		"history":  func() nav.Screen { return history.New(env, nil) },
	}
	return m
}

func runTUI(root nav.Screen, stderr io.Writer, interactive bool) int {
	if !interactive {
		fmt.Fprintln(stderr, i18n.NeedsTTY)
		return 1
	}
	if _, err := tea.NewProgram(app.New(root)).Run(); err != nil {
		fmt.Fprintf(stderr, "ubt: %v\n", err)
		return 1
	}
	return 0
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	info := version.Get()
	switch {
	case len(args) == 0:
		fmt.Fprintln(stdout, info.String())
	case len(args) == 1 && args[0] == "--json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(info); err != nil {
			fmt.Fprintf(stderr, "ubt: gagal menulis JSON: %v\n", err)
			return 1
		}
	default:
		fmt.Fprintf(stderr, "ubt version: argumen tidak dikenal %q\n", args)
		return 2
	}
	return 0
}
