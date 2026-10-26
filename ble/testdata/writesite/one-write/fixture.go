// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: One write of statusRequest: the guard passes it.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
