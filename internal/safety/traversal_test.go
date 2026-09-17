package safety

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/web"
)

// Nama dari input atau dari sistem (site, domain, jadwal) tidak boleh membuat command root menulis,
// menautkan, atau menghapus di luar folder yang dimaksud, walau validator di layar terlewati.
func TestTidakAdaPathTraversal(t *testing.T) {
	for _, bad := range []string{"../../../etc/passwd", "/etc/shadow", "..", "a/../../b", "sub/dir", ""} {
		site := web.Site{Name: bad, Path: "/etc/nginx/sites-available/" + bad}
		spec := schedule.Spec{Name: bad, Command: "true", User: "root", Freq: schedule.Frequency{Kind: "daily"}}
		nginx := []string{"/etc/nginx/sites-available", "/etc/nginx/sites-enabled"}
		cases := []struct {
			name    string
			plan    run.Plan
			allowed []string
		}{
			{"web.Proxy", web.ProxyPlan(web.ProxySpec{Domain: bad, Port: "3000"}, web.DefaultPaths, false), nginx},
			{"web.SiteEnable", web.SiteTogglePlan(site, web.DefaultPaths, true), nginx},
			{"web.SiteDisable", web.SiteTogglePlan(site, web.DefaultPaths, false), nginx},
			{"schedule.Cron", schedule.CreateCronPlan(spec), []string{"/etc/cron.d"}},
			{"schedule.Timer", must(schedule.CreateTimerPlan(spec)), []string{"/etc/systemd/system"}},
			{"schedule.RemoveTimer", schedule.RemovePlan(schedule.Job{Kind: schedule.KindTimer, Name: bad + ".timer", Managed: true}), []string{"/etc/systemd/system"}},
			{"schedule.RemoveCron", schedule.RemovePlan(schedule.Job{Kind: schedule.KindCron, Name: bad, Source: bad, Managed: true}), []string{"/etc/cron.d"}},
		}
		for _, c := range cases {
			for _, s := range c.plan.Sequence() {
				cmd := s.Command
				switch filepath.Base(cmd.Argv[0]) {
				case "install", "ln", "rm", "tee", "cp", "mv", "chmod", "chown":
				default:
					continue
				}
				for _, a := range cmd.Argv[1:] {
					if !strings.HasPrefix(a, "/") || a == "/dev/stdin" {
						continue
					}
					if !directlyIn(a, c.allowed) {
						t.Errorf("%s(%q): %s menyentuh %s di luar %v", c.name, bad, cmd.Preview(false), a, c.allowed)
					}
				}
			}
		}
	}
}

// directlyIn melaporkan path (setelah ../ diselesaikan) adalah file langsung di salah satu folder.
func directlyIn(path string, dirs []string) bool {
	clean := filepath.Clean(path)
	for _, d := range dirs {
		if filepath.Dir(clean) == d {
			return true
		}
	}
	return false
}

func TestFileName(t *testing.T) {
	for in, want := range map[string]string{"app.conf": "app.conf", "../../etc/passwd": "passwd", "/etc/shadow": "shadow", "..": "_", "": "_", "/": "_", "dir/": "dir"} {
		if got := run.FileName(in); got != want {
			t.Errorf("FileName(%q) = %q, ingin %q", in, got, want)
		}
	}
}
