// ABOUTME: Tests holding one connection across readings against the real
// ABOUTME: controller on lola: dropped links, cut-off readings and recovery.

package ble

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/perigrin/go-pwr/status"
)

// followReadBound is the context deadline of a reading in these tests, the
// default --timeout.
const followReadBound = 20 * time.Second

// newFollower opens the adapter and returns a Follower for the test
// controller, which ends the test with the link down, however the test ends.
func newFollower(t *testing.T) (*Follower, string) {
	t.Helper()
	adapter := openAdapter(t)
	mac := os.Getenv("GO_PWR_TEST_MAC")
	ensureLinkDownOnCleanup(t, mac)
	f := adapter.Follow(mac)
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Errorf("Follower.Close() error = %v", err)
		}
	})
	return f, mac
}

// followRead is one reading with the default timeout.
func followRead(f *Follower) (string, time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), followReadBound)
	defer cancel()
	started := time.Now()
	reply, err := f.Read(ctx)
	return reply, time.Since(started), err
}

// readWhole reads until a reading is whole, trying again after a failed one
// the way a loop of readings would. BlueZ sometimes aborts a connect with
// le-connection-abort-by-local, most often right after a drop, which
// protocol.md ("Time budget on lola") records as an ordinary failed reading.
// A dropped link is never what is being retried: the caller has seen it.
func readWhole(t *testing.T, f *Follower) string {
	t.Helper()
	const attempts = 4
	for attempt := 1; ; attempt++ {
		reply, took, err := followRead(f)
		if err == nil {
			t.Logf("reading %d: whole reply after %v", attempt, took.Round(time.Millisecond))
			return reply
		}
		t.Logf("reading %d: error after %v = %v", attempt, took.Round(time.Millisecond), err)
		if errors.Is(err, ErrLinkDropped) {
			t.Fatalf("reading %d: error = %v, want the link to stay up", attempt, err)
		}
		if attempt == attempts {
			t.Fatalf("no whole reply in %d readings, last error = %v", attempts, err)
		}
		time.Sleep(time.Second)
	}
}

// dropLink drops the link from outside, as the controller going away would.
func dropLink(t *testing.T, f *Follower, mac string) {
	t.Helper()
	sh(t, "bluetoothctl disconnect "+mac)
	if !waitLinkDown(t, f.adapter, mac, 10*time.Second) {
		t.Fatal("bluetoothctl disconnect left the link up")
	}
}

func TestFollowDetectsDroppedLink(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)

	requireWholeReading(t, readWhole(t, f))

	dropLink(t, f, mac)
	reply, took, err := followRead(f)
	t.Logf("reading after the drop = %q, %v after %v", reply, err, took.Round(time.Millisecond))
	if !errors.Is(err, ErrLinkDropped) {
		t.Fatalf("reading after the drop: error = %v, want ErrLinkDropped", err)
	}
	if !errors.Is(err, ErrRetryable) {
		t.Errorf("reading after the drop: error = %v, want ErrRetryable", err)
	}
	if reply != "" {
		t.Errorf("reading after the drop: reply = %q, want none", reply)
	}

	requireWholeReading(t, readWhole(t, f))
}

// cutOffReading takes a reading on f whose context ends a short time after
// the request is written, while its reply is still arriving, and returns its
// error. A held connection answers within a few hundred milliseconds, so the
// time to cut is looked for downwards: a reading that completes anyway is
// whole, and is followed by one cut sooner.
func cutOffReading(t *testing.T, f *Follower) error {
	t.Helper()
	for _, cut := range []time.Duration{200, 100, 60, 40, 25, 15, 10, 5} {
		cut *= time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), followReadBound)
		f.conn.afterWrite = func() { time.AfterFunc(cut, cancel) }
		reply, err := f.Read(ctx)
		cancel()
		if f.conn != nil {
			f.conn.afterWrite = nil
		}
		if err != nil {
			t.Logf("reading cut off %v after the request: %v", cut, err)
			return err
		}
		t.Logf("reading cut %v after the request completed anyway: %d bytes", cut, len(reply))
	}
	t.Fatal("no reading could be cut off before its reply completed")
	return nil
}

