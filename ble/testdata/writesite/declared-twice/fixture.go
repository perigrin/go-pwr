// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is declared twice.

package ble

var statusRequest = []byte{0x20}
var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
