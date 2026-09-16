// Package version menyimpan informasi versi build ubt.
//
// Nilai Version, Commit, dan Date diisi saat build lewat -ldflags (lihat Makefile).
// Bila kosong (mis. dipasang lewat `go install`), nilainya diambil dari build info Go.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	Version = ""
	Commit  = ""
	Date    = ""
)

// Info adalah informasi versi yang sudah dilengkapi fallback.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	Platform  string `json:"platform"`
}

// Get mengembalikan Info dengan fallback dari build info bila ldflags tidak dipakai.
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		fillFromBuildInfo(&info, bi)
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	return info
}

func fillFromBuildInfo(info *Info, bi *debug.BuildInfo) {
	if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		info.Version = bi.Main.Version
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.Date == "" {
				info.Date = s.Value
			}
		}
	}
}

// String mengembalikan satu baris versi yang ramah dibaca manusia.
func (i Info) String() string {
	commit := i.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		commit = "tidak diketahui"
	}
	date := i.Date
	if date == "" {
		date = "tidak diketahui"
	}
	return fmt.Sprintf("ubt %s (commit %s, dibuat %s, %s, %s)", i.Version, commit, date, i.GoVersion, i.Platform)
}
