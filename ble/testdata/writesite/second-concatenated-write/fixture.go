// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: A second write, by a joined name, the way StartNotify is called.

package ble

const statusRequest byte = 0x20

func send() {
	object.CallWithContext(ctx, "org.bluez.GattCharacteristic1.WriteValue", 0, []byte{statusRequest}, options)
}
func other() {
	object.Call(gattCharacteristicInterface+".WriteValue", 0, []byte{statusRequest}, options)
}
