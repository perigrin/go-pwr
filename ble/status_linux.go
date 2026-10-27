// ABOUTME: Sends the one status request to the controller on Linux and reads
// ABOUTME: the whole reply; the only code in this package that writes to it.

package ble

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/perigrin/go-pwr/status"
)

// The GATT objects of the controller, as docs/reference/protocol.md,
// "Connection", gives them. The request is written to requestUUID, under
// serviceUUID, and the reply arrives as notifications of replyUUID.
const (
	serviceUUID = "0000fff0-0000-1000-8000-00805f9b34fb"
	requestUUID = "0000fff2-0000-1000-8000-00805f9b34fb"
	replyUUID   = "0000fff1-0000-1000-8000-00805f9b34fb"

	gattServiceInterface = "org.bluez.GattService1"
	objectManagerGet     = "org.freedesktop.DBus.ObjectManager.GetManagedObjects"
	propertiesGet        = "org.freedesktop.DBus.Properties.Get"
)

// pollServices is how often ServicesResolved is read while waiting for it.
const pollServices = 50 * time.Millisecond

// stopNotifyBound is the longest Close waits for BlueZ to turn notifications
// off. The link is dropped right after, which ends them whatever BlueZ says.
const stopNotifyBound = 5 * time.Second

// RequestStatus sends the status request to the controller and returns its
// reply, whole, exactly as the controller sent it: a leading space, the body
// and a CRLF. It is the only operation that writes to the controller, and it
// writes one byte, 0x20, to FFF2 (see the package documentation).
//
// The first call turns on notifications of FFF1; Close turns them off. A
// call after Close, or on a Conn that Connect did not return, returns an
// error that matches ErrNotConnected and writes nothing.
//
// The reply must complete within the timing in the status package, and
// never after ctx ends. A reply that is cut off, silent or malformed is an
// error that wraps a *status.ReplyError with the bytes that arrived. A
// request that ctx abandoned may leave its reply still arriving; the next
// call waits for it to finish, or to time out, before it writes, so the two
// replies never mix. Every error other than ErrNotConnected matches
// ErrRetryable. Calls are serialised.
func (c *Conn) RequestStatus(ctx context.Context) (string, error) {
	if c == nil || c.adapter == nil {
		return "", fmt.Errorf("status request: %w: no connection was made", ErrNotConnected)
	}
	c.requests.Lock()
	defer c.requests.Unlock()
	select {
	case <-c.closing:
		return "", fmt.Errorf("status request: %w: the connection is closed", ErrNotConnected)
	default:
	}
	if err := ctx.Err(); err != nil {
		return "", retryableError(fmt.Errorf("status request: %w", err))
	}
	if c.link == nil {
		link, err := c.openLink(ctx)
		if err != nil {
			return "", retryableError(fmt.Errorf("status request: %w", err))
		}
		c.link = link
	}
	reply, err := c.link.request(ctx, c.closing, c.beforeWrite, c.afterWrite)
	if err != nil {
		if errors.Is(err, ErrNotConnected) {
			return "", fmt.Errorf("status request: %w", err)
		}
		return "", retryableError(fmt.Errorf("status request: %w", err))
	}
	return reply, nil
}

// openStatusLink opens the status link of c if it has none, as the first
// RequestStatus would, without writing anything. A link that fails to open
// is left closed.
func (c *Conn) openStatusLink(ctx context.Context) error {
	c.requests.Lock()
	defer c.requests.Unlock()
	select {
	case <-c.closing:
		return fmt.Errorf("status link: %w: the connection is closed", ErrNotConnected)
	default:
	}
	if c.link != nil {
		return nil
	}
	link, err := c.openLink(ctx)
	if err != nil {
		return err
	}
	c.link = link
	return nil
}

// statusLink is what a Conn needs to read status: a D-Bus connection of its
// own, the characteristics found on it, and the buffer of the current read.
//
// The request's write and the notification subscription use bus and nothing
// else. The ordering of a reply depends on it (see notificationConsumer).
type statusLink struct {
	bus         *dbus.Conn
	device      dbus.ObjectPath
	service     dbus.ObjectPath
	requestPath dbus.ObjectPath // FFF2
	replyPath   dbus.ObjectPath // FFF1
	buf         *readBuffer
	// onNotified, when set, runs on each notification taken, before it is
	// used.
	onNotified func()
	// notifyTried is set before notifications are asked for, so a teardown
	// after a failed or abandoned subscribe turns them off again.
	notifyTried bool

	// mu holds cur and orders each notification's add to buf with the update
	// of cur. Its lock is taken before buf's.
	mu   sync.Mutex
	cur  readState
	wake chan struct{}

	// unfinished says the last request was written but its reply did not
	// complete, so bytes of it may still arrive; since is when it was
	// written. Only the request path uses them, under Conn.requests.
	unfinished bool
	since      time.Time
}

