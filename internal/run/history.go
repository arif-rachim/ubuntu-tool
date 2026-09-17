package run

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Entry adalah satu command yang benar-benar dijalankan.
type Entry struct {
	Time       time.Time `json:"time"`
	Title      string    `json:"title"`
	Argv       []string  `json:"argv"` // tanpa sudo
	Sudo       bool      `json:"sudo"`
	Check      bool      `json:"check,omitempty"`
	ExitCode   int       `json:"exit_code"`
	Error      string    `json:"error,omitempty"`
	DurationMs int64     `json:"duration_ms"`
	Stdin      string    `json:"stdin,omitempty"` // kosong bila Sensitive
	Hidden     bool      `json:"stdin_hidden,omitempty"`
}

// History menyimpan riwayat sebagai JSON satu objek per baris.
type History struct {
	Path string
}

// DefaultHistory mengembalikan riwayat di $XDG_CONFIG_HOME/ubt/history.log atau ~/.config/ubt/history.log.
func DefaultHistory() (*History, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &History{Path: filepath.Join(dir, "ubt", "history.log")}, nil
}

// NewEntry membuat Entry dari hasil satu langkah.
func NewEntry(step Step, sudo bool, started time.Time, exitCode int, runErr error) Entry {
	c := step.Command
	e := Entry{
		Time:       started,
		Title:      c.Title,
		Argv:       c.Argv,
		Sudo:       sudo,
		Check:      step.IsCheck,
		ExitCode:   exitCode,
		DurationMs: time.Since(started).Milliseconds(),
	}
	if runErr != nil {
		e.Error = runErr.Error()
	}
	if c.Stdin != "" {
		if c.Sensitive {
			e.Hidden = true
		} else {
			e.Stdin = c.Stdin
		}
	}
	return e
}

// Append menambah satu entri. Direktori dibuat 0700 dan file 0600 karena bisa berisi path & isi file sistem.
func (h *History) Append(e Entry) error {
	if err := os.MkdirAll(filepath.Dir(h.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		f.Close()
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	return errors.Join(werr, f.Close())
}

// Read membaca entri terakhir (paling baru di akhir). limit <= 0 berarti semua.
// Baris yang rusak dilewati, supaya satu baris korup tidak menghilangkan seluruh riwayat.
func (h *History) Read(limit int) ([]Entry, error) {
	f, err := os.Open(h.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, sc.Err()
}
