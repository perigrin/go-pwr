// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: An AcquireWrite call besides the one write.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
func other() { object.Call("org.bluez.GattCharacteristic1.AcquireWrite", 0, options) }
