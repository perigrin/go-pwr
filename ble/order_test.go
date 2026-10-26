// ABOUTME: Tests that notifications reach the status assembler in order, using
// ABOUTME: real godbus signal-handler code in-process with hand-built signals.

package ble

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/perigrin/go-pwr/status"
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

// TestOrderedDeliveryIgnoresOtherPaths feeds signals for other devices, other
// characteristics, other interfaces and other members through the real
// handler, between the notifications that count, and checks that only
// FFF1 of the connected device reaches the sink.
func TestOrderedDeliveryIgnoresOtherPaths(t *testing.T) {
	handler, ch := signalChannel()
	c := &notificationConsumer{path: fff1Path}
	wait := recordSink(c, ch, 3)

	otherInterface := valueSignal(fff1Path, 0, []byte("other interface"))
	otherInterface.Body[0] = "org.bluez.Device1"
	otherMember := valueSignal(fff1Path, 0, []byte("other member"))
	otherMember.Name = "org.freedesktop.DBus.Properties.PropertiesChanged2"
	interfacesAdded := valueSignal(fff1Path, 0, []byte("interfaces added"))
	interfacesAdded.Name = "org.freedesktop.DBus.ObjectManager.InterfacesAdded"
	notifying := valueSignal(fff1Path, 0, nil)
	notifying.Body[1] = map[string]dbus.Variant{"Notifying": dbus.MakeVariant(true)}

	signals := []*dbus.Signal{
		valueSignal(fff1Path, 1, []byte("first")),
		valueSignal(fff2Path, 2, []byte("write characteristic")),
		valueSignal(otherFFF1Path, 3, []byte("other device")),
		valueSignal(fff1Path+"/desc0010", 4, []byte("descriptor of FFF1")),
		valueSignal(fff1Path[:len(fff1Path)-1], 5, []byte("path prefix")),
		valueSignal("/", 6, []byte("root")),
		otherInterface, otherMember, interfacesAdded, notifying,
		valueSignal(fff1Path, 7, []byte("second")),
		nil,
		valueSignal(fff1Path, 8, []byte("third")),
	}
	for i, sig := range signals {
		if sig != nil && sig.Sequence == 0 {
			sig.Sequence = dbus.Sequence(100 + i)
		}
		handler.DeliverSignal("org.freedesktop.DBus.Properties", "PropertiesChanged", sig)
	}

	var texts []string
	for _, d := range wait() {
		texts = append(texts, string(d.chunk))
	}
	if got, want := strings.Join(texts, "|"), "first|second|third"; got != want {
		t.Errorf("sink saw %q, want %q", got, want)
	}
	handler.Terminate()
}

// TestOrderedDeliveryDropsUnusableSignals checks that a signal for FFF1 that
// cannot be ordered or read is reported, never passed on.
func TestOrderedDeliveryDropsUnusableSignals(t *testing.T) {
	noSequence := valueSignal(fff1Path, 0, []byte("no sequence"))
	shortBody := valueSignal(fff1Path, 2, nil)
	shortBody.Body = shortBody.Body[:1]
	badChanged := valueSignal(fff1Path, 3, nil)
	badChanged.Body[1] = "not a map"
	badValue := valueSignal(fff1Path, 4, nil)
	badValue.Body[1] = map[string]dbus.Variant{"Value": dbus.MakeVariant("not bytes")}

	var reasons []error
	var sunk []delivered
	c := &notificationConsumer{
		path:    fff1Path,
		sink:    func(key uint64, chunk []byte) { sunk = append(sunk, delivered{key, chunk}) },
		dropped: func(err error) { reasons = append(reasons, err) },
	}
	for _, sig := range []*dbus.Signal{noSequence, shortBody, badChanged, badValue} {
		c.handle(sig)
	}
	if len(sunk) != 0 {
		t.Errorf("sink saw %d unusable signals, want none", len(sunk))
	}
	if len(reasons) != 4 {
		t.Fatalf("dropped heard of %d signals, want 4: %v", len(reasons), reasons)
	}
	if !errors.Is(reasons[0], errNoSequence) {
		t.Errorf("signal without a sequence dropped for %v, want errNoSequence", reasons[0])
	}
	for _, err := range reasons[1:] {
		if !errors.Is(err, errBadSignal) {
			t.Errorf("malformed signal dropped for %v, want errBadSignal", err)
		}
	}
}

