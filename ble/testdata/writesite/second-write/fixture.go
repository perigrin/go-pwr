// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A second write, by method name.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
func other() { characteristic.WriteWithoutResponse(statusRequest) }
