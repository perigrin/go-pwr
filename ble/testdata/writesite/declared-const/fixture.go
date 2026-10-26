// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is not a []byte variable.

package ble

const statusRequest = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
