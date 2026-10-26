// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A range variable shadows statusRequest.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
func change(b []byte) {
	for _, statusRequest := range b {
		_ = statusRequest
	}
}
