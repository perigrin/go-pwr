// ABOUTME: Fixture for the write-site guard; never compiled.
// ABOUTME: One write whose method name is joined from a constant and .WriteValue: the guard passes it.

package ble

const statusRequest byte = 0x20

func send() { object.Call(iface+".WriteValue", 0, []byte{statusRequest}, options) }
