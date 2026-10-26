// ABOUTME: Tests the give-up timing against the real notification timings
// ABOUTME: captured on lola, replaying them with timestamps and no real time.

package status

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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

// replay feeds the real sequence n to an Assembler and a Completion in
// order, stamping each notification with its captured time after
// the request. Before each one it checks the Completion has not given up.
// It returns the assembler's reply so far and the Completion.
func replay(t *testing.T, n, drop int) (*Completion, string) {
	t.Helper()
	stamps, err := readStamps(fmt.Sprintf("%s/session-1-reply-%02d.seq.txt", lolaTimingDir, n))
	if err != nil {
		t.Fatal(err)
	}
	chunks := lolaSequence(t, n)
	request := time.Date(2026, 10, 25, 22, 8, 0, 0, time.UTC)
	c := NewCompletion(request, time.Time{})
	a := NewAssembler()
	tok := a.Begin(0)
	var reply string
	for i := range chunks[:len(chunks)-drop] {
		at := request.Add(stamps[i])
		if err := c.Check(at, reply); err != nil {
			t.Fatalf("reply-%02d gave up before notification %d: %v", n, i+1, err)
		}
		var err error
		if reply, _, err = a.Add(tok, uint64(i+1), chunks[i]); err != nil {
			t.Fatal(err)
		}
		c.Notified(at)
	}
	return c, reply
}

// TestCompletionNeverGivesUpOnCapturedReplies replays all 21 real replies
// with their real timestamps: none is abandoned along the way.
func TestCompletionNeverGivesUpOnCapturedReplies(t *testing.T) {
	files, err := filepath.Glob(lolaTimingDir + "/*.seq.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no captured sequences (err %v)", err)
	}
	for n := 1; n <= len(files); n++ {
		replay(t, n, 0)
	}
}

func TestCompletionCutOffSequenceGivesUp(t *testing.T) {
	c, reply := replay(t, 1, 1)
	if reply == "" || strings.HasSuffix(reply, "\r\n") {
		t.Fatalf("setup: reply %q should be a non-empty cut-off", reply)
	}
	last := c.lastAt
	if err := c.Check(last.Add(GapTimeout-time.Millisecond), reply); err != nil {
		t.Fatalf("gave up before the gap timeout: %v", err)
	}
	err := c.Check(last.Add(GapTimeout), reply)
	if !errors.Is(err, ErrIncompleteReply) {
		t.Fatalf("Check at the gap timeout = %v, want ErrIncompleteReply", err)
	}
	line := ErrorLine(err)
	if !strings.Contains(line, strconv.Quote(reply)) {
		t.Errorf("error line %q does not quote what arrived, %q", line, reply)
	}
	if _, werr := Decode(reply); werr == nil {
		t.Error("a cut-off reply decoded: it must never be reported as a reading")
	}
}

func TestCompletionNoNotifications(t *testing.T) {
	request := time.Date(2026, 10, 25, 22, 8, 0, 0, time.UTC)
	c := NewCompletion(request, time.Time{})
	if err := c.Check(request.Add(FirstNotificationTimeout-time.Millisecond), ""); err != nil {
		t.Fatalf("gave up before the first-notification timeout: %v", err)
	}
	err := c.Check(request.Add(FirstNotificationTimeout), "")
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("Check at the timeout = %v, want ErrNoReply", err)
	}
	if line := ErrorLine(err); !strings.Contains(line, "no reply received") {
		t.Errorf("error line %q does not say no reply was received", line)
	}
}

// TestCompletionLimitCapsDeadline: a --timeout budget that ends sooner than
// the timing wins.
func TestCompletionLimitCapsDeadline(t *testing.T) {
	request := time.Date(2026, 10, 25, 22, 8, 0, 0, time.UTC)
	limit := request.Add(500 * time.Millisecond)
	c := NewCompletion(request, limit)
	if got := c.Deadline(); !got.Equal(limit) {
		t.Errorf("Deadline = %v, want the limit %v", got, limit)
	}
	if err := c.Check(limit, ""); !errors.Is(err, ErrNoReply) {
		t.Errorf("Check at the limit = %v, want ErrNoReply", err)
	}
}

// defaultTimeout is the default --timeout in cmd/go-pwr/flags.go.
const defaultTimeout = 20 * time.Second

var (
	scanSeen    = regexp.MustCompile(`seen (\d+(?:\.\d+)?) s into a scan`)
	connectDisc = regexp.MustCompile(`services resolved: (\d+(?:\.\d+)?) s with discovery`)
)

// seconds returns the first group of re's match in text as a duration.
func seconds(t *testing.T, text string, re *regexp.Regexp) time.Duration {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("protocol.md has no match for %v", re)
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	return time.Duration(f * float64(time.Second))
}

// TestCompletionFitsTimeoutBudget reads the scan and connect times of the
// lola timing capture's first look from protocol.md (the 01a17532-5787
// capture) and checks scan + connect + the longest wait after the request
// stays inside the default --timeout.
func TestCompletionFitsTimeoutBudget(t *testing.T) {
	b, err := os.ReadFile("../docs/reference/protocol.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	scan := seconds(t, text, scanSeen)
	connect := seconds(t, text, connectDisc)
	worst := max(FirstNotificationTimeout, TotalTimeout)
	if total := scan + connect + worst; total >= defaultTimeout {
		t.Errorf("scan %v + connect %v + give-up %v = %v, want under the default timeout %v", scan, connect, worst, total, defaultTimeout)
	}
}
