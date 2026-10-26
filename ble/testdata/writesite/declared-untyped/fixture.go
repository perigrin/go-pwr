// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is an untyped constant.

package ble

const statusRequest = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
