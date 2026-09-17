package disk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DeletedFile adalah file yang sudah dihapus tetapi masih dibuka proses, jadi ruang disknya belum kembali.
type DeletedFile struct {
	PID  int
	Path string // path sebelum dihapus
	Size int64
}

// DeletedOpen hasil pemindaian file terhapus yang masih dibuka.
type DeletedOpen struct {
	Files  []DeletedFile // terurut dari terbesar
	Denied int           // proses yang fd-nya tidak terbaca
}

// Total ukuran semua file terhapus yang masih dibuka.
func (d DeletedOpen) Total() int64 {
	var t int64
	for _, f := range d.Files {
		t += f.Size
	}
	return t
}

// FindDeletedOpen memindai /proc/*/fd untuk symlink berakhiran " (deleted)".
func FindDeletedOpen(procRoot string) (DeletedOpen, error) {
	var res DeletedOpen
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return res, err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(procRoot, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				res.Denied++
			}
			continue
		}
		seen := map[string]bool{}
		for _, fd := range fds {
			link := filepath.Join(fdDir, fd.Name())
			target, err := os.Readlink(link)
			if err != nil || !strings.HasSuffix(target, " (deleted)") || !strings.HasPrefix(target, "/") {
				continue
			}
			path := strings.TrimSuffix(target, " (deleted)")
			if seen[path] || strings.HasPrefix(path, "/memfd:") || strings.HasPrefix(path, "/dev/shm/") {
				continue
			}
			info, err := os.Stat(link) // mengikuti fd ke inode yang masih hidup
			if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
				continue
			}
			seen[path] = true
			res.Files = append(res.Files, DeletedFile{PID: pid, Path: path, Size: info.Size()})
		}
	}
	sort.Slice(res.Files, func(i, j int) bool { return res.Files[i].Size > res.Files[j].Size })
	return res, nil
}
