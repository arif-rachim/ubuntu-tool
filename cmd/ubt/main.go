// Command ubt adalah asisten terminal interaktif untuk mengelola server Ubuntu 24.04.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/arif-rachim/ubuntu-tool/internal/app"
	"github.com/arif-rachim/ubuntu-tool/internal/i18n"
	"github.com/arif-rachim/ubuntu-tool/internal/nav"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/demo"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/home"
	"github.com/arif-rachim/ubuntu-tool/internal/version"
)

const usage = `ubt — asisten interaktif untuk server Ubuntu

Pemakaian:
  ubt                  buka menu interaktif
  ubt version [--json] tampilkan versi
  ubt help             tampilkan bantuan ini
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, isInteractive()))
}

func isInteractive() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func run(args []string, stdout, stderr io.Writer, interactive bool) int {
	if len(args) == 0 {
		return runTUI(home.New(home.Groups()), stderr, interactive)
	}

	switch args[0] {
	case "version", "--version", "-v":
		return cmdVersion(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "--demo-ask":
		return runTUI(demo.New(), stderr, interactive)
	default:
		fmt.Fprintf(stderr, "ubt: perintah tidak dikenal %q\n\n%s", args[0], usage)
		return 2
	}
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