// readState is the reply of the current read so far.
type readState struct {
	reply string
	whole bool
	err   error
	at    time.Time // when the latest accepted notification arrived
}

// openLink builds the status link of c: the dedicated connection, the two
// characteristics, and notifications on FFF1.
func (c *Conn) openLink(ctx context.Context) (*statusLink, error) {
	bus, err := connectOrdered()
	if err != nil {
		return nil, err
	}
	l := &statusLink{bus: bus, device: c.object.Path(), wake: make(chan struct{}, 1), onNotified: c.onNotified}
	drop := c.dropFunc()
	l.buf = newReadBuffer(drop)
	if err := l.open(ctx, drop); err != nil {
		l.close()
		return nil, err
	}
	return l, nil
}

// dropFunc is what hears of a notification that was not used.
func (c *Conn) dropFunc() func(error) {
	if c.onDrop != nil {
		return c.onDrop
	}
	return func(err error) { slog.Warn("status notification dropped", "err", err) }
}

func (l *statusLink) open(ctx context.Context, drop func(error)) error {
	var owner string
	if err := l.bus.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, "org.bluez").Store(&owner); err != nil {
		return fmt.Errorf("find BlueZ on the bus: %w", err)
	}
	if err := l.waitServicesResolved(ctx); err != nil {
		return err
	}
	if err := l.findCharacteristics(ctx); err != nil {
		return err
	}
	consumer := &notificationConsumer{path: l.replyPath, sender: owner, sink: l.notified, dropped: drop}
	signals := make(chan *dbus.Signal, 64)
	l.bus.Signal(signals)
	go consumer.consume(signals)
	if err := l.bus.AddMatchSignalContext(ctx, characteristicMatch(l.replyPath)...); err != nil {
		return fmt.Errorf("match notifications of %s: %w", l.replyPath, err)
	}
	l.notifyTried = true
	if err := l.bus.Object("org.bluez", l.replyPath).CallWithContext(ctx, gattCharacteristicInterface+".StartNotify", 0).Err; err != nil {
		return fmt.Errorf("turn on notifications of %s: %w", l.replyPath, err)
	}
	return nil
}

// waitServicesResolved waits until BlueZ has the controller's services. It is
// already so when Connect returns, in practice.
func (l *statusLink) waitServicesResolved(ctx context.Context) error {
	object := l.bus.Object("org.bluez", l.device)
	for {
		value, err := getProperty(ctx, object, deviceInterface, "ServicesResolved")
		if err != nil {
			return fmt.Errorf("read ServicesResolved of %s: %w", l.device, err)
		}
		if resolved, _ := value.Value().(bool); resolved {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for services of %s: %w", l.device, ctx.Err())
		case <-time.After(pollServices):
		}
	}
}

// findCharacteristics finds FFF2 and FFF1 under the FFF0 service of the
// device: the one characteristic of each UUID, in a service with the FFF0
// UUID that belongs to this device.
func (l *statusLink) findCharacteristics(ctx context.Context) error {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := l.bus.Object("org.bluez", "/").CallWithContext(ctx, objectManagerGet, 0).Store(&objects); err != nil {
		return fmt.Errorf("list the GATT objects of %s: %w", l.device, err)
	}
	var requests, replies, services []dbus.ObjectPath
	for path, interfaces := range objects {
		characteristic, ok := interfaces[gattCharacteristicInterface]
		if !ok || !strings.HasPrefix(string(path), string(l.device)+"/") {
			continue
		}
		uuid, _ := characteristic["UUID"].Value().(string)
		service, _ := characteristic["Service"].Value().(dbus.ObjectPath)
		if !sameUUID(uuid, requestUUID) && !sameUUID(uuid, replyUUID) {
			continue
		}
		if !isFFF0Service(objects[service][gattServiceInterface], l.device) {
			continue
		}
		if sameUUID(uuid, requestUUID) {
			requests = append(requests, path)
			services = append(services, service)
		} else {
			replies = append(replies, path)
		}
	}
	if len(requests) != 1 || len(replies) != 1 {
		return fmt.Errorf("find FFF2 and FFF1 under FFF0 of %s: found %d and %d", l.device, len(requests), len(replies))
	}
	l.requestPath, l.replyPath, l.service = requests[0], replies[0], services[0]
	return nil
}

// isFFF0Service reports whether service, the properties of a GattService1,
// is the FFF0 service of device.
func isFFF0Service(service map[string]dbus.Variant, device dbus.ObjectPath) bool {
	uuid, _ := service["UUID"].Value().(string)
	owner, _ := service["Device"].Value().(dbus.ObjectPath)
	return sameUUID(uuid, serviceUUID) && owner == device
}

