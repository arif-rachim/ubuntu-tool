package version

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestFillFromBuildInfo(t *testing.T) {
	tests := []struct {
		name        string
		in          Info
		bi          debug.BuildInfo
		wantVersion string
		wantCommit  string
		wantDate    string
	}{
		{
			name: "ldflags menang atas build info",
			in:   Info{Version: "v1.2.3", Commit: "abc", Date: "2026-01-01"},
			bi: debug.BuildInfo{
				Main:     debug.Module{Version: "v9.9.9"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "zzz"}, {Key: "vcs.time", Value: "2020"}},
			},
			wantVersion: "v1.2.3", wantCommit: "abc", wantDate: "2026-01-01",
		},
		{
			name: "go install mengisi dari build info",
			bi: debug.BuildInfo{
				Main:     debug.Module{Version: "v0.1.0"},
				Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}, {Key: "vcs.time", Value: "2026-09-16T00:00:00Z"}},
			},
			wantVersion: "v0.1.0", wantCommit: "deadbeef", wantDate: "2026-09-16T00:00:00Z",
		},
		{
			name:        "(devel) diabaikan",
			bi:          debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			wantVersion: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in
			fillFromBuildInfo(&got, &tt.bi)
			if got.Version != tt.wantVersion || got.Commit != tt.wantCommit || got.Date != tt.wantDate {
				t.Errorf("dapat %+v, ingin version=%q commit=%q date=%q", got, tt.wantVersion, tt.wantCommit, tt.wantDate)
			}
		})
	}
}

func TestStringMemendekkanCommit(t *testing.T) {
	s := Info{Version: "v1.0.0", Commit: "0123456789abcdef", GoVersion: "go1.27", Platform: "linux/amd64"}.String()
	if !strings.Contains(s, "commit 0123456789ab,") {
		t.Errorf("commit tidak dipendekkan: %s", s)
	}
	if !strings.Contains(s, "dibuat tidak diketahui") {
		t.Errorf("tanggal kosong tidak ditangani: %s", s)
	}
}
