// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is the old []byte variable.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
