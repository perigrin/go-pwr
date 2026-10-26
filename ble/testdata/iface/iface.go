// ABOUTME: Guard fixture declaring an exported interface with a byte-taking method.
// ABOUTME: It is parsed by the byte-writer guard tests and never built.

package ble

type Writer interface {
	Write(b []byte) error
}
