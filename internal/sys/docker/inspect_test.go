package docker

import (
	"strings"
	"testing"
	"time"
)

const inspectOut = `[{
  "Name": "/shop_db",
  "Created": "2026-09-20T10:00:00.000000000Z",
  "RestartCount": 5,
  "State": {"Status": "exited", "Running": false, "OOMKilled": true, "ExitCode": 137,
            "StartedAt": "2026-09-22T09:00:00.000000000Z", "FinishedAt": "2026-09-22T09:30:00.000000000Z"},
  "Config": {"Image": "postgres:17", "WorkingDir": "/var/lib/postgresql", "User": "postgres", "Cmd": ["postgres"]},
  "HostConfig": {"RestartPolicy": {"Name": "unless-stopped"}, "Memory": 536870912, "NanoCpus": 1500000000,
                 "NetworkMode": "app-net",
                 "PortBindings": {"5432/tcp": [{"HostIp": "127.0.0.1", "HostPort": "5432"}]}},
  "Mounts": [{"Type": "volume", "Name": "dbdata", "Destination": "/var/lib/postgresql/data", "RW": true},
             {"Type": "bind", "Source": "/srv/init", "Destination": "/docker-entrypoint-initdb.d", "RW": false}]
}]`

func TestParseInspectDanSpec(t *testing.T) {
	i, err := ParseInspect(inspectOut)
	if err != nil {
		t.Fatal(err)
	}
	if i.ShortName() != "shop_db" || i.RestartCount != 5 || !i.State.OOMKilled {
		t.Fatalf("inspect = %+v", i.State)
	}
	s := SpecFromInspect(i)
	if s.Name != "shop_db" || s.Image != "postgres:17" || s.Restart != "unless-stopped" {
		t.Errorf("spec = %+v", s)
	}
	if len(s.Ports) != 1 || s.Ports[0] != "127.0.0.1:5432:5432" {
		t.Errorf("port = %q", s.Ports)
	}
	if len(s.Volumes) != 2 || s.Volumes[0] != "/srv/init:/docker-entrypoint-initdb.d:ro" || s.Volumes[1] != "dbdata:/var/lib/postgresql/data" {
		t.Errorf("volume = %q", s.Volumes)
	}
	if s.Memory != "512m" || s.CPUs != "1.5" || s.Network != "app-net" {
		t.Errorf("batas & network = %q %q %q", s.Memory, s.CPUs, s.Network)
	}
	// Resep hasil inspect harus langsung bisa dipakai membuat ulang container.
	if err := s.Validate(); err != nil {
		t.Errorf("spec hasil inspect tidak valid: %v", err)
	}
}

func TestTroubleshootOOM(t *testing.T) {
	i, _ := ParseInspect(inspectOut)
	fs := Troubleshoot(i, "")
	if len(fs) == 0 {
		t.Fatal("tidak ada temuan")
	}
	if !strings.Contains(fs[0].Symptom, "kehabisan memori") {
		t.Errorf("temuan pertama = %q", fs[0].Symptom)
	}
	joined := ""
	for _, f := range fs {
		joined += f.Symptom + "|" + f.Why + "\n"
	}
	for _, want := range []string{"512 MB", "restart 5 kali", "exit code 137"} {
		if !strings.Contains(strings.ToLower(joined), strings.ToLower(want)) {
			t.Errorf("temuan tidak menyebut %q:\n%s", want, joined)
		}
	}
}

func TestTroubleshootDariLog(t *testing.T) {
	i, _ := ParseInspect(`[{"Name":"/web","State":{"Status":"running","Running":true},"HostConfig":{"RestartPolicy":{"Name":"always"}}}]`)
	fs := Troubleshoot(i, "2026/09/22 listen tcp :80: bind: address already in use")
	if len(fs) != 1 || !strings.Contains(fs[0].Symptom, "address already in use") {
		t.Fatalf("temuan = %+v", fs)
	}
	if !strings.Contains(fs[0].Fix, "Ports & Proses") {
		t.Errorf("saran = %q", fs[0].Fix)
	}
}

func TestTroubleshootTanpaRestartPolicy(t *testing.T) {
	i, _ := ParseInspect(`[{"Name":"/web","State":{"Status":"running","Running":true},"HostConfig":{"RestartPolicy":{"Name":""}}}]`)
	fs := Troubleshoot(i, "")
	if len(fs) != 1 || !strings.Contains(fs[0].Symptom, "Tanpa kebijakan restart") {
		t.Fatalf("temuan = %+v", fs)
	}
}

func TestExitMeaning(t *testing.T) {
	for code, want := range map[int]string{0: "normal", 127: "tidak ada di dalam image", 137: "OOM", 143: "SIGTERM", 130: "sinyal 2"} {
		if got := ExitMeaning(code); !strings.Contains(got, want) {
			t.Errorf("exit %d = %q, ingin memuat %q", code, got, want)
		}
	}
}

func TestParseStatsUrutTerberat(t *testing.T) {
	out := `{"Name":"ringan","CPUPerc":"0.50%","MemPerc":"1.00%"}
{"Name":"berat","CPUPerc":"87.30%","MemPerc":"90.00%"}
baris rusak
{"Name":"sedang","CPUPerc":"12.00%","MemPerc":"5.00%"}`
	ss := ParseStats(out)
	if len(ss) != 3 || ss[0].Name != "berat" || ss[2].Name != "ringan" {
		t.Fatalf("stats = %+v", ss)
	}
	if Percent("87.30%") != 87.3 || Percent("—") != -1 {
		t.Error("Percent salah")
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	if got := Age("2026-09-22T09:00:00.000000000Z", now); got != "3 jam" {
		t.Errorf("Age = %q", got)
	}
	if got := Age("bukan waktu", now); got != "—" {
		t.Errorf("Age tidak valid = %q", got)
	}
}

func TestVolumeDanNetworkUsers(t *testing.T) {
	cs := ParseContainers(`{"Names":"db","Mounts":"dbdata,/srv/init","Networks":"app-net","State":"running"}
{"Names":"web","Mounts":"","Networks":"app-net,bridge","State":"running"}`)
	if got := VolumeUsers("dbdata", cs); len(got) != 1 || got[0] != "db" {
		t.Errorf("VolumeUsers = %q", got)
	}
	if got := NetworkUsers("app-net", cs); len(got) != 2 {
		t.Errorf("NetworkUsers = %q", got)
	}
	if got := VolumeUsers("lain", cs); got != nil {
		t.Errorf("volume menganggur = %q", got)
	}
}

func TestParseVolumeDanNetwork(t *testing.T) {
	vs := ParseVolumes(`{"Name":"dbdata","Driver":"local","Mountpoint":"/var/lib/docker/volumes/dbdata/_data"}`)
	if len(vs) != 1 || vs[0].Name != "dbdata" {
		t.Errorf("volume = %+v", vs)
	}
	ns := ParseNetworks(`{"ID":"1","Name":"bridge","Driver":"bridge"}
{"ID":"2","Name":"app-net","Driver":"bridge"}`)
	if len(ns) != 2 || !ns[0].Builtin() || ns[1].Builtin() {
		t.Errorf("network = %+v", ns)
	}
}
