// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is declared with another value.

package ble

var statusRequest = []byte{0x21}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
