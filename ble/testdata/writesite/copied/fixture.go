// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is copied into a buffer.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
func change(b []byte) { copy(b, []byte{statusRequest}) }
