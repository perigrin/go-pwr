// ABOUTME: Tests that notifications reach the status assembler in order, using
// ABOUTME: real godbus signal-handler code in-process with hand-built signals.

package ble

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	// fff1Path is the notifying characteristic of the connected device.
	fff1Path = dbus.ObjectPath("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/service000c/char000d")
	// fff2Path is the write characteristic of the same device.
	fff2Path = dbus.ObjectPath("/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF/service000c/char0010")
	// otherFFF1Path is the notifying characteristic of another device.
	otherFFF1Path = dbus.ObjectPath("/org/bluez/hci0/dev_11_22_33_44_55_66/service000c/char000d")
)

// valueSignal hand-builds the PropertiesChanged signal BlueZ sends when a
// characteristic's Value changes. godbus fills Sequence in on a real
// connection; here the test does.
func valueSignal(path dbus.ObjectPath, seq uint64, chunk []byte) *dbus.Signal {
	return &dbus.Signal{
		Sender:   ":1.7",
		Path:     path,
		Name:     "org.freedesktop.DBus.Properties.PropertiesChanged",
		Body:     []any{"org.bluez.GattCharacteristic1", map[string]dbus.Variant{"Value": dbus.MakeVariant(chunk)}, []string{}},
		Sequence: dbus.Sequence(seq),
	}
}

// lolaChunks returns the notification payloads of the real framed reply
// captured on lola as session-1-reply-NN (n is 1 to 21), in the order the
// controller sent them. A line is "<microseconds> <hex payload>".
func lolaChunks(t *testing.T, n int) [][]byte {
	t.Helper()
	name := fmt.Sprintf("../status/testdata/lola-timing/session-1-reply-%02d.seq.txt", n)
	f, err := os.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()
	var chunks [][]byte
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") {
			continue
		}
		_, payload, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: malformed line %q", name, line)
		}
		b, err := hex.DecodeString(payload)
		if err != nil {
			t.Fatalf("%s: line %q: %v", name, line, err)
		}
		chunks = append(chunks, b)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return chunks
}

// sequentialHandler is godbus's sequential signal handler as a connection
// sees it: a SignalHandler that is also a SignalRegistrar and a Terminator.
type sequentialHandler interface {
	dbus.SignalHandler
	dbus.SignalRegistrar
	dbus.Terminator
}

// signalChannel returns the channel godbus delivers signals to, fed by a real
// sequential signal handler, and the handler to deliver through.
func signalChannel() (sequentialHandler, <-chan *dbus.Signal) {
	handler := dbus.NewSequentialSignalHandler().(sequentialHandler)
	ch := make(chan *dbus.Signal)
	handler.AddSignal(ch)
	return handler, ch
}

// delivered is what a consumer's sink saw.
type delivered struct {
	key   uint64
	chunk []byte
}

// recordSink returns a sink that records what it is given and a function that
// waits until the consumer has seen want notifications, then returns the
// record. godbus's handler drops signals still queued when it is terminated,
// so a test must wait for delivery before terminating it.
func recordSink(c *notificationConsumer, ch <-chan *dbus.Signal, want int) func() []delivered {
	var (
		mu  sync.Mutex
		got []delivered
	)
	done := make(chan struct{})
	c.sink = func(key uint64, chunk []byte) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, delivered{key, chunk})
		if len(got) == want {
			close(done)
		}
	}
	go c.consume(ch)
	return func() []delivered {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			mu.Lock()
			defer mu.Unlock()
			return got
		}
		mu.Lock()
		defer mu.Unlock()
		return got
	}
}

// TestOrderedDeliveryInProcess hands 4500 back-to-back signals to godbus's
// real sequential handler (the library's own notification callback reordered
// 3398 to 3501 of 4500) and checks the consumer sees them in order. The
// sequence numbers skip, as they do on a real connection where other
// messages share the counter.
func TestOrderedDeliveryInProcess(t *testing.T) {
	const n = 4500
	handler, ch := signalChannel()
	c := &notificationConsumer{path: fff1Path}
	wait := recordSink(c, ch, n)

	seq := uint64(100)
	for i := range n {
		var payload [8]byte
		binary.BigEndian.PutUint64(payload[:], uint64(i))
		handler.DeliverSignal("org.freedesktop.DBus.Properties", "PropertiesChanged", valueSignal(fff1Path, seq, payload[:]))
		seq += 1 + uint64(i%3)
	}
	got := wait()
	handler.Terminate()
	if len(got) != n {
		t.Fatalf("consumer saw %d notifications, want %d", len(got), n)
	}
	var last uint64
	for i, d := range got {
		if d.key <= last {
			t.Fatalf("notification %d has key %d, not above the one before it (%d)", i, d.key, last)
		}
		last = d.key
		if idx := binary.BigEndian.Uint64(d.chunk); idx != uint64(i) {
			t.Fatalf("notification %d carries payload %d: delivered out of order", i, idx)
		}
	}
}
