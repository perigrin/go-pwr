// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: The write argument carries a second byte.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest, 0x26}, options)
}
