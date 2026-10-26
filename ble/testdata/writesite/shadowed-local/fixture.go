// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A local variable shadows statusRequest.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
func change() { statusRequest := byte(0x26); _ = []byte{statusRequest} }
