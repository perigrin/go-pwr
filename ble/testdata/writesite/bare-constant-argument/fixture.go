// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: The write argument is statusRequest itself, not a one-byte slice of it.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
