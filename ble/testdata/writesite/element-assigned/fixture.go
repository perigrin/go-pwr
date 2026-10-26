// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: statusRequest is changed at run time.

package ble

var statusRequest = []byte{0x20}

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
func change() { statusRequest[0] = 0x26 }
