// ABOUTME: Consumes BlueZ characteristic notifications from godbus signals in
// ABOUTME: the order they arrived, keyed by their D-Bus sequence number.

package ble

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	propertiesInterface         = "org.freedesktop.DBus.Properties"
	propertiesChangedMember     = "PropertiesChanged"
	propertiesChangedName       = propertiesInterface + "." + propertiesChangedMember
	gattCharacteristicInterface = "org.bluez.GattCharacteristic1"
	valueProperty               = "Value"
)

// Why a signal for the notifying characteristic could not be used.
var (
	errNoSequence = errors.New("signal has no sequence number")
	errBadSignal  = errors.New("signal is not a characteristic value change")
)

// connectOrdered connects to the system bus with godbus's sequential signal
// handler, the first precondition of notificationConsumer. It needs a bus, so
// it is exercised only where there is one.
func connectOrdered() (*dbus.Conn, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithSignalHandler(dbus.NewSequentialSignalHandler()))
	if err != nil {
		return nil, fmt.Errorf("connect to the system bus: %w", err)
	}
	return conn, nil
}

// characteristicMatch is the match rule that narrows the connection's signals
// to the property changes of one characteristic, so that other devices and
// other characteristics never reach the consumer. The consumer checks the
// path again, because a match rule is only a request to the bus.
func characteristicMatch(path dbus.ObjectPath) []dbus.MatchOption {
	return []dbus.MatchOption{
		dbus.WithMatchSender("org.bluez"),
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(propertiesInterface),
		dbus.WithMatchMember(propertiesChangedMember),
	}
}

// notificationConsumer passes the notifications of one characteristic to its
// sink in the order godbus delivered them. It is the only reader of the
// signal channel, so it keeps that order.
//
// Two preconditions hold the ordering, and the type system cannot check
// either:
//
//   - The connection must be built with
//     dbus.WithSignalHandler(dbus.NewSequentialSignalHandler()). The default
//     handler delivers signals from deferred goroutines, which reorder
//     back-to-back notifications. connectOrdered builds such a connection.
//   - The request's write and the signal subscription must share that one
//     connection, since Sequence counts per connection. A key from another
//     connection, such as the bluetooth library's shared system bus, means
//     nothing next to these.
//
// The ordering key is the signal's Sequence. godbus numbers every message its
// connection accepts, 1, 2, 3, ... in the order its single reader goroutine
// takes them off the bus, and stamps the number before the signal goes to the
// handler. Within one connection the numbers are therefore strictly
// increasing in arrival order. They are not contiguous across one
// characteristic's notifications, because method replies, other signals and
// other characteristics use numbers too, so a gap proves nothing and is not
// rejected. A key that does not increase is the assembler's to reject.
type notificationConsumer struct {
	// path is the one characteristic whose notifications are consumed.
	path dbus.ObjectPath
	// sender, when set, is the unique bus name of BlueZ's connection; signals
	// from any other connection are ignored. Resolve it with GetNameOwner on
	// "org.bluez" over the same connection the signals arrive on.
	sender string
	// sink gets each notification's key and payload, one at a time, in order.
	sink func(key uint64, chunk []byte)
	// dropped, when set, hears of a signal for path that could not be used.
	dropped func(err error)
}

// consume handles signals from ch until it is closed.
func (c *notificationConsumer) consume(ch <-chan *dbus.Signal) {
	for sig := range ch {
		c.handle(sig)
	}
}

// handle passes the notification in sig to the sink, or ignores sig when it
// is not a Value change of the characteristic at c.path.
func (c *notificationConsumer) handle(sig *dbus.Signal) {
	if sig == nil || sig.Path != c.path || sig.Name != propertiesChangedName {
		return
	}
	if c.sender != "" && sig.Sender != c.sender {
		return
	}
	if len(sig.Body) < 2 {
		c.drop(fmt.Errorf("%w: body has %d values", errBadSignal, len(sig.Body)))
		return
	}
	if iface, _ := sig.Body[0].(string); iface != gattCharacteristicInterface {
		return
	}
	changed, ok := sig.Body[1].(map[string]dbus.Variant)
	if !ok {
		c.drop(fmt.Errorf("%w: changed properties are a %T", errBadSignal, sig.Body[1]))
		return
	}
	value, ok := changed[valueProperty]
	if !ok {
		return // some other property, such as Notifying
	}
	chunk, ok := value.Value().([]byte)
	if !ok {
		c.drop(fmt.Errorf("%w: Value is a %T", errBadSignal, value.Value()))
		return
	}
	if sig.Sequence == dbus.NoSequence {
		c.drop(errNoSequence)
		return
	}
	c.sink(uint64(sig.Sequence), chunk)
}

func (c *notificationConsumer) drop(err error) {
	if c.dropped != nil {
		c.dropped(err)
	}
}