// readResult is what one add to the read buffer returned.
type readResult struct {
	reply string
	whole bool
	err   error
}

// readThrough runs one read of chunks through handler. seqs gives each
// chunk's sequence number. It returns the final add's result.
func readThrough(t *testing.T, handler sequentialHandler, b *readBuffer, results <-chan readResult, boundary uint64, chunks [][]byte, seqs []uint64) readResult {
	t.Helper()
	b.begin()
	if _, _, err := b.bound(dbus.Sequence(boundary)); err != nil {
		t.Fatal(err)
	}
	for i, chunk := range chunks {
		handler.DeliverSignal("org.freedesktop.DBus.Properties", "PropertiesChanged", valueSignal(fff1Path, seqs[i], chunk))
	}
	var last readResult
	for range chunks {
		select {
		case last = <-results:
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the consumer")
		}
	}
	return last
}

// consumerToBuffer wires a consumer to a read buffer and returns the results
// of every add, in order.
func consumerToBuffer(ch <-chan *dbus.Signal, b *readBuffer) <-chan readResult {
	results := make(chan readResult, 64)
	c := &notificationConsumer{path: fff1Path}
	c.sink = func(key uint64, chunk []byte) {
		reply, whole, err := b.add(key, chunk)
		results <- readResult{reply, whole, err}
	}
	go c.consume(ch)
	return results
}

// TestOrderedDeliveryAssemblesRealReplies runs all 21 real replies through
// the handler, with gaps in the sequence numbers, and checks each comes out
// whole and byte for byte.
func TestOrderedDeliveryAssemblesRealReplies(t *testing.T) {
	handler, ch := signalChannel()
	defer handler.Terminate()
	b := newReadBuffer(nil)
	results := consumerToBuffer(ch, b)

	seq := uint64(10)
	for n := 1; n <= 21; n++ {
		chunks := lolaChunks(t, n)
		boundary := seq
		seqs := make([]uint64, len(chunks))
		for i := range chunks {
			seq += 1 + uint64((n+i)%4) // gaps: other messages share the counter
			seqs[i] = seq
		}
		got := readThrough(t, handler, b, results, boundary, chunks, seqs)
		var want strings.Builder
		for _, c := range chunks {
			want.Write(c)
		}
		if got.err != nil || !got.whole || got.reply != want.String() {
			t.Fatalf("reply %d: got %q whole %v err %v, want whole %q", n, got.reply, got.whole, got.err, want.String())
		}
	}
}

// TestOrderedDeliveryRefusesReorderedSequence delivers a real reply with two
// middle chunks swapped, as the library's callback did. The swap passes the
// whole-reply rule on its own, so only the keys catch it: the read must fail
// and never come out whole.
func TestOrderedDeliveryRefusesReorderedSequence(t *testing.T) {
	handler, ch := signalChannel()
	defer handler.Terminate()
	b := newReadBuffer(nil)
	results := consumerToBuffer(ch, b)

	chunks := lolaChunks(t, 1)
	chunks[3], chunks[4] = chunks[4], chunks[3]
	seqs := make([]uint64, len(chunks))
	for i := range seqs {
		seqs[i] = uint64(11 + i)
	}
	// Swapped chunks reach the consumer carrying the sequence numbers godbus
	// stamped them with, so the keys fall where the chunks were swapped.
	seqs[3], seqs[4] = seqs[4], seqs[3]

	got := readThrough(t, handler, b, results, 10, chunks, seqs)
	if got.whole {
		t.Fatalf("a reordered read came out whole: %q", got.reply)
	}
	if !errors.Is(got.err, status.ErrKeyOrder) {
		t.Errorf("reordered read ended with %v, want status.ErrKeyOrder", got.err)
	}
}