func TestFollowNoSpliceAfterCutOffReading(t *testing.T) {
	requireHardware(t)
	f, _ := newFollower(t)
	drops := &dropRecorder{}
	f.configure = func(c *Conn) { c.onDrop = drops.record }

	requireWholeReading(t, readWhole(t, f))
	held := f.conn

	err := cutOffReading(t, f)
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("cut-off reading error = %v, want the context's error", err)
	}
	if errors.Is(err, ErrLinkDropped) {
		t.Errorf("cut-off reading error = %v, want a cut-off reply, not a dropped link", err)
	}

	// The tail of the cut-off reply must not reach the next one: it is read on
	// a connection of its own, made after the cut-off.
	reply := readWhole(t, f)
	requireWholeReading(t, reply)
	if f.conn == held {
		t.Error("the read after a cut-off reading used the connection the cut-off reply was arriving on")
	}
	for _, err := range drops.all() {
		t.Errorf("notification dropped: %v", err)
	}
}

// bluetoothctlSession is a running bluetoothctl that takes commands on its
// standard input, so that a disconnect can be issued within milliseconds of
// the request being written. Starting bluetoothctl for each disconnect takes
// longer than a reply does.
type bluetoothctlSession struct {
	t     *testing.T
	stdin io.WriteCloser
	cmd   *exec.Cmd
}

// startBluetoothctl starts the session, ends it when the test ends, and
// waits for it to be ready.
func startBluetoothctl(t *testing.T) *bluetoothctlSession {
	t.Helper()
	cmd := exec.Command("bluetoothctl")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("bluetoothctl stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start bluetoothctl: %v", err)
	}
	t.Cleanup(func() {
		io.WriteString(stdin, "quit\n")
		stdin.Close()
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	time.Sleep(time.Second)
	return &bluetoothctlSession{t: t, stdin: stdin, cmd: cmd}
}

// disconnect tells bluetoothctl to drop the link to mac, as
// `bluetoothctl disconnect` does.
func (b *bluetoothctlSession) disconnect(mac string) {
	if _, err := io.WriteString(b.stdin, "disconnect "+mac+"\n"); err != nil {
		b.t.Errorf("tell bluetoothctl to disconnect: %v", err)
	}
}

// promptly is how soon a drop must be reported: well under the 20 s timeout
// of the reading, with room for BlueZ to tell and for the link to be closed.
const promptly = 3 * time.Second

// TestFollowDropMidReply drops the link while the reply is arriving. A
// held connection answers within about 200 ms of the write, but BlueZ takes
// 2.1 to 2.3 s to drop the link after bluetoothctl disconnect (measured on
// lola, four drops). So the test tells bluetoothctl to disconnect a lead
// before it reads, and looks for the lead that lands the drop between the
// write and the end of the reply: a lead that is too long drops the link
// before the write, and one that is too short lets the reply complete.
func TestFollowDropMidReply(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)
	ctl := startBluetoothctl(t)

	requireWholeReading(t, readWhole(t, f))
	lead, step := 1800*time.Millisecond, 100*time.Millisecond
	const attempts = 14
	for attempt := 1; attempt <= attempts; attempt++ {
		written := false
		f.conn.afterWrite = func() { written = true }
		ctl.disconnect(mac)
		time.Sleep(lead)
		reply, took, err := followRead(f)
		if f.conn != nil {
			f.conn.afterWrite = nil
		}
		switch {
		case err == nil:
			t.Logf("attempt %d, lead %v: reply completed before the drop (%d bytes)", attempt, lead, len(reply))
			lead += step
			if !waitLinkDown(t, f.adapter, mac, 10*time.Second) {
				t.Fatal("bluetoothctl disconnect left the link up")
			}
			if _, _, err := followRead(f); !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("reading after the late drop: error = %v, want ErrLinkDropped", err)
			}
			readWhole(t, f)
		case !written:
			t.Logf("attempt %d, lead %v: link dropped before the write: %v", attempt, lead, err)
			if !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("error = %v, want ErrLinkDropped", err)
			}
			if lead > step {
				lead -= step
			}
			step = max(step/2, 10*time.Millisecond)
			readWhole(t, f)
		default:
			t.Logf("attempt %d, lead %v: dropped mid-reply: %v after %v", attempt, lead, err, took.Round(time.Millisecond))
			var replyErr *status.ReplyError
			if errors.As(err, &replyErr) {
				t.Errorf("error = %v carries a reply error (%v), want a dropped link and not an incomplete reply", err, replyErr)
			}
			if !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("error = %v, want ErrLinkDropped", err)
			}
			if took > promptly {
				t.Errorf("drop reported after %v, want under %v", took, promptly)
			}
			requireWholeReading(t, readWhole(t, f))
			return
		}
	}
	t.Fatalf("the link was not dropped mid-reply in %d attempts", attempts)
}

