// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is held in another variable.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
func change() byte { x := statusRequest; return x }
