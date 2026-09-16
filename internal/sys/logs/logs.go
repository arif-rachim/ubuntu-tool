// Package logs membaca journal systemd (format JSON) dan file di /var/log. Murni pengumpul data.
package logs

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// Tingkat prioritas syslog.
const (
	PrioEmerg   = 0
	PrioAlert   = 1
	PrioCrit    = 2
	PrioErr     = 3
	PrioWarning = 4
	PrioNotice  = 5
	PrioInfo    = 6
	PrioDebug   = 7
)

// PriorityName adalah nama prioritas dalam bahasa Indonesia.
func PriorityName(p int) string {
	switch {
	case p <= PrioCrit:
		return "kritis"
	case p == PrioErr:
		return "error"
	case p == PrioWarning:
		return "peringatan"
	case p == PrioNotice:
		return "catatan"
	case p == PrioInfo:
		return "info"
	}
	return "debug"
}

// Entry adalah satu baris journal.
type Entry struct {
	Time       time.Time
	Priority   int
	Unit       string
	Identifier string
	PID        int
	Hostname   string
	Message    string
	Cursor     string // posisi entri di journal; dipakai --after-cursor supaya mode ikuti tidak melewatkan entri
}

// Source adalah nama pengirim yang paling informatif.
func (e Entry) Source() string {
	switch {
	case e.Identifier != "":
		return e.Identifier
	case e.Unit != "":
		return strings.TrimSuffix(e.Unit, ".service")
	}
	return "?"
}

// OutputFields adalah field journal yang dibaca (supaya output JSON tetap kecil).
var OutputFields = "__REALTIME_TIMESTAMP,PRIORITY,_SYSTEMD_UNIT,SYSLOG_IDENTIFIER,_PID,_HOSTNAME,MESSAGE,__CURSOR"

// ParseJSONLine mem-parse satu baris `journalctl -o json`. MESSAGE bisa berupa string, array byte
// (pesan non-UTF-8), array string (field berulang), atau null.
func ParseJSONLine(line []byte) (Entry, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, err
	}
	str := func(k string) string {
		v, ok := raw[k]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		var many []string
		if json.Unmarshal(v, &many) == nil && len(many) > 0 {
			return many[0]
		}
		var bytes []byte
		var nums []int
		if json.Unmarshal(v, &nums) == nil {
			for _, n := range nums {
				bytes = append(bytes, byte(n))
			}
			return strings.ToValidUTF8(string(bytes), "�")
		}
		return ""
	}
	e := Entry{
		Unit:       str("_SYSTEMD_UNIT"),
		Identifier: str("SYSLOG_IDENTIFIER"),
		Hostname:   str("_HOSTNAME"),
		Message:    str("MESSAGE"),
		Cursor:     str("__CURSOR"),
		Priority:   PrioInfo,
	}
	if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
		e.Time = time.UnixMicro(us)
	}
	if p, err := strconv.Atoi(str("PRIORITY")); err == nil {
		e.Priority = p
	}
	e.PID, _ = strconv.Atoi(str("_PID"))
	return e, nil
}

// Query adalah kriteria pembacaan journal.
type Query struct {
	Boot     *int   // nil = semua boot; 0 = boot ini; -1 = boot sebelumnya
	Kernel   bool   // hanya pesan kernel (-k)
	Priority int    // tampilkan prioritas ini dan yang lebih parah; -1 = semua
	Unit     string // -u UNIT
	UserUnit bool   // --user-unit
	Tag      string // -t SYSLOG_IDENTIFIER, mis. pesan dari logger -t
	Since    string // --since, mis. "1 hour ago"
	Grep     string // --grep
	Lines    int    // -n; 0 = default 300
}

// BootPtr membantu membuat Query.Boot.
func BootPtr(n int) *int { return &n }

// Args mengembalikan argv journalctl (tanpa -o json) untuk ditampilkan sebagai command setara.
func (q Query) Args() []string {
	args := []string{"journalctl"}
	if q.Boot != nil {
		if *q.Boot == 0 {
			args = append(args, "-b")
		} else {
			args = append(args, "-b", strconv.Itoa(*q.Boot))
		}
	}
	if q.Kernel {
		args = append(args, "-k")
	}
	if q.Priority >= 0 && q.Priority < PrioDebug {
		args = append(args, "-p", map[int]string{0: "emerg", 1: "alert", 2: "crit", 3: "err", 4: "warning", 5: "notice", 6: "info"}[q.Priority])
	}
	if q.Unit != "" {
		if q.UserUnit {
			args = append(args, "--user-unit", q.Unit)
		} else {
			args = append(args, "-u", q.Unit)
		}
	}
	if q.Tag != "" {
		args = append(args, "-t", q.Tag)
	}
	if q.Since != "" {
		args = append(args, "--since", q.Since)
	}
	if q.Grep != "" {
		args = append(args, "--grep", q.Grep)
	}
	n := q.Lines
	if n <= 0 {
		n = 300
	}
	return append(args, "-n", strconv.Itoa(n), "--no-pager")
}

// JSONArgs adalah Args ditambah format JSON untuk dibaca ubt.
func (q Query) JSONArgs() []string {
	return append(q.Args(), "-o", "json", "--output-fields="+OutputFields)
}

