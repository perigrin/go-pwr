// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: The write passes some other variable.

package ble

const statusRequest byte = 0x20

var request = byte(0x20)

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{request}, options)
}
