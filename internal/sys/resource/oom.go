package resource

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/arif-rachim/ubuntu-tool/internal/run"
)

// OOMEvent adalah satu kejadian OOM killer dari log kernel.
type OOMEvent struct {
	Time    time.Time
	PID     int
	Process string
}

// OOMCommand adalah command untuk membaca kejadian OOM sejak boot.
func OOMCommand() run.Command {
	return run.Command{Argv: []string{"journalctl", "-k", "-b", "--grep", "Out of memory|oom-kill", "-o", "short-iso", "--no-pager"}}
}

var oomKilledRe = regexp.MustCompile(`Killed process (\d+) \(([^)]*)\)`)

// ParseOOM mengambil kejadian "Killed process PID (nama)" dari output journalctl -o short-iso.
func ParseOOM(out string) []OOMEvent {
	var events []OOMEvent
	for _, line := range strings.Split(out, "\n") {
		m := oomKilledRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ev := OOMEvent{Process: m[2]}
		ev.PID, _ = strconv.Atoi(m[1])
		if f := strings.Fields(line); len(f) > 0 {
			ev.Time, _ = time.Parse("2006-01-02T15:04:05-0700", f[0])
			if ev.Time.IsZero() {
				ev.Time, _ = time.Parse(time.RFC3339, f[0])
			}
		}
		events = append(events, ev)
	}
	return events
}

// ReadOOM menjalankan journalctl lewat runner. "-- No entries --" (exit 1) berarti tidak ada kejadian.
func ReadOOM(ctx context.Context, r run.Runner) ([]OOMEvent, error) {
	out, stderr, err := r.Capture(ctx, OOMCommand())
	if err != nil {
		if strings.Contains(out+stderr, "No entries") {
			return nil, nil
		}
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			return nil, err
		}
		return nil, errors.New(msg)
	}
	return ParseOOM(out), nil
}