// FollowArgs adalah argv untuk mengikuti log secara langsung (journalctl -f). Bila afterCursor diisi,
// journal dilanjutkan tepat setelah entri itu, sehingga entri yang muncul di antara pembacaan awal
// dan mulainya mode ikuti tidak terlewat. Tanpa cursor, hanya entri baru yang tampil (-n 0).
func (q Query) FollowArgs(afterCursor string) []string {
	q.Lines = -1
	args := q.JSONArgs()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-n" {
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if afterCursor != "" {
		args = append(args, "--after-cursor", afterCursor)
	} else {
		args = append(args, "-n", "0")
	}
	return append(args, "-f")
}

// Result adalah hasil pembacaan journal.
type Result struct {
	Entries []Entry
	// Limited bila journalctl memberi tahu sebagian log tidak terlihat oleh user ini.
	Limited bool
}

// ParseOutput mem-parse output JSON baris-per-baris; baris rusak dilewati.
func ParseOutput(out string) []Entry {
	var entries []Entry
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		if e, err := ParseJSONLine(line); err == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// Read menjalankan query lewat runner.
func Read(ctx context.Context, r run.Runner, q Query) (Result, error) {
	out, stderr, err := r.Capture(ctx, run.Command{Argv: q.JSONArgs()})
	res := Result{
		Entries: ParseOutput(out),
		Limited: strings.Contains(stderr, "not seeing messages") || strings.Contains(stderr, "No journal files were opened due to insufficient permissions"),
	}
	if err != nil && len(res.Entries) == 0 {
		msg := strings.TrimSpace(stderr)
		switch {
		case strings.Contains(out+stderr, "No entries"):
			return res, nil
		case strings.Contains(msg, "Specifying boot ID or boot offset has no effect") || strings.Contains(msg, "Data from the specified boot"):
			return res, errors.New("journal boot sebelumnya tidak tersimpan (journal tidak persisten)")
		case msg != "":
			return res, errors.New(msg)
		}
		return res, err
	}
	return res, nil
}

// Boot adalah satu boot yang tercatat di journal.
type Boot struct {
	Index int       `json:"index"`
	ID    string    `json:"boot_id"`
	First time.Time `json:"-"`
	Last  time.Time `json:"-"`
}

// ParseBoots mem-parse `journalctl --list-boots -o json`.
func ParseBoots(out string) ([]Boot, error) {
	var raw []struct {
		Index int    `json:"index"`
		ID    string `json:"boot_id"`
		First int64  `json:"first_entry"`
		Last  int64  `json:"last_entry"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return nil, err
	}
	boots := make([]Boot, len(raw))
	for i, r := range raw {
		boots[i] = Boot{Index: r.Index, ID: r.ID, First: time.UnixMicro(r.First), Last: time.UnixMicro(r.Last)}
	}
	return boots, nil
}

// Health adalah kondisi penyimpanan journal.
type Health struct {
	Persistent bool  // /var/log/journal ada: log bertahan setelah reboot
	Usage      int64 // byte
	Boots      []Boot
}

// ReadHealth membaca kondisi journal.
func ReadHealth(ctx context.Context, r run.Runner, journalDir string) Health {
	var h Health
	if st, err := os.Stat(journalDir); err == nil && st.IsDir() {
		h.Persistent = true
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"journalctl", "--disk-usage"}}); err == nil {
		h.Usage = parseUsage(out)
	}
	if out, _, err := r.Capture(ctx, run.Command{Argv: []string{"journalctl", "--list-boots", "-o", "json", "--no-pager"}}); err == nil {
		h.Boots, _ = ParseBoots(out)
	}
	return h
}

func parseUsage(out string) int64 {
	i := strings.Index(out, "take up ")
	if i < 0 {
		return 0
	}
	f := strings.Fields(out[i+len("take up "):])
	if len(f) == 0 {
		return 0
	}
	s := f[0]
	j := 0
	for j < len(s) && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
		j++
	}
	v, _ := strconv.ParseFloat(s[:j], 64)
	mult := map[string]float64{"": 1, "B": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}[strings.ToUpper(s[j:])]
	return int64(v * mult)
}

// LogFile adalah satu file di /var/log.
type LogFile struct {
	Path     string
	Size     int64
	ModTime  time.Time
	Readable bool
	Rotated  bool // hasil rotasi (.1, .gz)
}

// ListFiles mendaftar file log (rekursif satu tingkat folder), terbaru lebih dulu, tanpa folder journal.
func ListFiles(dir string) ([]LogFile, error) {
	var files []LogFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			if rel == "journal" || strings.Count(rel, string(filepath.Separator)) >= 1 {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		f := LogFile{Path: path, Size: info.Size(), ModTime: info.ModTime(), Rotated: isRotated(d.Name())}
		if fh, err := os.Open(path); err == nil {
			f.Readable = true
			fh.Close()
		}
		files = append(files, f)
		return nil
	})
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Rotated != files[j].Rotated {
			return !files[i].Rotated
		}
		return files[i].ModTime.After(files[j].ModTime)
	})
	return files, err
}

func isRotated(name string) bool {
	if strings.HasSuffix(name, ".gz") {
		return true
	}
	i := strings.LastIndexByte(name, '.')
	_, err := strconv.Atoi(name[i+1:])
	return i >= 0 && err == nil
}

// Tail membaca n baris terakhir file log (file .gz didekompresi).
func Tail(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("tidak punya izin membaca %s", path)
		}
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	}
	ring := make([]string, 0, n)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if len(ring) == n {
			ring = append(ring[1:], sc.Text())
		} else {
			ring = append(ring, sc.Text())
		}
	}
	return ring, sc.Err()
}