func sameUUID(a, b string) bool {
	return strings.EqualFold(a, b)
}

// getProperty reads one property of object.
func getProperty(ctx context.Context, object dbus.BusObject, iface, name string) (dbus.Variant, error) {
	var value dbus.Variant
	err := object.CallWithContext(ctx, propertiesGet, 0, iface, name).Store(&value)
	return value, err
}

// notified takes one notification of FFF1 from the consumer, in order.
func (l *statusLink) notified(key uint64, chunk []byte) {
	if l.onNotified != nil {
		l.onNotified()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	reply, whole, err := l.buf.add(key, chunk)
	l.apply(reply, whole, err, time.Now())
}

// apply records what the buffer returned. A dropped or held notification
// returns an empty reply and changes nothing; at stamps only a notification
// that the assembler accepted, which is what completion timing counts. The
// caller holds mu.
func (l *statusLink) apply(reply string, whole bool, err error, at time.Time) {
	switch {
	case err != nil:
		if l.cur.err == nil {
			l.cur.err = err
		}
	case len(reply) > len(l.cur.reply):
		l.cur.reply, l.cur.whole, l.cur.at = reply, whole, at
	default:
		return
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// state returns the current read.
func (l *statusLink) state() readState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur
}

// request writes the status request and waits for its reply.
func (l *statusLink) request(ctx context.Context, closing <-chan struct{}, beforeWrite, afterWrite func()) (string, error) {
	if l.unfinished {
		if err := l.drain(ctx, closing); err != nil {
			return "", err
		}
	}
	target, err := l.confirm(ctx)
	if err != nil {
		return "", err
	}

	// The read begins before the write, so a notification that races the
	// write's reply is held, never lost; it is bounded after.
	l.mu.Lock()
	l.buf.begin()
	l.cur = readState{}
	l.mu.Unlock()

	if beforeWrite != nil {
		beforeWrite()
	}
	// Close may have begun since the checks; it must not meet a write.
	select {
	case <-closing:
		return "", fmt.Errorf("%w: the connection was closed", ErrNotConnected)
	default:
	}
	started := time.Now()
	call := target.send(ctx)
	if call.Err != nil {
		if dbusErrorName(call.Err) == "" {
			// The call may have been sent, so a reply may still come.
			l.unfinished, l.since = true, started
		}
		return "", fmt.Errorf("write the request: %w", call.Err)
	}
	l.unfinished, l.since = true, started
	if afterWrite != nil {
		afterWrite()
	}
	l.mu.Lock()
	reply, whole, err := l.buf.bound(call.ResponseSequence)
	l.apply(reply, whole, err, time.Now())
	l.mu.Unlock()

	limit, _ := ctx.Deadline()
	reply, err = l.await(ctx, closing, status.NewCompletion(started, limit))
	if err != nil {
		return "", err
	}
	l.unfinished = false
	return reply, nil
}

// await waits for the current read to complete, to fail, or to give up by
// comp, or for ctx or closing to end it.
func (l *statusLink) await(ctx context.Context, closing <-chan struct{}, comp *status.Completion) (string, error) {
	var seen time.Time
	for {
		cur := l.state()
		if cur.err != nil {
			return "", cur.err
		}
		if cur.whole {
			if n := l.buf.discarded(); n > 0 {
				slog.Debug("discarded bytes of an earlier reply before this reply", "discarded", n)
			}
			return cur.reply, nil
		}
		if !cur.at.IsZero() && !cur.at.Equal(seen) {
			comp.Notified(cur.at)
			seen = cur.at
		}
		now := time.Now()
		if err := comp.Check(now, cur.reply); err != nil {
			return "", l.noteDiscards(err)
		}
		timer := time.NewTimer(comp.Deadline().Sub(now))
		select {
		case <-l.wake:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", l.noteDiscards(fmt.Errorf("%w: %w", ctx.Err(), comp.Check(comp.Deadline(), cur.reply)))
		case <-closing:
			timer.Stop()
			return "", fmt.Errorf("%w: the connection was closed", ErrNotConnected)
		}
		timer.Stop()
	}
}

// noteDiscards says in err how many bytes of the read were discarded as the
// tail of an earlier reply, if any: when a read gives up, that is part of
// why. A read that succeeds says nothing in its result, since the discard did
// no harm; await leaves a debug line for it.
func (l *statusLink) noteDiscards(err error) error {
	if n := l.buf.discarded(); n > 0 {
		return fmt.Errorf("%w (discarded %d bytes before the reply's last space)", err, n)
	}
	return err
}

// drain waits for the reply of the last request, which did not complete, to
// finish or to be over: at the latest when its own timing would have given
// up. Bytes of it that arrive after the next request's write could not be
// told from that request's reply.
func (l *statusLink) drain(ctx context.Context, closing <-chan struct{}) error {
	comp := status.NewCompletion(l.since, time.Time{})
	var seen time.Time
	for {
		cur := l.state()
		if cur.whole {
			l.unfinished = false
			return nil
		}
		if !cur.at.IsZero() && !cur.at.Equal(seen) {
			comp.Notified(cur.at)
			seen = cur.at
		}
		now := time.Now()
		if !now.Before(comp.Deadline()) {
			l.unfinished = false
			return nil
		}
		timer := time.NewTimer(comp.Deadline().Sub(now))
		select {
		case <-l.wake:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for the last reply to end: %w", ctx.Err())
		case <-closing:
			timer.Stop()
			return fmt.Errorf("%w: the connection was closed", ErrNotConnected)
		}
		timer.Stop()
	}
}

// writeTarget is the characteristic the request may be written to. Only
// confirm makes one, so a value of this type is proof that the checks ran.
type writeTarget struct {
	object dbus.BusObject
}

// confirm checks, before every write, that the target is the characteristic
// with UUID FFF2 under the FFF0 service of the connected controller, and
// returns it. It only reads.
func (l *statusLink) confirm(ctx context.Context) (writeTarget, error) {
	if !strings.HasPrefix(string(l.requestPath), string(l.device)+"/") {
		return writeTarget{}, fmt.Errorf("%s is not under %s", l.requestPath, l.device)
	}
	up, err := connected(ctx, l.bus.Object("org.bluez", l.device))
	if err != nil {
		return writeTarget{}, fmt.Errorf("read Connected of %s: %w", l.device, err)
	}
	if !up {
		return writeTarget{}, fmt.Errorf("%s is not connected", l.device)
	}
	characteristic := l.bus.Object("org.bluez", l.requestPath)
	uuid, err := stringProperty(ctx, characteristic, gattCharacteristicInterface, "UUID")
	if err != nil {
		return writeTarget{}, err
	}
	if !sameUUID(uuid, requestUUID) {
		return writeTarget{}, fmt.Errorf("%s has UUID %s, want %s", l.requestPath, uuid, requestUUID)
	}
	owner, err := getProperty(ctx, characteristic, gattCharacteristicInterface, "Service")
	if err != nil {
		return writeTarget{}, fmt.Errorf("read Service of %s: %w", l.requestPath, err)
	}
	if service, _ := owner.Value().(dbus.ObjectPath); service != l.service {
		return writeTarget{}, fmt.Errorf("%s belongs to %v, want %s", l.requestPath, owner.Value(), l.service)
	}
	service := l.bus.Object("org.bluez", l.service)
	uuid, err = stringProperty(ctx, service, gattServiceInterface, "UUID")
	if err != nil {
		return writeTarget{}, err
	}
	if !sameUUID(uuid, serviceUUID) {
		return writeTarget{}, fmt.Errorf("%s has UUID %s, want %s", l.service, uuid, serviceUUID)
	}
	return writeTarget{object: characteristic}, nil
}

func stringProperty(ctx context.Context, object dbus.BusObject, iface, name string) (string, error) {
	value, err := getProperty(ctx, object, iface, name)
	if err != nil {
		return "", fmt.Errorf("read %s of %s: %w", name, object.Path(), err)
	}
	text, _ := value.Value().(string)
	return text, nil
}

// send writes the status request to the target. It is the one place in this
// package where a value is written to the controller, and the value is
// statusRequest, always. TestSingleStatusWriteCallSite holds both facts.
func (t writeTarget) send(ctx context.Context) *dbus.Call {
	options := map[string]dbus.Variant{"type": dbus.MakeVariant("request")}
	return t.object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}

// close turns notifications off, if they were asked for, and closes the
// connection, which also ends them in BlueZ and stops the consumer.
func (l *statusLink) close() error {
	var err error
	if l.notifyTried {
		ctx, cancel := context.WithTimeout(context.Background(), stopNotifyBound)
		stop := l.bus.Object("org.bluez", l.replyPath).CallWithContext(ctx, gattCharacteristicInterface+".StopNotify", 0).Err
		cancel()
		if stop != nil && !isGone(stop) && !strings.Contains(stop.Error(), "Not notifying") {
			err = fmt.Errorf("turn off notifications of %s: %w", l.replyPath, stop)
		}
	}
	if closeErr := l.bus.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close the status connection: %w", closeErr))
	}
	return err
}
