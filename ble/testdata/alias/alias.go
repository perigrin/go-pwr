// ABOUTME: Guard fixture declaring an exported alias of []byte used as a parameter.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

type Payload = []byte

func Send(p Payload) {}
