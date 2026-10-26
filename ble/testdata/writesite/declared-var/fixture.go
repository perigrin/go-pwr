// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is a variable, which can be changed.

package ble

var statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
