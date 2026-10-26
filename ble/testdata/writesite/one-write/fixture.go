// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: One write of []byte{statusRequest}: the guard passes it.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
