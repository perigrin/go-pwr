// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A second write, named by a string that is only WriteValue.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}

func other() { object.Call("WriteValue", 0, []byte{statusRequest}, options) }
