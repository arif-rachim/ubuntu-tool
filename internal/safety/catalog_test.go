// Package safety berisi test lintas modul yang menjaga aturan keselamatan ubt:
// setiap command terlihat & dijelaskan, command berbahaya ditandai berbahaya, input user tidak bisa
// menyisipkan command lain, pemeriksaan "hanya membaca" benar-benar hanya membaca, dan hanya lapisan
// eksekusi yang boleh menjalankan proses atau mengubah file.
package safety

import (
	"net/netip"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/history"
	logscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/logs"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/network"
	portscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/screens/procact"
	webscreen "github.com/arif-rachim/ubuntu-tool/internal/screens/web"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/disk"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/docker"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/firewall"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/packages"
	sysports "github.com/arif-rachim/ubuntu-tool/internal/sys/ports"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/postgres"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/procs"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/schedule"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/systemd"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/users"
	"github.com/arif-rachim/ubuntu-tool/internal/sys/web"
)

// evil adalah input yang akan berbahaya bila sampai ke shell tanpa dikutip. Dipakai untuk memastikan
// setiap input tetap menjadi satu argumen utuh walau validator di layar terlewati.
const evil = "x';touch /tmp/ubt-pwned;$(id)`id`"

var now = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

type entry struct {
	name string
	plan run.Plan
}

func must(p run.Plan, _ ...any) run.Plan { return p }

