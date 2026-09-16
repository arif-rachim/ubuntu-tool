package ports

import (
	"fmt"
	"strconv"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/ui/ask"
)

// Nilai aksi yang bisa dipilih di layar detail.
const (
	ActStopUnit   = procact.StopUnit
	ActTerm       = procact.Term
	ActKill       = procact.Kill
	ActDockerStop = procact.DockerStop
	ActFindOwner  = "find-owner"
)

// Context adalah informasi user saat ini yang memengaruhi aksi.
type Context = procact.Context

// SSHPortFromEnv membaca port server sesi SSH saat ini.
var SSHPortFromEnv = procact.SSHPortFromEnv

// Target adalah port yang akan ditindak beserta proses pemiliknya.
type Target struct {
	Listener sysports.Listener
	Proc     *procs.Process // proses utama (induk bila beberapa proses berbagi socket); nil bila tidak terlihat
	Shared   int            // jumlah proses yang berbagi socket ini
}

func (t Target) subject() procact.Subject {
	return procact.Subject{Port: int(t.Listener.Local.Port()), Proto: string(t.Listener.Proto), Shared: t.Shared}
}

// MainPID memilih proses utama dari beberapa proses yang berbagi socket: yang induknya
// bukan salah satu dari mereka (mis. master nginx, bukan worker-nya).
func MainPID(pids []int, parent func(pid int) int) int {
	if len(pids) == 0 {
		return 0
	}
	set := map[int]bool{}
	for _, p := range pids {
		set[p] = true
	}
	for _, p := range pids {
		if !set[parent(p)] {
			return p
		}
	}
	return pids[0]
}

func shortID(id string) string { return procact.ShortID(id) }

// Options mengembalikan pilihan aksi untuk target.
func Options(t Target, ctx Context) []ask.Option {
	if t.Proc == nil {
		return []ask.Option{{
			Value:       ActFindOwner,
			Label:       "Cari tahu pemiliknya dengan sudo",
			Recommended: true,
			Description: fmt.Sprintf("Pemilik port %d tidak terlihat oleh user ini. Jalankan ss sebagai root untuk melihat proses pemiliknya.", t.Listener.Local.Port()),
		}}
	}
	return procact.Options(*t.Proc, t.subject(), ctx)
}

// Plan membangun command untuk aksi yang dipilih.
func Plan(action string, t Target, ctx Context) run.Plan {
	if action == ActFindOwner {
		port := t.Listener.Local.Port()
		filter := fmt.Sprintf("sport = :%d", port)
		flag := "-tlpn"
		if t.Listener.Proto == sysports.UDP {
			flag = "-ulpn"
		}
		return run.Single(run.Command{
			Title:     fmt.Sprintf("Cari pemilik port %d/%s", port, t.Listener.Proto),
			Argv:      []string{"ss", flag, filter},
			NeedsRoot: true,
			Explain: []run.Line{
				{Token: "ss", Meaning: "tampilkan socket (pengganti netstat)"},
				{Token: flag, Meaning: "t/u = TCP/UDP, l = yang listening, p = tampilkan proses, n = angka port apa adanya"},
				{Token: filter, Meaning: "hanya socket dengan port lokal " + strconv.Itoa(int(port))},
			},
			Effect: "Hanya membaca. Nama proses & PID pemilik terlihat di kolom users:((...)).",
		})
	}
	if t.Proc == nil {
		return run.Plan{}
	}
	return procact.Plan(action, *t.Proc, t.subject(), ctx)
}
