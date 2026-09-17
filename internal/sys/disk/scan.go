package disk

import (
	"container/heap"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"syscall"
)

// Node adalah satu folder hasil scan beserta total pemakaian disk isinya.
type Node struct {
	Name     string
	Path     string
	Size     int64 // byte terpakai di disk (st_blocks × 512), termasuk subfolder
	Files    int64 // jumlah file, termasuk subfolder
	Denied   int   // subfolder yang tidak bisa dibaca
	Children []*Node
	Parent   *Node
}

// File adalah satu file besar.
type File struct {
	Path string
	Size int64
}

// Progress dibaca UI selama scan berjalan.
type Progress struct {
	Files atomic.Int64
	Bytes atomic.Int64
	Dirs  atomic.Int64
}

// Result adalah hasil scan satu folder.
type Result struct {
	Root     *Node
	BigFiles []File // terurut dari terbesar
	Denied   int    // total folder yang tidak bisa dibaca
}

// ScanOptions mengatur scan.
type ScanOptions struct {
	MaxBigFiles int   // jumlah file besar yang disimpan
	MinBigFile  int64 // ukuran minimum file besar
}

// Scan menghitung pemakaian disk semua folder di bawah root tanpa menyeberang ke filesystem lain
// (seperti du -x). Hardlink dihitung sekali. Bisa dibatalkan lewat ctx.
func Scan(ctx context.Context, root string, opt ScanOptions, prog *Progress) (Result, error) {
	if prog == nil {
		prog = &Progress{}
	}
	st, err := os.Lstat(root)
	if err != nil {
		return Result{}, err
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return Result{}, errors.New("stat tidak didukung")
	}
	s := &scanner{ctx: ctx, dev: sys.Dev, prog: prog, opt: opt, inodes: map[uint64]bool{}}
	rootNode := &Node{Name: root, Path: root}
	if err := s.walk(rootNode); err != nil {
		return Result{}, err
	}
	big := make([]File, len(s.big))
	for i := len(s.big) - 1; i >= 0; i-- {
		big[i] = heap.Pop(&s.big).(File)
	}
	return Result{Root: rootNode, BigFiles: big, Denied: s.denied}, nil
}

type scanner struct {
	ctx    context.Context
	dev    uint64
	prog   *Progress
	opt    ScanOptions
	inodes map[uint64]bool
	big    fileHeap
	denied int
}

func (s *scanner) walk(n *Node) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.prog.Dirs.Add(1)
	entries, err := os.ReadDir(n.Path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			n.Denied++
			s.denied++
			return nil
		}
		return nil // folder hilang di tengah scan
	}
	for _, e := range entries {
		p := filepath.Join(n.Path, e.Name())
		info, err := os.Lstat(p)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if st.Dev != s.dev {
			continue // mount point filesystem lain
		}
		if info.IsDir() {
			child := &Node{Name: e.Name(), Path: p, Parent: n}
			if err := s.walk(child); err != nil {
				return err
			}
			child.Size += st.Blocks * 512
			n.Children = append(n.Children, child)
			n.Size += child.Size
			n.Files += child.Files
			n.Denied += child.Denied
			continue
		}
		if st.Nlink > 1 {
			if s.inodes[st.Ino] {
				continue
			}
			s.inodes[st.Ino] = true
		}
		size := st.Blocks * 512
		n.Size += size
		n.Files++
		s.prog.Files.Add(1)
		s.prog.Bytes.Add(size)
		if info.Mode().IsRegular() && s.opt.MaxBigFiles > 0 && size >= s.opt.MinBigFile {
			if s.big.Len() < s.opt.MaxBigFiles {
				heap.Push(&s.big, File{Path: p, Size: size})
			} else if size > s.big[0].Size {
				s.big[0] = File{Path: p, Size: size}
				heap.Fix(&s.big, 0)
			}
		}
	}
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Size > n.Children[j].Size })
	return nil
}

// fileHeap adalah min-heap berdasarkan ukuran, untuk menyimpan N file terbesar.
type fileHeap []File

func (h fileHeap) Len() int           { return len(h) }
func (h fileHeap) Less(i, j int) bool { return h[i].Size < h[j].Size }
func (h fileHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *fileHeap) Push(x any)        { *h = append(*h, x.(File)) }
func (h *fileHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