// catalog membangun semua Plan yang bisa dihasilkan ubt, dengan input normal dan input jahat.
func catalog() []entry {
	var out []entry
	add := func(name string, p run.Plan) { out = append(out, entry{name, p}) }

	// Paket
	add("packages.Update", packages.UpdatePlan())
	add("packages.Upgrade", packages.UpgradePlan(10, 3))
	add("packages.Install", packages.InstallPlan(evil))
	add("packages.Remove", packages.RemovePlan(evil, false))
	add("packages.Purge", packages.RemovePlan("nginx", true))
	add("packages.FixBroken", packages.FixBrokenPlan())
	add("packages.AutoUpgrade", packages.EnableAutoUpgradePlan())
	add("packages.AddPPA", packages.AddPPAPlan(evil))
	add("packages.Reboot", packages.RebootPlan())

	// Service
	for _, act := range []string{systemd.ActStart, systemd.ActStop, systemd.ActRestart, systemd.ActReload, systemd.ActEnable, systemd.ActDisable, systemd.ActEnableNow, systemd.ActResetFailed} {
		add("systemd."+act, systemd.Plan(act, evil, false))
		add("systemd."+act+".ssh", systemd.Plan(act, "ssh.service", false))
		add("systemd."+act+".user", systemd.Plan(act, "app.service", true))
	}

	// Proses & port
	proc := procs.Process{PID: 4242, Name: evil, UID: 1000, User: "budi", Cmdline: []string{evil},
		Cgroup: procs.Cgroup{Unit: "app.service", Container: "0123456789abcdef0123"}}
	ctx := procact.Context{UID: 1000, SelfPID: 1}
	for _, act := range []string{procact.StopUnit, procact.Term, procact.Kill, procact.DockerStop, procact.Renice} {
		add("procact."+act, procact.Plan(act, proc, procact.Subject{Port: 8080, Proto: "tcp"}, ctx))
	}
	lst := sysports.Listener{Socket: sysports.Socket{Proto: sysports.TCP, Local: netip.MustParseAddrPort("0.0.0.0:8080")}, PIDs: []int{4242}}
	for _, act := range []string{portscreen.ActStopUnit, portscreen.ActTerm, portscreen.ActKill, portscreen.ActDockerStop, portscreen.ActFindOwner} {
		add("ports."+act, portscreen.Plan(act, portscreen.Target{Listener: lst, Proc: &proc}, ctx))
	}

	// Firewall
	add("firewall.RuleAllow", firewall.RulePlan("allow", firewall.Target{Port: evil, Proto: "tcp", From: evil}, evil))
	add("firewall.RuleDeny", firewall.RulePlan("deny", firewall.Target{App: evil}, ""))
	add("firewall.EnableSSH", firewall.EnablePlan(firewall.Status{Installed: true, Readable: true}, firewall.SSHContext{Port: 22, InSession: true, SSHRunning: true}))
	add("firewall.Enable", firewall.EnablePlan(firewall.Status{Installed: true, Readable: true}, firewall.SSHContext{}))
	add("firewall.Disable", firewall.DisablePlan())
	add("firewall.Delete", firewall.DeletePlan(firewall.Rule{Num: 3, To: "8080/tcp", Action: "ALLOW"}, ""))
	add("firewall.DeleteSSH", firewall.DeletePlan(firewall.Rule{Num: 1, To: "22/tcp", Action: "ALLOW"}, "aturan terakhir untuk SSH"))
	add("firewall.Reset", firewall.ResetPlan())
	add("firewall.Install", firewall.InstallPlan())

	// User & SSH
	key, _ := users.ParseKey("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl budi@laptop")
	add("users.Add", users.AddUserPlan(evil, true))
	add("users.GroupAdd", users.GroupPlan(evil, "sudo", true))
	add("users.GroupRemove", users.GroupPlan("budi", "sudo", false))
	add("users.Lock", users.LockPlan(evil, true))
	add("users.Unlock", users.LockPlan("budi", false))
	add("users.Expire", users.ExpirePlan(evil))
	add("users.Delete", users.DeleteUserPlan(evil))
	add("users.AddKey", users.AddKeyPlan(evil, "/home/"+evil, key, false))
	add("users.AddKeySelf", users.AddKeyPlan("budi", "/home/budi", key, true))
	add("users.RemoveKey", users.RemoveKeyPlan("budi", "/home/budi", []users.Key{key, key}, 0, false, now))
	add("users.FixPerm", users.FixPermissionsPlan(evil, "/home/"+evil, false))
	add("users.GenKey", users.GenerateKeyPlan("/home/budi", evil))
	add("users.CopyID", users.CopyIDPlan("/home/budi/.ssh/id_ed25519.pub", evil))
	add("users.Hardening", users.HardeningPlan(users.HardeningInput{DisablePassword: true, DisableRoot: true, MaxAuthTries: 3, Port: "2222"},
		users.SafetyContext{CurrentUser: "budi", CurrentHasKey: true, CurrentIsSudo: true, UFWActive: true, SocketActivated: true, CurrentPort: "22"}, nil, now))
	add("users.InstallSSH", users.InstallSSHPlan())

	// Penjadwalan
	schedSpec := schedule.Spec{Name: "backup", Description: evil, Command: "/usr/local/bin/backup.sh", User: "root", Freq: schedule.Frequency{Kind: "daily", Hour: 2}}
	add("schedule.Cron", schedule.CreateCronPlan(schedSpec))
	add("schedule.Timer", must(schedule.CreateTimerPlan(schedSpec)))
	timer := schedule.Job{Kind: schedule.KindTimer, Name: "ubt-" + evil + ".timer", Unit: "ubt-" + evil + ".service", Source: "/etc/systemd/system/ubt-x.timer", Managed: true}
	cron := schedule.Job{Kind: schedule.KindCron, Name: "ubt-backup", Source: "/etc/cron.d/ubt-backup", Command: evil, User: "root", Managed: true}
	add("schedule.RunNowTimer", schedule.RunNowPlan(timer))
	add("schedule.RunNowCron", schedule.RunNowPlan(cron))
	userCron := cron
	userCron.User = evil
	add("schedule.RunNowCronUser", schedule.RunNowPlan(userCron))
	add("schedule.RunNowCronDir", schedule.RunNowPlan(schedule.Job{Kind: schedule.KindCronDir, Name: "logrotate", Command: "/etc/cron.daily/logrotate"}))
	add("schedule.RemoveTimer", schedule.RemovePlan(timer))
	add("schedule.RemoveCron", schedule.RemovePlan(cron))

	// Disk & Log
	add("disk.Swapfile", disk.SwapfilePlan("/swapfile", 2, now))
	add("logs.Persistent", logscreen.PersistentPlan())
	add("logs.Vacuum", logscreen.VacuumPlan(3<<30))
	add("logs.AddToAdm", logscreen.AddToAdmPlan(evil))

	// Network
	add("network.PublicIP", network.PublicIPPlan())
	add("network.Tracepath", network.TracepathPlan(evil))

	// Web & TLS
	site := web.Site{Name: evil, Path: "/etc/nginx/sites-available/" + evil}
	add("web.Proxy", web.ProxyPlan(web.ProxySpec{Domain: evil, Port: evil, WebSocket: true}, web.DefaultPaths, true))
	add("web.SiteEnable", web.SiteTogglePlan(site, web.DefaultPaths, true))
	add("web.SiteDisable", web.SiteTogglePlan(site, web.DefaultPaths, false))
	add("web.TestConfig", web.TestConfigPlan())
	add("web.InstallNginx", web.InstallNginxPlan())
	add("web.InstallCertbot", web.InstallCertbotPlan())
	add("web.Certbot", web.CertbotPlan("/usr/bin/certbot", evil, true))
	add("web.RenewDryRun", web.RenewDryRunPlan("/usr/bin/certbot"))
	add("web.ListCerts", web.ListCertsPlan("/usr/bin/certbot"))
	add("web.HTTPS", webscreen.HTTPSPlan("/usr/bin/certbot", evil, false))

	// Docker (dengan dan tanpa sudo)
	running := docker.Container{ID: "abc", Names: evil, Image: evil, State: "running", Labels: "com.docker.compose.project=shop"}
	stopped := docker.Container{ID: "def", Names: "coba", Image: "alpine", State: "exited"}
	for _, c := range []docker.Client{{}, {Sudo: true}} {
		p := "docker."
		if c.Sudo {
			p = "docker(sudo)."
		}
		add(p+"Start", c.StartPlan(stopped))
		add(p+"Stop", c.StopPlan(running))
		add(p+"Restart", c.RestartPlan(running))
		add(p+"RemoveRunning", c.RemovePlan(running))
		add(p+"RemoveStopped", c.RemovePlan(stopped))
		add(p+"Commit", c.CommitPlan(running, evil))
		add(p+"Exec", c.ExecShellPlan(running))
		add(p+"RunShell", c.RunShellPlan(evil, false, ""))
		add(p+"RunShellKeep", c.RunShellPlan("alpine", true, evil))
		add(p+"Pull", c.PullPlan(evil))
		add(p+"Rmi", c.RemoveImagePlan(docker.Image{ID: "1", Repository: evil, Tag: "latest"}))
		add(p+"Prune", c.PrunePlan(false, false))
		add(p+"PruneAll", c.PrunePlan(true, false))
		add(p+"PruneVolumes", c.PrunePlan(true, true))
		add(p+"Python", c.PythonRunPlan(evil, "/home/budi/"+evil, evil, 1000, 1000))
		add(p+"ComposeUp", c.ComposeUpPlan("/home/budi/"+evil+"/docker-compose.yml"))
	}
	// Docker: registry, arsip image, wizard run, build, volume & network
	reg := docker.Registry{Name: "nexus", Host: "nexus.contoh.com:8082", User: evil, Repo: "tim-a"}
	spec := docker.RunSpec{Name: evil, Image: evil, Ports: []string{"127.0.0.1:8080:80"}, Volumes: []string{"dbdata:/data"},
		Env: []string{"KEY=" + evil}, EnvFile: "/srv/" + evil + "/.env", Workdir: "/app", Command: []string{evil, "--flag"},
		User: "1000:1000", Network: evil, Restart: "unless-stopped", Memory: "512m", CPUs: "1.5", HealthCmd: evil, Detach: true}
	recipes := docker.Recipes{}.Upsert(spec)
	for _, c := range []docker.Client{{}, {Sudo: true}} {
		p := "docker."
		if c.Sudo {
			p = "docker(sudo)."
		}
		add(p+"Login", c.LoginPlan(reg))
		add(p+"Logout", c.LogoutPlan(reg.Host))
		add(p+"Push", c.PushPlan(evil, reg.Ref(evil)))
		add(p+"LoadImage", c.LoadImagePlan("/home/budi/"+evil+".tar"))
		add(p+"SaveImage", c.SaveImagePlan([]string{evil}, "/home/budi/"+evil+".tar"))
		add(p+"Run", c.RunPlan(spec, "/home/budi/.config/ubt/containers.json", recipes))
		add(p+"RunNoRecipe", c.RunPlan(docker.RunSpec{Name: "app", Image: "nginx", Interactive: true}, "", docker.Recipes{}))
		add(p+"Recreate", c.RecreatePlan(spec, true, true))
		add(p+"Build", c.BuildPlan(docker.BuildSpec{Context: "/home/budi/" + evil, Tag: evil, BuildArgs: []string{"VERSI=" + evil}, NoCache: true, Pull: true}))
		add(p+"VolumeCreate", c.CreateVolumePlan(evil))
		add(p+"VolumeRemove", c.RemoveVolumePlan(docker.Volume{Name: evil}, []string{"app"}))
		add(p+"VolumePrune", c.PruneVolumesPlan())
		add(p+"NetworkCreate", c.CreateNetworkPlan(evil))
		add(p+"NetworkRemove", c.RemoveNetworkPlan(docker.Network{Name: evil}, nil))
		add(p+"VolumeBackup", c.BackupVolumePlan(evil, "/home/budi/backup", evil+".tar.gz"))
		add(p+"VolumeRestore", c.RestoreVolumePlan(evil, "/home/budi/backup", evil+".tar.gz"))
	}
	add("docker.SaveRegistries", docker.SaveRegistriesPlan("/home/budi/.config/ubt/registries.json", docker.Registries{}.Upsert(reg), "Simpan profil"))
	add("docker.InstallCA", docker.InstallCAPlan(reg.Host, "/home/budi/"+evil+".crt"))
	add("docker.Insecure", docker.InsecureRegistryPlan(reg.Host, map[string]any{"log-driver": "json-file"}, true))
	add("docker.Install", docker.InstallPlan(evil))
	add("docker.AddGroup", run.Single(docker.AddGroupCommand(evil)))
	add("docker.StartDaemon", docker.StartDaemonPlan())

	// PostgreSQL
	pgc := postgres.Client{Port: 5432}
	cluster := postgres.Cluster{Version: "17", Name: "main", Port: 5432, Status: "online", DataDir: "/var/lib/postgresql/17/main", LogFile: "/var/log/postgresql/postgresql-17-main.log"}
	add("pg.Install", postgres.InstallPlan())
	add("pg.StartCluster", postgres.StartClusterPlan(cluster))
	add("pg.RestartCluster", postgres.RestartClusterPlan(cluster))
	add("pg.ReloadCluster", postgres.ReloadClusterPlan(cluster))
	add("pg.CreateDatabase", pgc.CreateDatabasePlan(evil, evil))
	add("pg.CreateDatabaseNoOwner", pgc.CreateDatabasePlan("toko", ""))
	add("pg.DropDatabase", pgc.DropDatabasePlan(evil))
	add("pg.CreateRole", pgc.CreateRolePlan(evil, postgres.RoleOptions{Login: true, CreateDB: true}, true))
	add("pg.CreateGroupRole", pgc.CreateRolePlan("laporan", postgres.RoleOptions{}, false))
	add("pg.CreateSuperuser", pgc.CreateRolePlan(evil, postgres.RoleOptions{Login: true, Superuser: true, CreateRole: true}, true))
	add("pg.SetPassword", pgc.SetPasswordPlan(evil))
	add("pg.DropRole", pgc.DropRolePlan(evil))
	for _, level := range []string{postgres.AccessRead, postgres.AccessWrite, postgres.AccessOwner} {
		add("pg.Grant."+level, pgc.GrantPlan(evil, evil, level))
	}
	add("pg.Revoke", pgc.RevokePlan(evil, evil))
	add("pg.Psql", pgc.PsqlPlan(evil))
	add("pg.QueryRead", pgc.QueryPlan(evil, "SELECT * FROM pesanan WHERE nama = '"+evil+"'"))
	add("pg.QueryWrite", pgc.QueryPlan("toko", "UPDATE pesanan SET status = 'lunas'"))
	add("pg.QueryDDL", pgc.QueryPlan("toko", "DROP TABLE pesanan"))
	add("pg.Terminate", pgc.TerminatePlan(4242, evil))
	add("pg.Cancel", pgc.CancelPlan(4242))
	add("pg.Extension", pgc.CreateExtensionPlan(evil, evil))
	add("pg.EnableStatements", postgres.EnableStatementsPlan(cluster))
	add("pg.Backup", pgc.BackupPlan(evil, "/var/backups/postgresql", postgres.BackupName(evil, postgres.FormatCustom, now), postgres.FormatCustom))
	add("pg.BackupPlain", pgc.BackupPlan("toko", "/var/backups/postgresql", postgres.BackupName("toko", postgres.FormatPlain, now), postgres.FormatPlain))
	add("pg.BackupRoles", pgc.BackupRolesPlan("/var/backups/postgresql", now))
	add("pg.RestoreCustom", pgc.RestorePlan("/var/backups/postgresql/"+evil+".dump", evil, true, true))
	add("pg.RestorePlain", pgc.RestorePlan("/var/backups/postgresql/toko.sql", "toko", false, false))
	add("pg.BackupScript", run.Single(postgres.BackupSchedule{Databases: []string{evil, "toko"}, Dir: "/var/backups/postgresql", KeepDays: 7, Hour: 2}.ScriptCommand()))
	add("pg.RemoteLocal", postgres.RemoteAccessPlan(cluster, postgres.ListenLocal, evil, evil, ""))
	add("pg.RemoteSpecific", postgres.RemoteAccessPlan(cluster, postgres.ListenSpecific, evil, evil, "10.8.0.4/32"))
	add("pg.RemoteAll", postgres.RemoteAccessPlan(cluster, postgres.ListenAll, "all", "all", "0.0.0.0/0"))
	add("pg.ApplySettings", postgres.ApplySettingsPlan(cluster, postgres.Tune(postgres.Server{RAMBytes: 8 << 30, CPUs: 4, SSD: true, Workload: postgres.WorkloadWeb})))
	add("pg.ApplySettingsReload", postgres.ApplySettingsPlan(cluster, []postgres.Tuned{{Name: "work_mem", Value: "8MB", Why: "uji"}}))

	// Riwayat
	add("history.Write", history.WritePlan("/home/budi/"+evil+".sh", "echo halo\n"))
	return out
}