// TestFollowDropReportedPromptly drops the link after the checks that precede
// the write, so the request meets a dead link and no reply can come. A
// Follower that waited for the reply would report that after the whole
// timeout, as an incomplete reply or none; this one reports the drop as soon
// as BlueZ does.
func TestFollowDropReportedPromptly(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)

	requireWholeReading(t, readWhole(t, f))

	var downAt time.Time
	f.conn.beforeWrite = func() {
		sh(t, "bluetoothctl disconnect "+mac)
		waitLinkDown(t, f.adapter, mac, 10*time.Second)
		downAt = time.Now()
	}
	reply, _, err := followRead(f)
	reported := time.Since(downAt)
	t.Logf("reading = %q, %v, reported %v after the link went down (timeout %v)", reply, err, reported.Round(time.Millisecond), followReadBound)
	if downAt.IsZero() {
		t.Fatal("the link was never dropped: the request did not reach its write")
	}
	if !errors.Is(err, ErrLinkDropped) {
		t.Fatalf("error = %v, want ErrLinkDropped", err)
	}
	if reported > promptly {
		t.Errorf("drop reported %v after the link went down, want under %v and far under the %v timeout", reported, promptly, followReadBound)
	}
	requireWholeReading(t, readWhole(t, f))
}

// TestFollowRepeatedDrops drops the link five times in a row. Every drop must
// resubscribe cleanly: no error about a subscription that already exists, and
// the same number of notifications delivered for each reply, as a
// subscription made twice would deliver more.
func TestFollowRepeatedDrops(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)
	drops := &dropRecorder{}
	var delivered []*atomic.Int32
	f.configure = func(c *Conn) {
		count := new(atomic.Int32)
		delivered = append(delivered, count)
		c.onDrop = drops.record
		c.onNotified = func() { count.Add(1) }
	}

	const drops5 = 5
	perReply := make([]int32, 0, drops5+1)
	for i := 0; i <= drops5; i++ {
		if i > 0 {
			dropLink(t, f, mac)
			_, _, err := followRead(f)
			if !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("drop %d: reading after it: error = %v, want ErrLinkDropped", i, err)
			}
		}
		requireWholeReading(t, readWhole(t, f))
		perReply = append(perReply, delivered[len(delivered)-1].Load())
	}
	t.Logf("%d connections, notifications delivered for each first reply: %v", len(delivered), perReply)
	for i, n := range perReply {
		if n != perReply[0] {
			t.Errorf("reply %d took %d notifications, want %d as for the first", i, n, perReply[0])
		}
	}
	for _, err := range drops.all() {
		t.Errorf("notification dropped: %v", err)
	}
}
