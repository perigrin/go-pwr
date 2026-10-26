// ABOUTME: Guard fixture declaring unexported byte helpers beside typed exported wrappers.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

type Mode int

type payload []byte

func writeRaw(b []byte) {}

func (m Mode) write(p payload) {}

type sink interface {
	put(b []byte)
}

func Request(m Mode) { writeRaw(nil) }

func Result() []byte { return nil }
