// Package schedule membaca jadwal cron & systemd timer, menjelaskan ekspresinya dalam bahasa
// manusia, dan membangun command untuk membuat/menjalankan/menghapus jadwal. Murni pengumpul data.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CronExpr adalah ekspresi cron 5 kolom yang sudah di-parse.
type CronExpr struct {
	Raw     string
	Special string // @reboot, @daily, ... (kosong bila 5 kolom)
	fields  [5]field
}

type field struct {
	values map[int]bool
	any    bool // *
	raw    string
}

var fieldSpec = []struct {
	name     string
	min, max int
	names    map[string]int
}{
	{"menit", 0, 59, nil},
	{"jam", 0, 23, nil},
	{"tanggal", 1, 31, nil},
	{"bulan", 1, 12, map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}},
	{"hari", 0, 7, map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}},
}

var specials = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

// ParseCron mem-parse ekspresi cron standar (5 kolom) atau @special.
func ParseCron(s string) (CronExpr, error) {
	s = strings.TrimSpace(s)
	e := CronExpr{Raw: s}
	if strings.HasPrefix(s, "@") {
		if s == "@reboot" {
			e.Special = s
			return e, nil
		}
		expanded, ok := specials[s]
		if !ok {
			return e, fmt.Errorf("singkatan %s tidak dikenal", s)
		}
		e.Special = s
		s = expanded
	}
	parts := strings.Fields(s)
	if len(parts) != 5 {
		return e, fmt.Errorf("ekspresi cron harus 5 kolom (menit jam tanggal bulan hari), ada %d", len(parts))
	}
	for i, p := range parts {
		f, err := parseField(p, i)
		if err != nil {
			return e, fmt.Errorf("kolom %s %q: %w", fieldSpec[i].name, p, err)
		}
		e.fields[i] = f
	}
	return e, nil
}

func parseField(s string, idx int) (field, error) {
	spec := fieldSpec[idx]
	f := field{values: map[int]bool{}, raw: s}
	if s == "*" {
		f.any = true
	}
	for _, part := range strings.Split(s, ",") {
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 {
				return f, errors.New("langkah /N harus angka positif")
			}
			step, part = n, base
		}
		lo, hi := spec.min, spec.max
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err error
			if lo, err = value(a, spec.names); err != nil {
				return f, err
			}
			if hi, err = value(b, spec.names); err != nil {
				return f, err
			}
		default:
			v, err := value(part, spec.names)
			if err != nil {
				return f, err
			}
			lo, hi = v, v
			if step > 1 {
				hi = spec.max
			}
		}
		if lo < spec.min || hi > spec.max || lo > hi {
			return f, fmt.Errorf("di luar rentang %d-%d", spec.min, spec.max)
		}
		for v := lo; v <= hi; v += step {
			if idx == 4 && v == 7 {
				v = 0 // Minggu bisa ditulis 0 atau 7
				f.values[0] = true
				break
			}
			f.values[v] = true
		}
	}
	return f, nil
}

func value(s string, names map[string]int) (int, error) {
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q bukan angka", s)
	}
	return v, nil
}

func (f field) sorted() []int {
	out := make([]int, 0, len(f.values))
	for v := range f.values {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// Matches melaporkan apakah waktu t cocok dengan ekspresi (resolusi menit).
func (e CronExpr) Matches(t time.Time) bool {
	f := e.fields
	dom, dow := f[2].values[t.Day()], f[4].values[int(t.Weekday())]
	dayOK := dom && dow
	// Aturan cron: bila tanggal DAN hari sama-sama dibatasi, cukup salah satu yang cocok.
	if !f[2].any && !f[4].any {
		dayOK = dom || dow
	}
	return f[0].values[t.Minute()] && f[1].values[t.Hour()] && f[3].values[int(t.Month())] && dayOK
}

// Next mengembalikan n waktu berikutnya setelah from.
func (e CronExpr) Next(from time.Time, n int) []time.Time {
	if e.Special == "@reboot" {
		return nil
	}
	var out []time.Time
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(4 * 366 * 24 * time.Hour)
	for len(out) < n && t.Before(limit) {
		if !e.fields[3].values[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if e.Matches(t) {
			out = append(out, t)
		}
		t = t.Add(time.Minute)
	}
	return out
}

var hariNama = []string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
var bulanNama = []string{"", "Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}

// Describe menjelaskan ekspresi dalam bahasa Indonesia, mis. "setiap Senin pukul 02:00".
func (e CronExpr) Describe() string {
	if e.Special == "@reboot" {
		return "setiap kali server dinyalakan"
	}
	f := e.fields
	min, hour := f[0], f[1]
	var when string
	switch {
	case min.any && hour.any:
		when = "setiap menit"
	case stepOf(min.raw) > 0 && hour.any:
		when = fmt.Sprintf("setiap %d menit", stepOf(min.raw))
	case hour.any && len(min.values) == 1:
		when = fmt.Sprintf("setiap jam pada menit ke-%d", min.sorted()[0])
	case stepOf(hour.raw) > 0 && len(min.values) == 1:
		when = fmt.Sprintf("setiap %d jam pada menit ke-%d", stepOf(hour.raw), min.sorted()[0])
	case len(min.values) == 1 && len(hour.values) <= 4:
		var times []string
		for _, h := range hour.sorted() {
			times = append(times, fmt.Sprintf("%02d:%02d", h, min.sorted()[0]))
		}
		when = "pukul " + strings.Join(times, ", ")
	case len(min.values) == 1 && contiguous(hour.sorted()):
		hs := hour.sorted()
		m := min.sorted()[0]
		when = fmt.Sprintf("setiap jam pada menit ke-%d, dari pukul %02d:%02d sampai %02d:%02d", m, hs[0], m, hs[len(hs)-1], m)
	case hour.any && len(min.values) <= 12:
		when = "setiap jam pada menit " + joinInts(min.sorted())
	default:
		when = fmt.Sprintf("pada menit %s, jam %s", min.raw, hour.raw)
	}

	var day string
	switch {
	case f[2].any && f[4].any:
		if !strings.HasPrefix(when, "setiap") {
			day = "setiap hari"
		}
	case f[2].any:
		var names []string
		for _, d := range f[4].sorted() {
			names = append(names, hariNama[d])
		}
		if len(names) == 5 && !f[4].values[0] && !f[4].values[6] {
			day = "setiap hari kerja (Senin–Jumat)"
		} else {
			day = "setiap " + strings.Join(names, ", ")
		}
	case f[4].any:
		var ds []string
		for _, d := range f[2].sorted() {
			ds = append(ds, strconv.Itoa(d))
		}
		day = "setiap tanggal " + strings.Join(ds, ", ")
	default:
		day = "pada tanggal " + f[2].raw + " atau hari " + f[4].raw
	}
	month := ""
	if !f[3].any {
		var ms []string
		for _, m := range f[3].sorted() {
			ms = append(ms, bulanNama[m])
		}
		month = " di bulan " + strings.Join(ms, ", ")
	}
	return strings.TrimSpace(strings.TrimSpace(day+" "+when) + month)
}

func contiguous(xs []int) bool {
	for i := 1; i < len(xs); i++ {
		if xs[i] != xs[i-1]+1 {
			return false
		}
	}
	return len(xs) > 1
}

func joinInts(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ", ")
}

func stepOf(raw string) int {
	if base, st, ok := strings.Cut(raw, "/"); ok && (base == "*" || base == "0") {
		n, _ := strconv.Atoi(st)
		return n
	}
	return 0
}
