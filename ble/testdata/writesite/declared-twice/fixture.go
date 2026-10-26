// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is declared twice.

package ble

const statusRequest byte = 0x20
const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
