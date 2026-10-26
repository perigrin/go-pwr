// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: The write argument is a literal, not statusRequest.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{0x20}, options)
}
