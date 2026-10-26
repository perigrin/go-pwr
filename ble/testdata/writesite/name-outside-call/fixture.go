// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A write name held in a constant, where the guard cannot see its value.

package ble

var statusRequest = []byte{0x20}

const method = "org.bluez.GattCharacteristic1.WriteValue"

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, statusRequest, options)
}
