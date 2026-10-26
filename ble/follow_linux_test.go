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

// reportedWithin is how soon after BlueZ shows the link down the drop must be
// reported. It is well under status.GapTimeout (1 s), which would end a stalled
// reply anyway, so only the Follower's own watch of the link can meet it.
const reportedWithin = 500 * time.Millisecond

// dropAttempt is what one reading saw of a link dropped under it.
type dropAttempt struct {
	reply   string
	err     error
	written bool // the request was written before the reading ended
	// notifiedBeforeDown is how many notifications had arrived when the link
	// was first seen down; downAt is when that was, and returnedAt when the
	// reading returned.
	notifiedBeforeDown int32
	downAt, returnedAt time.Time
}

// watchDown reads the Connected property of the controller every 10 ms, until
// stop is closed, and sends the time it first reads false with the number of
// notifications counted by then.
func watchDown(adapter *Adapter, mac string, notified *atomic.Int32, stop <-chan struct{}) <-chan dropAttempt {
	seen := make(chan dropAttempt, 1)
	go func() {
		object := adapter.conn.Object("org.bluez", devicePath(adapter, mac))
		for {
			up, err := connected(context.Background(), object)
			if !up && (err == nil || isGone(err)) {
				seen <- dropAttempt{downAt: time.Now(), notifiedBeforeDown: notified.Load()}
				return
			}
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	return seen
}

// dropMidReply drops the link while a reply is arriving, and returns what the
// reading saw, with the follower holding a new, whole connection again. A
// held connection answers within about 200 ms of the write, but BlueZ takes
// 2.1 to 2.3 s to drop the link after bluetoothctl disconnect (measured on
// lola). So it tells bluetoothctl to disconnect a lead before it reads, and
// looks for the lead that lands the drop after the first notification and
// before the reply is whole. A lead that is too long drops the link before
// the write or before any notification; one that is too short lets the
// reply complete.
func dropMidReply(t *testing.T, f *Follower, mac string, notified *atomic.Int32) dropAttempt {
	t.Helper()
	ctl := startBluetoothctl(t)
	requireWholeReading(t, readWhole(t, f))
	lead, step := 1800*time.Millisecond, 50*time.Millisecond
	const attempts = 20
	for attempt := 1; attempt <= attempts; attempt++ {
		var written atomic.Bool
		f.conn.afterWrite = func() { written.Store(true) }
		notified.Store(0)
		stop := make(chan struct{})
		down := watchDown(f.adapter, mac, notified, stop)
		ctl.disconnect(mac)
		time.Sleep(lead)
		reply, _, err := followRead(f)
		returnedAt := time.Now()
		if f.conn != nil {
			f.conn.afterWrite = nil
		}
		var saw dropAttempt
		select {
		case saw = <-down:
		case <-time.After(10 * time.Second):
			t.Fatal("bluetoothctl disconnect left the link up")
		}
		close(stop)
		saw.reply, saw.err, saw.written, saw.returnedAt = reply, err, written.Load(), returnedAt

		switch {
		case err == nil:
			t.Logf("attempt %d, lead %v: reply completed before the drop (%d bytes)", attempt, lead, len(reply))
			lead += step
			if _, _, err := followRead(f); !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("reading after the late drop: error = %v, want ErrLinkDropped", err)
			}
		case !saw.written || saw.notifiedBeforeDown == 0:
			t.Logf("attempt %d, lead %v: link dropped before the reply began (written %v, %d notifications): %v", attempt, lead, saw.written, saw.notifiedBeforeDown, err)
			if !errors.Is(err, ErrLinkDropped) {
				t.Fatalf("error = %v, want ErrLinkDropped", err)
			}
			if lead > step {
				lead -= step
			}
			step = max(step/2, 10*time.Millisecond)
		default:
			t.Logf("attempt %d, lead %v: dropped mid-reply after %d notifications: %v, reported %v after the link went down", attempt, lead, saw.notifiedBeforeDown, err, saw.returnedAt.Sub(saw.downAt).Round(time.Millisecond))
			requireWholeReading(t, readWhole(t, f))
			return saw
		}
		readWhole(t, f)
	}
	t.Fatalf("the link was not dropped mid-reply in %d attempts", attempts)
	return dropAttempt{}
}

// followerCountingNotifications makes a Follower whose connections count the
// notifications they take in the returned counter.
func followerCountingNotifications(t *testing.T) (*Follower, string, *atomic.Int32) {
	t.Helper()
	f, mac := newFollower(t)
	notified := new(atomic.Int32)
	f.configure = func(c *Conn) { c.onNotified = func() { notified.Add(1) } }
	return f, mac, notified
}

func TestFollowDropMidReply(t *testing.T) {
	requireHardware(t)
	f, mac, notified := followerCountingNotifications(t)
	saw := dropMidReply(t, f, mac, notified)
	var replyErr *status.ReplyError
	if errors.As(saw.err, &replyErr) {
		t.Errorf("error = %v carries a reply error (%v), want a dropped link and not an incomplete reply", saw.err, replyErr)
	}
	if !errors.Is(saw.err, ErrLinkDropped) {
		t.Errorf("error = %v, want ErrLinkDropped", saw.err)
	}
	if saw.reply != "" {
		t.Errorf("reply = %q, want none", saw.reply)
	}
}

// TestFollowDropReportedPromptly drops the link after the first notification
// of a reply and before its end. The reply then goes quiet, which the reply's
// own timing would report as an incomplete reply a second later, and the
// reading's timeout after 20 s. The Follower watches the link, and reports
// the drop within reportedWithin of BlueZ showing it down.
func TestFollowDropReportedPromptly(t *testing.T) {
	requireHardware(t)
	f, mac, notified := followerCountingNotifications(t)
	saw := dropMidReply(t, f, mac, notified)
	reported := saw.returnedAt.Sub(saw.downAt)
	t.Logf("drop reported %v after the link went down (limit %v, gap timeout %v, reading timeout %v)", reported.Round(time.Millisecond), reportedWithin, status.GapTimeout, followReadBound)
	if !errors.Is(saw.err, ErrLinkDropped) {
		t.Errorf("error = %v, want ErrLinkDropped", saw.err)
	}
	if reported > reportedWithin {
		t.Errorf("drop reported %v after the link went down, want under %v", reported, reportedWithin)
	}
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

// TestFollowAdapterBlockedThenRecovers blocks the adapter with rfkill while a
// connection is held. Readings then fail, retryably, and once the adapter is
// unblocked the held connection is made again and read whole.
func TestFollowAdapterBlockedThenRecovers(t *testing.T) {
	requireRoot(t)
	f, _ := newFollower(t)
	restoreOnCleanup(t)

	requireWholeReading(t, readWhole(t, f))

	sh(t, "rfkill block bluetooth")
	time.Sleep(time.Second)
	for i := 1; i <= 2; i++ {
		reply, took, err := followRead(f)
		t.Logf("reading %d while blocked = %q, %v after %v", i, reply, err, took.Round(time.Millisecond))
		if err == nil {
			t.Errorf("reading %d while the adapter is blocked: no error, reply %q", i, reply)
			continue
		}
		if !errors.Is(err, ErrRetryable) || errors.Is(err, ErrFatalAtStartup) {
			t.Errorf("reading %d while blocked: error = %v, want ErrRetryable and not ErrFatalAtStartup", i, err)
		}
		if i == 1 && !errors.Is(err, ErrLinkDropped) {
			t.Errorf("reading 1 while blocked: error = %v, want the held link reported dropped", err)
		}
	}

	sh(t, "rfkill unblock bluetooth")
	if !waitPowered(t) {
		t.Fatal("adapter not powered 30s after unblocking it")
	}
	requireWholeReading(t, readWhole(t, f))
}

func TestFollowReadAfterClose(t *testing.T) {
	f := (&Adapter{}).Follow("C3:00:00:58:51:40")
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
	if _, err := f.Read(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Read() after Close: error = %v, want ErrNotConnected", err)
	}
}

// TestFollowReadAfterCloseLeavesNoLink closes a Follower that holds a link,
// and expects the next Read to make none.
func TestFollowReadAfterCloseLeavesNoLink(t *testing.T) {
	requireHardware(t)
	f, mac := newFollower(t)
	requireWholeReading(t, readWhole(t, f))
	if err := f.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := followRead(f); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Read() after Close: error = %v, want ErrNotConnected", err)
	}
	if linkUp(t, f.adapter, mac) {
		t.Error("link up after Close and a Read, want it down")
	}
}
