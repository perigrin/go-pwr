// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: The only write has a joined name and another value.

package ble

const statusRequest byte = 0x20

func send() { object.Call(iface+".WriteValue", 0, []byte{0x21}, options) }
