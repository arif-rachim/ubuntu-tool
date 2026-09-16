// Command ubt adalah asisten terminal interaktif untuk mengelola server Ubuntu 24.04.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/arif-rachim/ubuntu-tool/internal/version"
)

const usage = `ubt — asisten interaktif untuk server Ubuntu

Pemakaian:
  ubt                  buka menu interaktif
  ubt version [--json] tampilkan versi
  ubt help             tampilkan bantuan ini
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		// Menu interaktif dibangun di fase 2 (kerangka TUI).
		fmt.Fprintln(stderr, "Menu interaktif belum tersedia di build ini. Coba `ubt help`.")
		return 1
	}

	switch args[0] {
	case "version", "--version", "-v":
		return cmdVersion(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "ubt: perintah tidak dikenal %q\n\n%s", args[0], usage)
		return 2
	}
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
