// Package disk membaca filesystem, perangkat blok, pemakaian folder, file terhapus yang masih
// dibuka, kandidat bersih-bersih, dan swap. Murni pengumpul data.
package disk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Mount adalah satu filesystem yang terpasang beserta pemakaiannya.
type Mount struct {
	Source string
	Target string
	FSType string
	Size   int64 // byte
	Used   int64
	Avail  int64 // yang bisa dipakai user biasa (tanpa jatah cadangan root)
	Inodes int64
	IFree  int64
	Err    error // statfs gagal (mis. izin)
}

// UsePercent dihitung seperti df: used / (used + avail), dibulatkan ke atas.
func (m Mount) UsePercent() int {
	total := m.Used + m.Avail
	if total <= 0 {
		return 0
	}
	return int((m.Used*100 + total - 1) / total)
}

// InodePercent adalah persen inode terpakai.
func (m Mount) InodePercent() int {
	if m.Inodes <= 0 {
		return 0
	}
	used := m.Inodes - m.IFree
	return int((used*100 + m.Inodes - 1) / m.Inodes)
}

// pseudoFS adalah filesystem virtual yang tidak memakai disk.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true, "tmpfs": true, "cgroup": true, "cgroup2": true,
	"securityfs": true, "pstore": true, "bpf": true, "debugfs": true, "tracefs": true, "configfs": true,
	"fusectl": true, "mqueue": true, "hugetlbfs": true, "autofs": true, "binfmt_misc": true, "efivarfs": true,
	"squashfs": true, "overlay": true, "nsfs": true, "ramfs": true, "rpc_pipefs": true, "fuse.portal": true,
	"fuse.gvfsd-fuse": true, "fuse.snapfuse": true,
}

// Real melaporkan apakah filesystem memakai disk sungguhan (bukan virtual/snap/overlay container).
func (m Mount) Real() bool { return !pseudoFS[m.FSType] }

// ParseMountinfo mem-parse /proc/self/mountinfo.
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
func ParseMountinfo(s string) []Mount {
	var out []Mount
	for _, line := range strings.Split(s, "\n") {
		pre, post, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		pf := strings.Fields(pre)
		qf := strings.Fields(post)
		if len(pf) < 5 || len(qf) < 2 {
			continue
		}
		out = append(out, Mount{Target: unescapeMount(pf[4]), FSType: qf[0], Source: unescapeMount(qf[1])})
	}
	return out
}

// unescapeMount mengubah escape oktal (\040 = spasi) di mountinfo.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Statfs mengisi ukuran & inode dari statfs(2).
func Statfs(m *Mount) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.Target, &st); err != nil {
		m.Err = err
		return
	}
	bs := int64(st.Bsize)
	m.Size = int64(st.Blocks) * bs
	m.Used = (int64(st.Blocks) - int64(st.Bfree)) * bs
	m.Avail = int64(st.Bavail) * bs
	m.Inodes = int64(st.Files)
	m.IFree = int64(st.Ffree)
}

// ReadMounts membaca filesystem nyata (tanpa virtual), satu baris per perangkat sumber.
func ReadMounts(procRoot string, includePseudo bool) ([]Mount, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "self", "mountinfo"))
	if err != nil {
		return nil, err
	}
	all := ParseMountinfo(string(data))
	var out []Mount
	seen := map[string]int{} // source → indeks di out (bind mount dari sumber sama cukup sekali)
	for _, m := range all {
		if !includePseudo && !m.Real() {
			continue
		}
		if i, ok := seen[m.Source]; ok && m.Real() && strings.HasPrefix(m.Source, "/dev/") {
			if len(m.Target) < len(out[i].Target) {
				out[i].Target = m.Target
			}
			continue
		}
		seen[m.Source] = len(out)
		out = append(out, m)
	}
	for i := range out {
		Statfs(&out[i])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Target < out[j].Target })
	return out, nil
}

// MountFor mengembalikan mount yang memuat path (target terpanjang yang menjadi awalan path).
func MountFor(mounts []Mount, path string) (Mount, bool) {
	best, found := Mount{}, false
	for _, m := range mounts {
		t := m.Target
		if path == t || t == "/" || strings.HasPrefix(path, strings.TrimSuffix(t, "/")+"/") {
			if !found || len(t) > len(best.Target) {
				best, found = m, true
			}
		}
	}
	return best, found
}

// BlockDevice adalah satu baris lsblk.
type BlockDevice struct {
	Name        string        `json:"name"`
	Size        int64         `json:"size"`
	Type        string        `json:"type"`
	FSType      string        `json:"fstype"`
	Mountpoints []string      `json:"-"`
	Model       string        `json:"model"`
	Rotational  bool          `json:"-"`
	Children    []BlockDevice `json:"children"`
}

type rawBlockDevice struct {
	BlockDevice
	RawMountpoints []*string        `json:"mountpoints"`
	RawRota        json.RawMessage  `json:"rota"`
	RawChildren    []rawBlockDevice `json:"children"`
}

// LsblkArgs adalah argumen lsblk yang dipakai (dicatat supaya bisa ditampilkan sebagai command setara).
var LsblkArgs = []string{"lsblk", "--json", "-b", "-o", "NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS,MODEL,ROTA"}

// ParseLsblk mem-parse output lsblk --json. Kolom rota bisa bool atau "0"/"1" tergantung versi.
func ParseLsblk(data []byte) ([]BlockDevice, error) {
	var raw struct {
		Devices []rawBlockDevice `json:"blockdevices"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return convertDevices(raw.Devices), nil
}

func convertDevices(raw []rawBlockDevice) []BlockDevice {
	out := make([]BlockDevice, 0, len(raw))
	for _, r := range raw {
		d := r.BlockDevice
		for _, mp := range r.RawMountpoints {
			if mp != nil && *mp != "" {
				d.Mountpoints = append(d.Mountpoints, *mp)
			}
		}
		switch strings.Trim(string(r.RawRota), `"`) {
		case "true", "1":
			d.Rotational = true
		}
		d.Children = convertDevices(r.RawChildren)
		out = append(out, d)
	}
	return out
}

// Unmounted melaporkan partisi/disk berisi filesystem yang belum dipasang di mana pun.
func (d BlockDevice) Unmounted() bool {
	return d.FSType != "" && d.FSType != "swap" && len(d.Mountpoints) == 0 && len(d.Children) == 0
}
