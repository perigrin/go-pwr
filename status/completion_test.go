// ABOUTME: Tests the give-up timing against the real notification timings
// ABOUTME: captured on lola, replaying them with timestamps and no real time.

package status

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// capturedTiming is the longest of each measure over a set of captured
// sequences.
type capturedTiming struct {
	Sequences int
	First     time.Duration // request to first notification
	Gap       time.Duration // between two notifications
	Total     time.Duration // request to last notification
}

// readStamps returns the microsecond stamps of one sequence file as
// durations since the request write.
func readStamps(name string) ([]time.Duration, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var stamps []time.Duration
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		us, _, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("%s: malformed line %q", name, line)
		}
		n, err := strconv.ParseInt(us, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: line %q: %w", name, line, err)
		}
		stamps = append(stamps, time.Duration(n)*time.Microsecond)
	}
	return stamps, sc.Err()
}

// measureTiming reads every *.seq.txt in dir. It is an error for dir to
// hold none, so a missing capture can't pass as "no gap was too long".
func measureTiming(dir string) (capturedTiming, error) {
	var m capturedTiming
	files, err := filepath.Glob(filepath.Join(dir, "*.seq.txt"))
	if err != nil {
		return m, err
	}
	if len(files) == 0 {
		return m, fmt.Errorf("no captured sequences in %s", dir)
	}
	for _, name := range files {
		stamps, err := readStamps(name)
		if err != nil {
			return m, err
		}
		if len(stamps) == 0 {
			return m, fmt.Errorf("%s has no notifications", name)
		}
		m.Sequences++
		m.First = max(m.First, stamps[0])
		m.Total = max(m.Total, stamps[len(stamps)-1])
		for i := 1; i < len(stamps); i++ {
			m.Gap = max(m.Gap, stamps[i]-stamps[i-1])
		}
	}
	return m, nil
}

const lolaTimingDir = "testdata/lola-timing"

func TestCompletionTimingCoversCapturedGaps(t *testing.T) {
	m, err := measureTiming(lolaTimingDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d sequences: longest first %v, gap %v, total %v", m.Sequences, m.First, m.Gap, m.Total)
	for _, c := range []struct {
		name     string
		limit    time.Duration
		measured time.Duration
	}{
		{"FirstNotificationTimeout", FirstNotificationTimeout, m.First},
		{"GapTimeout", GapTimeout, m.Gap},
		{"TotalTimeout", TotalTimeout, m.Total},
	} {
		if need := c.measured * TimingMargin; c.limit < need {
			t.Errorf("%s = %v, want at least %v (measured %v x %d)", c.name, c.limit, need, c.measured, TimingMargin)
		}
	}
}

// TestCompletionTimingRequiresCaptures: the measurement refuses a directory
// with no sequence files, so the covering test can't pass on nothing.
func TestCompletionTimingRequiresCaptures(t *testing.T) {
	if m, err := measureTiming(t.TempDir()); err == nil {
		t.Fatalf("measureTiming of an empty directory = %+v, nil; want an error", m)
	}
}

var _ = errors.New
